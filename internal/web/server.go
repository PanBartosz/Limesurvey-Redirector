package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"limesurvey_redirector/internal/auth"
	"limesurvey_redirector/internal/config"
	"limesurvey_redirector/internal/credentials"
	"limesurvey_redirector/internal/limesurvey"
	"limesurvey_redirector/internal/models"
	"limesurvey_redirector/internal/routing"
	"limesurvey_redirector/internal/store"
)

type surveyService interface {
	BuildCandidates(ctx context.Context, route models.Route) ([]routing.Candidate, error)
}

type Server struct {
	cfg             config.Config
	store           *store.Store
	auth            *auth.Manager
	csrf            *csrfProtector
	lsService       surveyService
	instanceSecrets *credentials.Protector
	tmpl            *template.Template
}

type viewData struct {
	Title           string
	Path            string
	Authenticated   bool
	IsAdmin         bool
	Username        string
	AdminUsername   string
	CSRFToken       string
	Error           string
	Message         string
	PublicBaseURL   string
	Instances       []models.Instance
	Routes          []store.RouteListItem
	RouteCards      []routeCardView
	Route           models.Route
	RouteForm       routeFormState
	Algorithms      []routing.AlgorithmDefinition
	SimulationURL   string
	RecentDecisions []models.RedirectDecision
	Users           []models.User
}

type routeCardView struct {
	ID             int64
	Name           string
	Description    string
	Slug           string
	PublicURL      string
	AlgorithmID    string
	AlgorithmLabel string
	Enabled        bool
	OwnerUsername  string
	InstanceNames  []string
	Targets        []routeCardTargetView
}

type routeCardTargetView struct {
	SurveyID int64
	Weight   int
}

type routeFormState struct {
	Name              string
	Slug              string
	Description       string
	InstanceID        string
	Algorithm         string
	FuzzyThreshold    string
	FallbackURL       string
	ForwardQueryMode  string
	PendingEnabled    bool
	PendingTTLSeconds string
	PendingWeight     string
	StickinessEnabled bool
	StickinessMode    string
	StickinessParam   string
	Enabled           bool
	Targets           []routeFormTargetView
}

type routeFormTargetView struct {
	SurveyID string
	Weight   string
}

func NewServer(cfg config.Config, st *store.Store, authManager *auth.Manager, lsService *limesurvey.Service, instanceSecrets *credentials.Protector) (*Server, error) {
	tmpl, err := template.ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:             cfg,
		store:           st,
		auth:            authManager,
		csrf:            newCSRFProtector(cfg.SessionSecret, cfg.SecureCookies),
		lsService:       lsService,
		instanceSecrets: instanceSecrets,
		tmpl:            tmpl,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	mux.HandleFunc("/healthz", s.healthz)
	mux.HandleFunc("/readyz", s.readyz)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/favicon.svg", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/admin/login", s.login)
	mux.HandleFunc("/admin/logout", s.logout)
	mux.HandleFunc("/admin", s.requireAuth(s.dashboard))
	mux.HandleFunc("/admin/tutorial", s.requireAuth(s.tutorial))
	mux.HandleFunc("/admin/routes", s.requireAuth(s.routes))
	mux.HandleFunc("/admin/routes/", s.requireAuth(s.routeDetail))
	mux.HandleFunc("/api/routes/", s.requireAuth(s.routeSimulation))
	mux.HandleFunc("/admin/instances", s.requireAdmin(s.instances))
	mux.HandleFunc("/admin/users", s.requireAdmin(s.users))
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("/r/", s.redirect)
	return loggingMiddleware(securityHeadersMiddleware(mux))
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ready"))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if _, ok, err := s.validatedSession(w, r); err != nil {
			writeInternalServerError(w, err)
			return
		} else if ok {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
		s.render(w, r, http.StatusOK, "login.html", viewData{Title: "Sign In", Path: r.URL.Path, AdminUsername: s.cfg.AdminUsername})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := parsePostForm(w, r); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.csrf.validate(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")
	if username == "" || password == "" {
		s.render(w, r, http.StatusUnauthorized, "login.html", viewData{Title: "Sign In", Path: r.URL.Path, Error: "Username and password are required", AdminUsername: s.cfg.AdminUsername})
		return
	}

	if s.auth.CheckAdminCredentials(username, password) {
		s.auth.SetSession(w, auth.Session{Username: s.cfg.AdminUsername, Role: auth.RoleAdmin, SessionVersion: 0, ExpiresAt: time.Now().Add(24 * time.Hour)})
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	authUser, err := s.store.GetAuthUserByUsername(r.Context(), username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.render(w, r, http.StatusUnauthorized, "login.html", viewData{Title: "Sign In", Path: r.URL.Path, Error: "Invalid username or password", AdminUsername: s.cfg.AdminUsername})
			return
		}
		writeInternalServerError(w, err)
		return
	}
	if !authUser.User.Enabled || !auth.CheckPasswordHash(authUser.PasswordHash, password) {
		s.render(w, r, http.StatusUnauthorized, "login.html", viewData{Title: "Sign In", Path: r.URL.Path, Error: "Invalid username or password", AdminUsername: s.cfg.AdminUsername})
		return
	}

	s.auth.SetSession(w, auth.Session{
		Username:       authUser.User.Username,
		Role:           auth.Role(authUser.User.Role),
		SessionVersion: authUser.User.SessionVersion,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	})
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := parsePostForm(w, r); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.csrf.validate(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.auth.ClearSession(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	session := s.mustSession(r)
	ctx := r.Context()
	instances, err := s.availableInstances(ctx, session)
	if err != nil {
		writeInternalServerError(w, err)
		return
	}
	routes, err := s.visibleRoutes(ctx, session)
	if err != nil {
		writeInternalServerError(w, err)
		return
	}
	routeCards, err := s.visibleRouteCards(ctx, session)
	if err != nil {
		writeInternalServerError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, "dashboard.html", viewData{
		Title:      "Dashboard",
		Path:       r.URL.Path,
		Instances:  instances,
		Routes:     routes,
		RouteCards: routeCards,
	})
}

func (s *Server) tutorial(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.render(w, r, http.StatusOK, "tutorial.html", viewData{
		Title: "Tutorial",
		Path:  r.URL.Path,
	})
}

func (s *Server) instances(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method == http.MethodPost {
		if err := parsePostForm(w, r); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !s.csrf.validate(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		input := store.CreateInstanceInput{
			Name:             strings.TrimSpace(r.FormValue("name")),
			SurveyBaseURL:    strings.TrimSpace(r.FormValue("survey_base_url")),
			RemoteControlURL: strings.TrimSpace(r.FormValue("remotecontrol_url")),
			RPCTransport:     models.RPCTransport(strings.TrimSpace(r.FormValue("rpc_transport"))),
			Username:         strings.TrimSpace(r.FormValue("username")),
			Enabled:          r.FormValue("enabled") == "on",
		}
		password := r.FormValue("password")
		if err := s.validateInstanceInput(input); err != nil {
			instances, _ := s.store.ListInstances(ctx)
			s.render(w, r, http.StatusBadRequest, "instances.html", viewData{Title: "Instances", Path: r.URL.Path, Instances: instances, Error: err.Error()})
			return
		}
		if err := validateInstancePassword(password); err != nil {
			instances, _ := s.store.ListInstances(ctx)
			s.render(w, r, http.StatusBadRequest, "instances.html", viewData{Title: "Instances", Path: r.URL.Path, Instances: instances, Error: err.Error()})
			return
		}
		encryptedPassword, err := s.instanceSecrets.Encrypt(password)
		if err != nil {
			writeInternalServerError(w, err)
			return
		}
		input.EncryptedPassword = encryptedPassword
		if _, err := s.store.CreateInstance(ctx, input); err != nil {
			instances, _ := s.store.ListInstances(ctx)
			s.render(w, r, http.StatusBadRequest, "instances.html", viewData{Title: "Instances", Path: r.URL.Path, Instances: instances, Error: friendlyStoreError(err)})
			return
		}
		http.Redirect(w, r, "/admin/instances", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	instances, err := s.store.ListInstances(ctx)
	if err != nil {
		writeInternalServerError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, "instances.html", viewData{Title: "Instances", Path: r.URL.Path, Instances: instances})
}

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method == http.MethodPost {
		if err := parsePostForm(w, r); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !s.csrf.validate(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		action := defaultIfEmpty(r.FormValue("action"), "create")
		switch action {
		case "create":
			username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
			password := r.FormValue("password")
			if username == "" || password == "" {
				s.renderUsersPage(w, r, http.StatusBadRequest, "Username and password are required")
				return
			}
			if err := validateUsername(username); err != nil {
				s.renderUsersPage(w, r, http.StatusBadRequest, err.Error())
				return
			}
			if err := validateUserPassword(password); err != nil {
				s.renderUsersPage(w, r, http.StatusBadRequest, err.Error())
				return
			}
			if strings.EqualFold(username, s.auth.AdminUsername()) {
				s.renderUsersPage(w, r, http.StatusBadRequest, "That username is reserved for the env-configured admin")
				return
			}
			hash, err := auth.HashPassword(password)
			if err != nil {
				writeInternalServerError(w, err)
				return
			}
			if _, err := s.store.CreateUser(ctx, store.CreateUserInput{Username: username, PasswordHash: hash, Role: string(auth.RoleUser), Enabled: r.FormValue("enabled") == "on"}); err != nil {
				s.renderUsersPage(w, r, http.StatusBadRequest, friendlyStoreError(err))
				return
			}
		case "toggle_enabled":
			userID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
			if err != nil || userID <= 0 {
				s.renderUsersPage(w, r, http.StatusBadRequest, "Invalid user")
				return
			}
			user, err := s.store.GetUser(ctx, userID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					s.renderUsersPage(w, r, http.StatusBadRequest, "User not found")
					return
				}
				writeInternalServerError(w, err)
				return
			}
			if err := s.store.UpdateUserEnabled(ctx, userID, !user.Enabled); err != nil {
				writeInternalServerError(w, err)
				return
			}
		case "reset_password":
			userID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
			if err != nil || userID <= 0 {
				s.renderUsersPage(w, r, http.StatusBadRequest, "Invalid user")
				return
			}
			password := r.FormValue("new_password")
			if err := validateUserPassword(password); err != nil {
				s.renderUsersPage(w, r, http.StatusBadRequest, err.Error())
				return
			}
			hash, err := auth.HashPassword(password)
			if err != nil {
				writeInternalServerError(w, err)
				return
			}
			if err := s.store.UpdateUserPasswordHash(ctx, userID, hash); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					s.renderUsersPage(w, r, http.StatusBadRequest, "User not found")
					return
				}
				writeInternalServerError(w, err)
				return
			}
		case "delete":
			userID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
			if err != nil || userID <= 0 {
				s.renderUsersPage(w, r, http.StatusBadRequest, "Invalid user")
				return
			}
			user, err := s.store.GetUser(ctx, userID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					s.renderUsersPage(w, r, http.StatusBadRequest, "User not found")
					return
				}
				writeInternalServerError(w, err)
				return
			}
			ownedRoutes, err := s.store.CountRoutesByOwner(ctx, user.Username, user.Role)
			if err != nil {
				writeInternalServerError(w, err)
				return
			}
			if ownedRoutes > 0 {
				s.renderUsersPage(w, r, http.StatusBadRequest, "Cannot delete a user who still owns routes. Delete or reassign those routes first.")
				return
			}
			if err := s.store.DeleteUser(ctx, userID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					s.renderUsersPage(w, r, http.StatusBadRequest, "User not found")
					return
				}
				writeInternalServerError(w, err)
				return
			}
		default:
			s.renderUsersPage(w, r, http.StatusBadRequest, "Unsupported action")
			return
		}
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.renderUsersPage(w, r, http.StatusOK, "")
}

func (s *Server) routes(w http.ResponseWriter, r *http.Request) {
	session := s.mustSession(r)
	ctx := r.Context()
	instances, err := s.availableInstances(ctx, session)
	if err != nil {
		writeInternalServerError(w, err)
		return
	}
	if r.Method == http.MethodPost {
		if err := parsePostForm(w, r); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !s.csrf.validate(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		input, err := s.buildRouteInput(ctx, r, instances, session.Username, string(session.Role), 0)
		if err != nil {
			routeCards, _ := s.visibleRouteCards(ctx, session)
			s.render(w, r, http.StatusBadRequest, "routes.html", viewData{
				Title:      "Routes",
				Path:       r.URL.Path,
				Instances:  instances,
				RouteCards: routeCards,
				RouteForm:  routeFormFromRequest(r),
				Algorithms: routing.Definitions(),
				Error:      err.Error(),
			})
			return
		}
		id, err := s.store.CreateRoute(ctx, input)
		if err != nil {
			routeCards, _ := s.visibleRouteCards(ctx, session)
			s.render(w, r, http.StatusBadRequest, "routes.html", viewData{
				Title:      "Routes",
				Path:       r.URL.Path,
				Instances:  instances,
				RouteCards: routeCards,
				RouteForm:  routeFormFromRequest(r),
				Algorithms: routing.Definitions(),
				Error:      friendlyStoreError(err),
			})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/admin/routes/%d", id), http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	routeCards, err := s.visibleRouteCards(ctx, session)
	if err != nil {
		writeInternalServerError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, "routes.html", viewData{
		Title:      "Routes",
		Path:       r.URL.Path,
		Instances:  instances,
		RouteCards: routeCards,
		RouteForm:  defaultRouteForm(),
		Algorithms: routing.Definitions(),
	})
}

func (s *Server) routeDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/admin/routes/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(path, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	session := s.mustSession(r)
	route, err := s.store.GetRoute(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !routeVisibleToPrincipal(session.IsAdmin(), session.Username, string(session.Role), route) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.renderRouteDetailPage(w, r, http.StatusOK, route, routeFormFromRoute(route), "")
		return
	case http.MethodPost:
		if err := parsePostForm(w, r); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !s.csrf.validate(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		action := defaultIfEmpty(r.FormValue("action"), "update")
		switch action {
		case "update":
			instances, err := s.availableInstances(ctx, session)
			if err != nil {
				writeInternalServerError(w, err)
				return
			}
			input, err := s.buildRouteInput(ctx, r, instances, route.OwnerUsername, route.OwnerRole, route.ID)
			if err != nil {
				draftRoute := routeDraftFromRequest(route, r)
				s.renderRouteDetailPage(w, r, http.StatusBadRequest, draftRoute, routeFormFromRequest(r), err.Error())
				return
			}
			if err := s.store.UpdateRoute(ctx, route.ID, input); err != nil {
				draftRoute := routeDraftFromRequest(route, r)
				s.renderRouteDetailPage(w, r, http.StatusBadRequest, draftRoute, routeFormFromRequest(r), friendlyStoreError(err))
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/admin/routes/%d", route.ID), http.StatusSeeOther)
			return
		case "delete":
			if err := s.store.DeleteRoute(ctx, route.ID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					http.NotFound(w, r)
					return
				}
				writeInternalServerError(w, err)
				return
			}
			http.Redirect(w, r, "/admin/routes", http.StatusSeeOther)
			return
		default:
			s.renderRouteDetailPage(w, r, http.StatusBadRequest, route, routeFormFromRoute(route), "Unsupported action")
			return
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

func (s *Server) routeSimulation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/routes/")
	if !strings.HasSuffix(path, "/simulate") {
		http.NotFound(w, r)
		return
	}
	idString := strings.TrimSuffix(path, "/simulate")
	idString = strings.TrimSuffix(idString, "/")
	id, err := strconv.ParseInt(idString, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	session := s.mustSession(r)
	route, err := s.store.GetRoute(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !routeVisibleToPrincipal(session.IsAdmin(), session.Username, string(session.Role), route) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	payload := s.simulateRoute(ctx, route)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	slug := strings.TrimPrefix(r.URL.Path, "/r/")
	if slug == "" {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	route, err := s.store.GetRouteBySlug(ctx, slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !route.Enabled {
		http.Error(w, "route disabled", http.StatusNotFound)
		return
	}

	result, candidates, err := s.evaluateRoute(ctx, route)
	requestID := fmt.Sprintf("req_%d", time.Now().UnixNano())
	requestQuery := redactQueryString(r.URL.RawQuery)
	decision := models.RedirectDecision{
		RouteID:           route.ID,
		RequestID:         requestID,
		DecisionMode:      route.Algorithm,
		RequestQuery:      requestQuery,
		CandidateSnapshot: limesurvey.SnapshotJSON(candidates),
		Status:            "failed",
	}

	if err != nil || result.Chosen == nil {
		if route.FallbackURL != "" {
			decision.Status = "fallback"
			decision.ForwardedQuery = requestQuery
			_ = s.store.InsertRedirectDecision(ctx, decision)
			fallback := appendForwardedQuery(route.FallbackURL, r.URL.Query())
			http.Redirect(w, r, fallback, http.StatusSeeOther)
			return
		}
		_ = s.store.InsertRedirectDecision(ctx, decision)
		http.Error(w, "no eligible targets", http.StatusServiceUnavailable)
		return
	}

	chosen := result.Chosen.Target
	targetID := chosen.ID
	decision.RouteTargetID = &targetID
	decision.Status = "redirected"
	decision.ChosenScore = &result.ChosenScore
	finalURL := buildSurveyURL(chosen.Instance.SurveyBaseURL, chosen.SurveyID, r.URL.Query(), route.ForwardQueryMode)
	parsedURL, _ := url.Parse(finalURL)
	decision.ForwardedQuery = redactQueryString(parsedURL.RawQuery)
	_ = s.store.InsertRedirectDecision(ctx, decision)
	http.Redirect(w, r, finalURL, http.StatusSeeOther)
}

func (s *Server) simulateRoute(ctx context.Context, route models.Route) simulationResponse {
	result, candidates, err := s.evaluateRoute(ctx, route)
	return buildSimulationResponse(route, candidates, result, err)
}

func (s *Server) evaluateRoute(ctx context.Context, route models.Route) (routing.Result, []routing.Candidate, error) {
	candidates, err := s.lsService.BuildCandidates(ctx, route)
	if err != nil {
		return routing.Result{}, nil, err
	}
	for _, candidate := range candidates {
		stats := models.TargetStats{
			RouteTargetID:       candidate.Target.ID,
			CompletedResponses:  candidate.CompletedResponses,
			IncompleteResponses: candidate.IncompleteResponses,
			FullResponses:       candidate.FullResponses,
			SurveyActive:        candidate.SurveyActive,
			FetchError:          candidate.FetchError,
			FetchedAt:           time.Now().UTC(),
		}
		if err := s.store.UpsertTargetStats(ctx, candidate.Target.ID, stats); err != nil {
			log.Printf("target stats update failed for %d: %v", candidate.Target.ID, err)
		}
	}
	result, err := routing.Select(route, candidates)
	return result, candidates, err
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok, err := s.validatedSession(w, r); err != nil {
			writeInternalServerError(w, err)
			return
		} else if !ok {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok, err := s.validatedSession(w, r)
		if err != nil {
			writeInternalServerError(w, err)
			return
		}
		if !ok {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		if !session.IsAdmin() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, data viewData) {
	data.PublicBaseURL = s.cfg.PublicBaseURL
	data.AdminUsername = s.cfg.AdminUsername
	data.CSRFToken = s.csrf.ensureToken(w, r)
	if session, ok, err := s.validatedSession(w, r); err == nil && ok {
		data.Authenticated = true
		data.IsAdmin = session.IsAdmin()
		data.Username = session.Username
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template execute failed: %v", err)
	}
}

func (s *Server) validatedSession(w http.ResponseWriter, r *http.Request) (auth.Session, bool, error) {
	session, ok := s.auth.CurrentSession(r)
	if !ok {
		return auth.Session{}, false, nil
	}
	if session.IsAdmin() {
		return session, true, nil
	}

	authUser, err := s.store.GetAuthUserByUsername(r.Context(), session.Username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.auth.ClearSession(w)
			return auth.Session{}, false, nil
		}
		return auth.Session{}, false, err
	}
	if !authUser.User.Enabled || authUser.User.Role != string(session.Role) {
		s.auth.ClearSession(w)
		return auth.Session{}, false, nil
	}
	if authUser.User.SessionVersion != session.SessionVersion {
		s.auth.ClearSession(w)
		return auth.Session{}, false, nil
	}
	return auth.Session{
		Username:       authUser.User.Username,
		Role:           auth.Role(authUser.User.Role),
		SessionVersion: authUser.User.SessionVersion,
		ExpiresAt:      session.ExpiresAt,
	}, true, nil
}

func (s *Server) renderUsersPage(w http.ResponseWriter, r *http.Request, status int, errorMsg string) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeInternalServerError(w, err)
		return
	}
	s.render(w, r, status, "users.html", viewData{
		Title: "Users",
		Path:  "/admin/users",
		Users: users,
		Error: errorMsg,
	})
}

func (s *Server) renderRouteDetailPage(w http.ResponseWriter, r *http.Request, status int, route models.Route, formState routeFormState, errorMsg string) {
	session := s.mustSession(r)
	instances, err := s.availableInstances(r.Context(), session)
	if err != nil {
		writeInternalServerError(w, err)
		return
	}
	decisions, err := s.store.ListRecentDecisions(r.Context(), route.ID, 20)
	if err != nil {
		writeInternalServerError(w, err)
		return
	}
	s.render(w, r, status, "route_detail.html", viewData{
		Title:           defaultIfEmpty(route.Name, "Route"),
		Path:            r.URL.Path,
		Route:           route,
		RouteForm:       formState,
		Instances:       instances,
		Algorithms:      routing.Definitions(),
		SimulationURL:   fmt.Sprintf("/api/routes/%d/simulate", route.ID),
		RecentDecisions: decisions,
		Error:           errorMsg,
	})
}

func (s *Server) availableInstances(ctx context.Context, session auth.Session) ([]models.Instance, error) {
	if session.IsAdmin() {
		return s.store.ListInstances(ctx)
	}
	instances, err := s.store.ListEnabledInstances(ctx)
	if err != nil {
		return nil, err
	}
	return scrubInstancesForUser(instances), nil
}

func (s *Server) visibleRoutes(ctx context.Context, session auth.Session) ([]store.RouteListItem, error) {
	if session.IsAdmin() {
		return s.store.ListRoutes(ctx)
	}
	return s.store.ListRoutesByOwner(ctx, session.Username, string(session.Role))
}

func (s *Server) visibleRouteDetails(ctx context.Context, session auth.Session) ([]models.Route, error) {
	items, err := s.visibleRoutes(ctx, session)
	if err != nil {
		return nil, err
	}
	routes := make([]models.Route, 0, len(items))
	for _, item := range items {
		route, err := s.store.GetRoute(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func (s *Server) visibleRouteCards(ctx context.Context, session auth.Session) ([]routeCardView, error) {
	routes, err := s.visibleRouteDetails(ctx, session)
	if err != nil {
		return nil, err
	}
	return buildRouteCards(routes, s.cfg.PublicBaseURL), nil
}

func (s *Server) mustSession(r *http.Request) auth.Session {
	session, _ := s.auth.CurrentSession(r)
	return session
}

func (s *Server) validateInstanceInput(input store.CreateInstanceInput) error {
	if strings.TrimSpace(input.Name) == "" || len(strings.TrimSpace(input.Name)) > 120 {
		return fmt.Errorf("instance name is required and must be at most 120 characters")
	}
	if _, ok := allowedTransports[input.RPCTransport]; !ok {
		return fmt.Errorf("RPC transport is invalid")
	}
	if _, err := validateAbsoluteHTTPURL(input.SurveyBaseURL); err != nil {
		return fmt.Errorf("survey base URL: %w", err)
	}
	if _, err := validateAbsoluteHTTPURL(input.RemoteControlURL); err != nil {
		return fmt.Errorf("RemoteControl URL: %w", err)
	}
	if strings.TrimSpace(input.Username) == "" || len(strings.TrimSpace(input.Username)) > 128 {
		return fmt.Errorf("instance username is required")
	}
	return nil
}

func (s *Server) buildRouteInput(ctx context.Context, r *http.Request, instances []models.Instance, ownerUsername, ownerRole string, excludeRouteID int64) (store.CreateRouteInput, error) {
	instanceID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("instance_id")), 10, 64)
	if err != nil || instanceID <= 0 || !containsInstance(instances, instanceID) {
		return store.CreateRouteInput{}, fmt.Errorf("select a valid configured instance")
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || len(name) > 120 {
		return store.CreateRouteInput{}, fmt.Errorf("route name is required and must be at most 120 characters")
	}
	slug := strings.ToLower(strings.TrimSpace(r.FormValue("slug")))
	if err := validateSlug(slug); err != nil {
		return store.CreateRouteInput{}, err
	}
	taken, err := s.store.RouteSlugTaken(ctx, slug, excludeRouteID)
	if err != nil {
		return store.CreateRouteInput{}, err
	}
	if taken {
		return store.CreateRouteInput{}, fmt.Errorf("route slug already exists")
	}
	description := strings.TrimSpace(r.FormValue("description"))
	if len(description) > 2000 {
		return store.CreateRouteInput{}, fmt.Errorf("description must be at most 2000 characters")
	}

	algorithm := defaultIfEmpty(strings.TrimSpace(r.FormValue("algorithm")), "completed_fuzzy")
	if !knownAlgorithm(algorithm) {
		return store.CreateRouteInput{}, fmt.Errorf("algorithm is invalid")
	}
	fuzzyThreshold := 0
	if algorithmNeedsFuzzyThreshold(algorithm) || strings.TrimSpace(r.FormValue("fuzzy_threshold")) != "" {
		fuzzyThreshold, err = strconv.Atoi(defaultIfEmpty(strings.TrimSpace(r.FormValue("fuzzy_threshold")), "0"))
		if err != nil || fuzzyThreshold < 0 || fuzzyThreshold > 100000 {
			return store.CreateRouteInput{}, fmt.Errorf("fuzzy threshold is invalid")
		}
	}
	pendingTTL, err := strconv.Atoi(defaultIfEmpty(strings.TrimSpace(r.FormValue("pending_ttl_seconds")), "1800"))
	if err != nil || pendingTTL < 60 || pendingTTL > 604800 {
		return store.CreateRouteInput{}, fmt.Errorf("pending TTL must be between 60 and 604800 seconds")
	}
	pendingWeight, err := strconv.ParseFloat(defaultIfEmpty(strings.TrimSpace(r.FormValue("pending_weight")), "1"), 64)
	if err != nil || pendingWeight < 0 || pendingWeight > 1000 {
		return store.CreateRouteInput{}, fmt.Errorf("pending weight is invalid")
	}

	forwardMode := models.QueryForwardMode(defaultIfEmpty(strings.TrimSpace(r.FormValue("forward_query_mode")), string(models.QueryForwardAll)))
	if _, ok := allowedForwardModes[forwardMode]; !ok {
		return store.CreateRouteInput{}, fmt.Errorf("forward query mode is invalid")
	}

	allInstances, err := s.store.ListInstances(ctx)
	if err != nil {
		return store.CreateRouteInput{}, err
	}
	fallbackURL, err := validateFallbackURL(r.FormValue("fallback_url"), allowedFallbackHosts(allInstances, s.cfg.PublicBaseURL))
	if err != nil {
		return store.CreateRouteInput{}, err
	}

	stickinessEnabled := r.FormValue("stickiness_enabled") == "on"
	stickinessMode := models.StickinessMode(strings.TrimSpace(r.FormValue("stickiness_mode")))
	stickinessParam := strings.TrimSpace(r.FormValue("stickiness_param"))
	if !stickinessEnabled {
		stickinessMode = models.StickinessOff
		stickinessParam = ""
	}
	if _, ok := allowedStickinessModes[stickinessMode]; !ok {
		return store.CreateRouteInput{}, fmt.Errorf("stickiness mode is invalid")
	}
	if stickinessMode == models.StickinessQueryKey {
		if err := validateQueryParamKey(stickinessParam); err != nil {
			return store.CreateRouteInput{}, err
		}
	} else {
		stickinessParam = ""
	}

	targets, err := parseRouteTargets(r)
	if err != nil {
		return store.CreateRouteInput{}, err
	}
	if len(targets) == 0 {
		return store.CreateRouteInput{}, fmt.Errorf("provide at least one survey ID")
	}
	if len(targets) > 200 {
		return store.CreateRouteInput{}, fmt.Errorf("too many survey IDs; limit is 200")
	}
	if !algorithmUsesTargetWeights(algorithm) {
		for index := range targets {
			targets[index].Weight = 1
		}
	}

	return store.CreateRouteInput{
		Slug:              slug,
		Name:              name,
		Description:       description,
		OwnerUsername:     ownerUsername,
		OwnerRole:         ownerRole,
		Algorithm:         algorithm,
		FuzzyThreshold:    fuzzyThreshold,
		PendingEnabled:    r.FormValue("pending_enabled") == "on",
		PendingTTLSeconds: pendingTTL,
		PendingWeight:     pendingWeight,
		ForwardQueryMode:  forwardMode,
		FallbackURL:       fallbackURL,
		Enabled:           r.FormValue("enabled") == "on",
		StickinessEnabled: stickinessEnabled,
		StickinessMode:    stickinessMode,
		StickinessParam:   stickinessParam,
		InstanceID:        instanceID,
		Targets:           targets,
	}, nil
}

func knownAlgorithm(id string) bool {
	for _, definition := range routing.Definitions() {
		if definition.ID == id {
			return true
		}
	}
	return false
}

func algorithmNeedsFuzzyThreshold(id string) bool {
	definition := routing.DefinitionByID(id)
	return definition.NeedsFuzzyThreshold
}

func algorithmUsesTargetWeights(id string) bool {
	definition := routing.DefinitionByID(id)
	return definition.UsesTargetWeights
}

func friendlyStoreError(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "users.username"):
		return "username already exists"
	case strings.Contains(message, "instances.name"):
		return "instance name already exists"
	case strings.Contains(message, "routes.slug"):
		return "route slug already exists"
	default:
		return "save failed"
	}
}

func buildSurveyURL(base string, surveyID int64, sourceQuery url.Values, mode models.QueryForwardMode) string {
	parsed, err := url.Parse(base)
	if err != nil {
		return base
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strconv.FormatInt(surveyID, 10)
	query := parsed.Query()
	if mode == models.QueryForwardAll {
		for key, values := range sourceQuery {
			for _, value := range values {
				query.Add(key, value)
			}
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func appendForwardedQuery(base string, sourceQuery url.Values) string {
	parsed, err := url.Parse(base)
	if err != nil {
		return base
	}
	query := parsed.Query()
	for key, values := range sourceQuery {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func parseSurveyIDs(raw string) []int64 {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ',' || r == ';' || r == ' ' || r == '\t'
	})
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		value, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err == nil && value > 0 {
			ids = append(ids, value)
		}
	}
	return ids
}

func containsInstance(instances []models.Instance, instanceID int64) bool {
	for _, instance := range instances {
		if instance.ID == instanceID {
			return true
		}
	}
	return false
}

func defaultIfEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func routeDraftFromRequest(route models.Route, r *http.Request) models.Route {
	draft := route
	draft.Name = strings.TrimSpace(r.FormValue("name"))
	draft.Slug = strings.ToLower(strings.TrimSpace(r.FormValue("slug")))
	draft.Description = strings.TrimSpace(r.FormValue("description"))
	draft.Algorithm = defaultIfEmpty(strings.TrimSpace(r.FormValue("algorithm")), route.Algorithm)
	if value, err := strconv.Atoi(strings.TrimSpace(r.FormValue("fuzzy_threshold"))); err == nil {
		draft.FuzzyThreshold = value
	}
	draft.PendingEnabled = r.FormValue("pending_enabled") == "on"
	if value, err := strconv.Atoi(strings.TrimSpace(r.FormValue("pending_ttl_seconds"))); err == nil {
		draft.PendingTTLSeconds = value
	}
	if value, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("pending_weight")), 64); err == nil {
		draft.PendingWeight = value
	}
	draft.ForwardQueryMode = models.QueryForwardMode(defaultIfEmpty(strings.TrimSpace(r.FormValue("forward_query_mode")), string(route.ForwardQueryMode)))
	draft.FallbackURL = strings.TrimSpace(r.FormValue("fallback_url"))
	draft.Enabled = r.FormValue("enabled") == "on"
	draft.StickinessEnabled = r.FormValue("stickiness_enabled") == "on"
	draft.StickinessMode = models.StickinessMode(strings.TrimSpace(r.FormValue("stickiness_mode")))
	draft.StickinessParam = strings.TrimSpace(r.FormValue("stickiness_param"))
	return draft
}

func routeInstanceID(route models.Route) int64 {
	if len(route.Targets) == 0 {
		return 0
	}
	return route.Targets[0].InstanceID
}

func routeSurveyIDs(route models.Route) string {
	if len(route.Targets) == 0 {
		return ""
	}
	values := make([]string, 0, len(route.Targets))
	for _, target := range route.Targets {
		values = append(values, strconv.FormatInt(target.SurveyID, 10))
	}
	return strings.Join(values, "\n")
}

func defaultRouteForm() routeFormState {
	return routeFormState{
		Algorithm:         "completed_fuzzy",
		FuzzyThreshold:    "5",
		ForwardQueryMode:  string(models.QueryForwardAll),
		PendingTTLSeconds: "1800",
		PendingWeight:     "1",
		Enabled:           true,
		Targets: []routeFormTargetView{
			{SurveyID: "", Weight: "1"},
		},
	}
}

func routeFormFromRoute(route models.Route) routeFormState {
	form := routeFormState{
		Name:              route.Name,
		Slug:              route.Slug,
		Description:       route.Description,
		InstanceID:        "",
		Algorithm:         route.Algorithm,
		FuzzyThreshold:    strconv.Itoa(route.FuzzyThreshold),
		FallbackURL:       route.FallbackURL,
		ForwardQueryMode:  string(route.ForwardQueryMode),
		PendingEnabled:    route.PendingEnabled,
		PendingTTLSeconds: strconv.Itoa(route.PendingTTLSeconds),
		PendingWeight:     strconv.FormatFloat(route.PendingWeight, 'f', -1, 64),
		StickinessEnabled: route.StickinessEnabled,
		StickinessMode:    string(route.StickinessMode),
		StickinessParam:   route.StickinessParam,
		Enabled:           route.Enabled,
		Targets:           make([]routeFormTargetView, 0, len(route.Targets)),
	}
	if instanceID := routeInstanceID(route); instanceID > 0 {
		form.InstanceID = strconv.FormatInt(instanceID, 10)
	}
	for _, target := range route.Targets {
		form.Targets = append(form.Targets, routeFormTargetView{
			SurveyID: strconv.FormatInt(target.SurveyID, 10),
			Weight:   strconv.Itoa(max(target.Weight, 1)),
		})
	}
	if len(form.Targets) == 0 {
		form.Targets = []routeFormTargetView{{SurveyID: "", Weight: "1"}}
	}
	if form.Algorithm == "" {
		form.Algorithm = "completed_fuzzy"
	}
	if form.ForwardQueryMode == "" {
		form.ForwardQueryMode = string(models.QueryForwardAll)
	}
	return form
}

func routeFormFromRequest(r *http.Request) routeFormState {
	form := routeFormState{
		Name:              strings.TrimSpace(r.FormValue("name")),
		Slug:              strings.ToLower(strings.TrimSpace(r.FormValue("slug"))),
		Description:       strings.TrimSpace(r.FormValue("description")),
		InstanceID:        strings.TrimSpace(r.FormValue("instance_id")),
		Algorithm:         defaultIfEmpty(strings.TrimSpace(r.FormValue("algorithm")), "completed_fuzzy"),
		FuzzyThreshold:    defaultIfEmpty(strings.TrimSpace(r.FormValue("fuzzy_threshold")), "0"),
		FallbackURL:       strings.TrimSpace(r.FormValue("fallback_url")),
		ForwardQueryMode:  defaultIfEmpty(strings.TrimSpace(r.FormValue("forward_query_mode")), string(models.QueryForwardAll)),
		PendingEnabled:    r.FormValue("pending_enabled") == "on",
		PendingTTLSeconds: defaultIfEmpty(strings.TrimSpace(r.FormValue("pending_ttl_seconds")), "1800"),
		PendingWeight:     defaultIfEmpty(strings.TrimSpace(r.FormValue("pending_weight")), "1"),
		StickinessEnabled: r.FormValue("stickiness_enabled") == "on",
		StickinessMode:    strings.TrimSpace(r.FormValue("stickiness_mode")),
		StickinessParam:   strings.TrimSpace(r.FormValue("stickiness_param")),
		Enabled:           r.FormValue("enabled") == "on",
	}

	if targets, err := parseRouteTargetsForDraft(r); err == nil && len(targets) > 0 {
		form.Targets = targets
	} else if surveyIDs := strings.TrimSpace(r.FormValue("survey_ids")); surveyIDs != "" {
		form.Targets = targetsFromLegacySurveyIDs(surveyIDs)
	}
	if len(form.Targets) == 0 {
		form.Targets = []routeFormTargetView{{SurveyID: "", Weight: "1"}}
	}
	return form
}

func parseRouteTargets(r *http.Request) ([]store.RouteTargetInput, error) {
	surveyValues := r.Form["target_survey_id"]
	weightValues := r.Form["target_weight"]
	if len(surveyValues) == 0 {
		legacy := parseSurveyIDs(r.FormValue("survey_ids"))
		targets := make([]store.RouteTargetInput, 0, len(legacy))
		for _, surveyID := range legacy {
			targets = append(targets, store.RouteTargetInput{SurveyID: surveyID, Weight: 1})
		}
		return targets, nil
	}

	targets := make([]store.RouteTargetInput, 0, len(surveyValues))
	for index, rawSurveyID := range surveyValues {
		surveyText := strings.TrimSpace(rawSurveyID)
		weightText := "1"
		if index < len(weightValues) {
			weightText = strings.TrimSpace(weightValues[index])
		}
		if surveyText == "" && weightText == "" {
			continue
		}
		if surveyText == "" {
			return nil, fmt.Errorf("each target row needs a survey ID")
		}
		surveyID, err := strconv.ParseInt(surveyText, 10, 64)
		if err != nil || surveyID <= 0 {
			return nil, fmt.Errorf("survey IDs must be positive integers")
		}
		weight := 1
		if weightText != "" {
			value, err := strconv.Atoi(weightText)
			if err != nil || value <= 0 || value > 100000 {
				return nil, fmt.Errorf("target weights must be whole numbers between 1 and 100000")
			}
			weight = value
		}
		targets = append(targets, store.RouteTargetInput{SurveyID: surveyID, Weight: weight})
	}
	return targets, nil
}

func parseRouteTargetsForDraft(r *http.Request) ([]routeFormTargetView, error) {
	surveyValues := r.Form["target_survey_id"]
	weightValues := r.Form["target_weight"]
	if len(surveyValues) == 0 {
		return nil, nil
	}
	targets := make([]routeFormTargetView, 0, len(surveyValues))
	for index, rawSurveyID := range surveyValues {
		weight := "1"
		if index < len(weightValues) {
			weight = weightValues[index]
		}
		if strings.TrimSpace(rawSurveyID) == "" && strings.TrimSpace(weight) == "" {
			continue
		}
		targets = append(targets, routeFormTargetView{
			SurveyID: strings.TrimSpace(rawSurveyID),
			Weight:   defaultIfEmpty(strings.TrimSpace(weight), "1"),
		})
	}
	return targets, nil
}

func targetsFromLegacySurveyIDs(raw string) []routeFormTargetView {
	ids := parseSurveyIDs(raw)
	if len(ids) == 0 {
		return []routeFormTargetView{{SurveyID: "", Weight: "1"}}
	}
	targets := make([]routeFormTargetView, 0, len(ids))
	for _, surveyID := range ids {
		targets = append(targets, routeFormTargetView{
			SurveyID: strconv.FormatInt(surveyID, 10),
			Weight:   "1",
		})
	}
	return targets
}

func buildRouteCards(routes []models.Route, publicBaseURL string) []routeCardView {
	cards := make([]routeCardView, 0, len(routes))
	for _, route := range routes {
		card := routeCardView{
			ID:             route.ID,
			Name:           route.Name,
			Description:    route.Description,
			Slug:           route.Slug,
			PublicURL:      strings.TrimRight(publicBaseURL, "/") + "/r/" + route.Slug,
			AlgorithmID:    route.Algorithm,
			AlgorithmLabel: routing.DefinitionByID(route.Algorithm).Label,
			Enabled:        route.Enabled,
			OwnerUsername:  route.OwnerUsername,
			InstanceNames:  []string{},
			Targets:        make([]routeCardTargetView, 0, len(route.Targets)),
		}
		seenInstances := map[string]struct{}{}
		for _, target := range route.Targets {
			if target.Instance.Name != "" {
				if _, ok := seenInstances[target.Instance.Name]; !ok {
					card.InstanceNames = append(card.InstanceNames, target.Instance.Name)
					seenInstances[target.Instance.Name] = struct{}{}
				}
			}
			card.Targets = append(card.Targets, routeCardTargetView{
				SurveyID: target.SurveyID,
				Weight:   max(target.Weight, 1),
			})
		}
		cards = append(cards, card)
	}
	return cards
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(started))
	})
}
