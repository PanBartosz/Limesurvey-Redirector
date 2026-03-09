package web

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"limesurvey_redirector/internal/auth"
	"limesurvey_redirector/internal/config"
	"limesurvey_redirector/internal/credentials"
	"limesurvey_redirector/internal/models"
	"limesurvey_redirector/internal/routing"
	"limesurvey_redirector/internal/store"
)

type stubSurveyService struct {
	candidates []routing.Candidate
	err        error
}

func (s *stubSurveyService) BuildCandidates(ctx context.Context, route models.Route) ([]routing.Candidate, error) {
	return s.candidates, s.err
}

type testEnv struct {
	cfg       config.Config
	store     *store.Store
	server    *Server
	ts        *httptest.Server
	stub      *stubSurveyService
	protector *credentials.Protector
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.Config{
		AdminUsername:          "admin",
		AdminPassword:          "AdminPass123!",
		SessionSecret:          "01234567890123456789012345678901",
		InstanceCredentialsKey: "abcdefghijklmnopqrstuvwxyz012345",
		PublicBaseURL:          "http://127.0.0.1:18099",
		RequestTimeout:         time.Second,
		StatsTTL:               time.Second,
	}
	protector, err := credentials.NewProtector(cfg.InstanceCredentialsKey)
	if err != nil {
		t.Fatalf("NewProtector failed: %v", err)
	}
	tmpl, err := template.ParseFS(assets, "templates/*.html")
	if err != nil {
		t.Fatalf("ParseFS failed: %v", err)
	}
	stub := &stubSurveyService{}
	srv := &Server{
		cfg:             cfg,
		store:           st,
		auth:            auth.New(cfg.AdminUsername, cfg.AdminPassword, cfg.SessionSecret, false),
		csrf:            newCSRFProtector(cfg.SessionSecret, false),
		lsService:       stub,
		instanceSecrets: protector,
		tmpl:            tmpl,
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testEnv{cfg: cfg, store: st, server: srv, ts: ts, stub: stub, protector: protector}
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New failed: %v", err)
	}
	return &http.Client{Jar: jar}
}

func seedInstance(t *testing.T, env *testEnv, name string) int64 {
	t.Helper()
	encryptedPassword, err := env.protector.Encrypt("mock-password")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	id, err := env.store.CreateInstance(context.Background(), store.CreateInstanceInput{
		Name:              name,
		SurveyBaseURL:     "http://survey.example.test/surveys",
		RemoteControlURL:  "http://mock-ls:19080/jsonrpc",
		RPCTransport:      models.RPCTransportJSON,
		Username:          "api-user",
		EncryptedPassword: encryptedPassword,
		Enabled:           true,
	})
	if err != nil {
		t.Fatalf("CreateInstance failed: %v", err)
	}
	return id
}

func seedUser(t *testing.T, env *testEnv, username string) {
	t.Helper()
	hash, err := auth.HashPassword("UserPassword123!")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if _, err := env.store.CreateUser(context.Background(), store.CreateUserInput{Username: username, PasswordHash: hash, Role: string(auth.RoleUser), Enabled: true}); err != nil {
		t.Fatalf("CreateUser failed: %v", err)
	}
}

func userID(t *testing.T, env *testEnv, username string) int64 {
	t.Helper()
	authUser, err := env.store.GetAuthUserByUsername(context.Background(), username)
	if err != nil {
		t.Fatalf("GetAuthUserByUsername failed: %v", err)
	}
	return authUser.User.ID
}

func seedRoute(t *testing.T, env *testEnv, owner string, instanceID int64, slug string) models.Route {
	t.Helper()
	id, err := env.store.CreateRoute(context.Background(), store.CreateRouteInput{
		Slug:             slug,
		Name:             strings.Title(strings.ReplaceAll(slug, "-", " ")),
		Description:      "test route",
		OwnerUsername:    owner,
		OwnerRole:        string(auth.RoleUser),
		Algorithm:        "least_completed",
		ForwardQueryMode: models.QueryForwardAll,
		Enabled:          true,
		InstanceID:       instanceID,
		SurveyIDs:        []int64{111, 222},
	})
	if err != nil {
		t.Fatalf("CreateRoute failed: %v", err)
	}
	route, err := env.store.GetRoute(context.Background(), id)
	if err != nil {
		t.Fatalf("GetRoute failed: %v", err)
	}
	return route
}

func fetchCSRF(t *testing.T, client *http.Client, pageURL string) string {
	t.Helper()
	resp, err := client.Get(pageURL)
	if err != nil {
		t.Fatalf("GET %s failed: %v", pageURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	re := regexp.MustCompile(`name="_csrf" value="([^"]+)"`)
	match := re.FindStringSubmatch(string(body))
	if len(match) != 2 {
		t.Fatalf("csrf token not found in response body: %s", string(body))
	}
	return match[1]
}

func login(t *testing.T, client *http.Client, baseURL, username, password string) {
	t.Helper()
	csrf := fetchCSRF(t, client, baseURL+"/admin/login")
	form := url.Values{}
	form.Set("_csrf", csrf)
	form.Set("username", username)
	form.Set("password", password)
	resp, err := client.PostForm(baseURL+"/admin/login", form)
	if err != nil {
		t.Fatalf("login POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != "/admin" {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected login to land on /admin, got %s body=%s", resp.Request.URL.Path, string(body))
	}
}

func TestSecurityHeadersOnLoginPage(t *testing.T) {
	env := newTestEnv(t)
	resp, err := http.Get(env.ts.URL + "/admin/login")
	if err != nil {
		t.Fatalf("GET login failed: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("unexpected X-Frame-Options: %q", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("unexpected X-Content-Type-Options: %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("unexpected Cache-Control: %q", got)
	}
}

func TestLoginRequiresCSRF(t *testing.T) {
	env := newTestEnv(t)
	form := url.Values{}
	form.Set("username", "admin")
	form.Set("password", env.cfg.AdminPassword)
	resp, err := http.PostForm(env.ts.URL+"/admin/login", form)
	if err != nil {
		t.Fatalf("POST login failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestAuthenticatedUserCanAccessTutorialPage(t *testing.T) {
	env := newTestEnv(t)
	seedUser(t, env, "alice")
	client := newClient(t)
	login(t, client, env.ts.URL, "alice", "UserPassword123!")

	resp, err := client.Get(env.ts.URL + "/admin/tutorial")
	if err != nil {
		t.Fatalf("GET tutorial failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	payload := string(body)
	if !strings.Contains(payload, "Polski") || !strings.Contains(payload, "English") || !strings.Contains(payload, "Open Routes") {
		t.Fatalf("unexpected tutorial page body: %s", payload)
	}
}

func TestUserCannotAccessAdminEndpointsOrOtherUsersRoutes(t *testing.T) {
	env := newTestEnv(t)
	instanceID := seedInstance(t, env, "Shared LS")
	seedUser(t, env, "alice")
	seedUser(t, env, "bob")
	route := seedRoute(t, env, "alice", instanceID, "alice-route")

	client := newClient(t)
	login(t, client, env.ts.URL, "bob", "UserPassword123!")

	for _, path := range []string{"/admin/instances", "/admin/users", fmt.Sprintf("/admin/routes/%d", route.ID)} {
		resp, err := client.Get(env.ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s failed: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 403 for %s, got %d body=%s", path, resp.StatusCode, string(body))
		}
	}

	resp, err := client.Get(env.ts.URL + "/admin/routes")
	if err != nil {
		t.Fatalf("GET routes failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "alice-route") {
		t.Fatalf("bob should not see alice's route list: %s", string(body))
	}
}

func TestRouteCreationRejectsExternalFallback(t *testing.T) {
	env := newTestEnv(t)
	instanceID := seedInstance(t, env, "Shared LS")
	seedUser(t, env, "alice")
	client := newClient(t)
	login(t, client, env.ts.URL, "alice", "UserPassword123!")

	csrf := fetchCSRF(t, client, env.ts.URL+"/admin/routes")
	form := url.Values{}
	form.Set("_csrf", csrf)
	form.Set("name", "External Fallback")
	form.Set("slug", "external-fallback")
	form.Set("description", "bad fallback")
	form.Set("instance_id", fmt.Sprintf("%d", instanceID))
	form.Set("survey_ids", "111\n222")
	form.Set("algorithm", "least_completed")
	form.Set("forward_query_mode", "all")
	form.Set("pending_ttl_seconds", "1800")
	form.Set("pending_weight", "1")
	form.Set("fuzzy_threshold", "0")
	form.Set("fallback_url", "https://evil.example/phish")
	form.Set("enabled", "on")
	resp, err := client.PostForm(env.ts.URL+"/admin/routes", form)
	if err != nil {
		t.Fatalf("POST routes failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "fallback URL host is not allowed") {
		t.Fatalf("unexpected response body: %s", string(body))
	}
}

func TestRouteCreationAcceptsStructuredTargetsAndWeights(t *testing.T) {
	env := newTestEnv(t)
	instanceID := seedInstance(t, env, "Shared LS")
	seedUser(t, env, "alice")
	client := newClient(t)
	login(t, client, env.ts.URL, "alice", "UserPassword123!")

	csrf := fetchCSRF(t, client, env.ts.URL+"/admin/routes")
	form := url.Values{}
	form.Set("_csrf", csrf)
	form.Set("action", "create")
	form.Set("name", "Weighted Route")
	form.Set("slug", "weighted-route")
	form.Set("description", "weighted create path")
	form.Set("instance_id", fmt.Sprintf("%d", instanceID))
	form.Add("target_survey_id", "111")
	form.Add("target_survey_id", "222")
	form.Add("target_weight", "1")
	form.Add("target_weight", "3")
	form.Set("algorithm", "weighted_completed")
	form.Set("forward_query_mode", "all")
	form.Set("pending_ttl_seconds", "1800")
	form.Set("pending_weight", "1")
	form.Set("enabled", "on")
	resp, err := client.PostForm(env.ts.URL+"/admin/routes", form)
	if err != nil {
		t.Fatalf("POST routes failed: %v", err)
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Request.URL.Path, "/admin/routes/") {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected redirect to route detail, got %s body=%s", resp.Request.URL.Path, string(body))
	}

	route, err := env.store.GetRouteBySlug(context.Background(), "weighted-route")
	if err != nil {
		t.Fatalf("GetRouteBySlug failed: %v", err)
	}
	if len(route.Targets) != 2 || route.Targets[0].Weight != 1 || route.Targets[1].Weight != 3 {
		t.Fatalf("unexpected route targets: %+v", route.Targets)
	}
}

func TestSimulationResponseRedactsSensitiveFields(t *testing.T) {
	env := newTestEnv(t)
	instanceID := seedInstance(t, env, "Shared LS")
	seedUser(t, env, "alice")
	route := seedRoute(t, env, "alice", instanceID, "alice-route")
	target := route.Targets[0]
	target.Instance = models.Instance{
		ID:               99,
		Name:             "Sensitive LS",
		RPCTransport:     models.RPCTransportJSON,
		RemoteControlURL: "http://internal.example/admin/remotecontrol",
		Username:         "api-user",
		Enabled:          true,
	}
	env.stub.candidates = []routing.Candidate{{
		Target:              target,
		CompletedResponses:  1,
		IncompleteResponses: 2,
		FullResponses:       3,
		SurveyActive:        true,
	}}

	client := newClient(t)
	login(t, client, env.ts.URL, "alice", "UserPassword123!")
	resp, err := client.Get(fmt.Sprintf("%s/api/routes/%d/simulate", env.ts.URL, route.ID))
	if err != nil {
		t.Fatalf("GET simulate failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	payload := string(body)
	for _, forbidden := range []string{"encrypted_password", "api-user", "remotecontrol"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("simulation leaked %q: %s", forbidden, payload)
		}
	}
	if !strings.Contains(payload, `"instance":{"id":99,"name":"Sensitive LS","rpc_transport":"jsonrpc"}`) {
		t.Fatalf("simulation payload missing redacted instance summary: %s", payload)
	}
}

func TestRedirectDecisionRedactsQueriesAndCandidateSnapshot(t *testing.T) {
	env := newTestEnv(t)
	instanceID := seedInstance(t, env, "Shared LS")
	seedUser(t, env, "alice")
	route := seedRoute(t, env, "alice", instanceID, "public-route")
	target := route.Targets[0]
	target.Instance.SurveyBaseURL = env.ts.URL + "/surveys"
	target.Instance.RemoteControlURL = "http://internal.example/admin/remotecontrol"
	target.Instance.Username = "api-user"
	env.stub.candidates = []routing.Candidate{{
		Target:             target,
		CompletedResponses: 1,
		SurveyActive:       true,
	}}

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(env.ts.URL + "/r/public-route?token=abc123&src=mail")
	if err != nil {
		t.Fatalf("GET public redirect failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 303, got %d body=%s", resp.StatusCode, string(body))
	}
	if location := resp.Header.Get("Location"); !strings.Contains(location, "token=abc123") || !strings.Contains(location, "src=mail") {
		t.Fatalf("expected forwarded query params in redirect location, got %s", location)
	}

	decisions, err := env.store.ListRecentDecisions(context.Background(), route.ID, 10)
	if err != nil {
		t.Fatalf("ListRecentDecisions failed: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("expected one decision, got %d", len(decisions))
	}
	decision := decisions[0]
	if strings.Contains(decision.RequestQuery, "abc123") || strings.Contains(decision.ForwardedQuery, "abc123") {
		t.Fatalf("query values were not redacted: %+v", decision)
	}
	for _, forbidden := range []string{"encrypted_password", "api-user", "remotecontrol"} {
		if strings.Contains(decision.CandidateSnapshot, forbidden) {
			t.Fatalf("candidate snapshot leaked %q: %s", forbidden, decision.CandidateSnapshot)
		}
	}
}

func TestDisabledUserIsLoggedOutAndCannotReuseSession(t *testing.T) {
	env := newTestEnv(t)
	seedUser(t, env, "alice")

	client := newClient(t)
	login(t, client, env.ts.URL, "alice", "UserPassword123!")

	if err := env.store.UpdateUserEnabled(context.Background(), userID(t, env, "alice"), false); err != nil {
		t.Fatalf("UpdateUserEnabled failed: %v", err)
	}

	resp, err := client.Get(env.ts.URL + "/admin")
	if err != nil {
		t.Fatalf("GET /admin failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != "/admin/login" {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected disabled user session to land on login, got %s body=%s", resp.Request.URL.Path, string(body))
	}
}

func TestAdminCanDisableResetAndDeleteUsers(t *testing.T) {
	env := newTestEnv(t)
	seedUser(t, env, "carol")

	adminClient := newClient(t)
	login(t, adminClient, env.ts.URL, "admin", env.cfg.AdminPassword)

	post := func(form url.Values) {
		csrf := fetchCSRF(t, adminClient, env.ts.URL+"/admin/users")
		form.Set("_csrf", csrf)
		resp, err := adminClient.PostForm(env.ts.URL+"/admin/users", form)
		if err != nil {
			t.Fatalf("POST /admin/users failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.Request.URL.Path != "/admin/users" {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected to land on /admin/users, got %s body=%s", resp.Request.URL.Path, string(body))
		}
	}

	id := userID(t, env, "carol")

	form := url.Values{}
	form.Set("action", "toggle_enabled")
	form.Set("user_id", fmt.Sprintf("%d", id))
	post(form)
	authUser, err := env.store.GetAuthUserByUsername(context.Background(), "carol")
	if err != nil {
		t.Fatalf("GetAuthUserByUsername failed: %v", err)
	}
	if authUser.User.Enabled {
		t.Fatalf("expected carol to be disabled")
	}

	loginClient := newClient(t)
	csrf := fetchCSRF(t, loginClient, env.ts.URL+"/admin/login")
	loginForm := url.Values{}
	loginForm.Set("_csrf", csrf)
	loginForm.Set("username", "carol")
	loginForm.Set("password", "UserPassword123!")
	resp, err := loginClient.PostForm(env.ts.URL+"/admin/login", loginForm)
	if err != nil {
		t.Fatalf("disabled login POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected disabled login to return 401, got %d body=%s", resp.StatusCode, string(body))
	}

	form = url.Values{}
	form.Set("action", "toggle_enabled")
	form.Set("user_id", fmt.Sprintf("%d", id))
	post(form)

	carolClient := newClient(t)
	login(t, carolClient, env.ts.URL, "carol", "UserPassword123!")

	form = url.Values{}
	form.Set("action", "reset_password")
	form.Set("user_id", fmt.Sprintf("%d", id))
	form.Set("new_password", "NewPassword456!")
	post(form)

	resp, err = carolClient.Get(env.ts.URL + "/admin")
	if err != nil {
		t.Fatalf("GET /admin after password reset failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != "/admin/login" {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected old session after password reset to land on login, got %s body=%s", resp.Request.URL.Path, string(body))
	}

	oldPasswordClient := newClient(t)
	csrf = fetchCSRF(t, oldPasswordClient, env.ts.URL+"/admin/login")
	loginForm = url.Values{}
	loginForm.Set("_csrf", csrf)
	loginForm.Set("username", "carol")
	loginForm.Set("password", "UserPassword123!")
	resp, err = oldPasswordClient.PostForm(env.ts.URL+"/admin/login", loginForm)
	if err != nil {
		t.Fatalf("old-password login POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected old password login to return 401, got %d body=%s", resp.StatusCode, string(body))
	}

	newPasswordClient := newClient(t)
	login(t, newPasswordClient, env.ts.URL, "carol", "NewPassword456!")

	form = url.Values{}
	form.Set("action", "delete")
	form.Set("user_id", fmt.Sprintf("%d", id))
	post(form)
	if _, err := env.store.GetAuthUserByUsername(context.Background(), "carol"); err == nil {
		t.Fatalf("expected deleted user lookup to fail")
	}

	resp, err = newPasswordClient.Get(env.ts.URL + "/admin")
	if err != nil {
		t.Fatalf("GET /admin after delete failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != "/admin/login" {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected deleted user session to land on login, got %s body=%s", resp.Request.URL.Path, string(body))
	}
}

func TestDeletingUserWithOwnedRoutesIsRejected(t *testing.T) {
	env := newTestEnv(t)
	instanceID := seedInstance(t, env, "Shared LS")
	seedUser(t, env, "alice")
	seedRoute(t, env, "alice", instanceID, "alice-route")

	client := newClient(t)
	login(t, client, env.ts.URL, "admin", env.cfg.AdminPassword)

	csrf := fetchCSRF(t, client, env.ts.URL+"/admin/users")
	form := url.Values{}
	form.Set("_csrf", csrf)
	form.Set("action", "delete")
	form.Set("user_id", fmt.Sprintf("%d", userID(t, env, "alice")))
	resp, err := client.PostForm(env.ts.URL+"/admin/users", form)
	if err != nil {
		t.Fatalf("POST delete user failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "still owns routes") {
		t.Fatalf("unexpected response body: %s", string(body))
	}
}

func TestRouteCanBeUpdatedAndDeletedByOwner(t *testing.T) {
	env := newTestEnv(t)
	instanceID := seedInstance(t, env, "Shared LS")
	seedUser(t, env, "alice")
	route := seedRoute(t, env, "alice", instanceID, "alpha-route")

	client := newClient(t)
	login(t, client, env.ts.URL, "alice", "UserPassword123!")

	csrf := fetchCSRF(t, client, fmt.Sprintf("%s/admin/routes/%d", env.ts.URL, route.ID))
	form := url.Values{}
	form.Set("_csrf", csrf)
	form.Set("action", "update")
	form.Set("name", "Alpha Route Updated")
	form.Set("slug", "alpha-route-updated")
	form.Set("description", "updated description")
	form.Set("instance_id", fmt.Sprintf("%d", instanceID))
	form.Set("survey_ids", "333\n444")
	form.Set("algorithm", "completed_fuzzy")
	form.Set("fuzzy_threshold", "7")
	form.Set("pending_enabled", "on")
	form.Set("pending_ttl_seconds", "2400")
	form.Set("pending_weight", "1.5")
	form.Set("forward_query_mode", "all")
	form.Set("fallback_url", "")
	form.Set("stickiness_enabled", "on")
	form.Set("stickiness_mode", "cookie")
	form.Set("stickiness_param", "")
	form.Set("enabled", "on")
	resp, err := client.PostForm(fmt.Sprintf("%s/admin/routes/%d", env.ts.URL, route.ID), form)
	if err != nil {
		t.Fatalf("POST route update failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != fmt.Sprintf("/admin/routes/%d", route.ID) {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected route update to land on detail page, got %s body=%s", resp.Request.URL.Path, string(body))
	}

	updated, err := env.store.GetRoute(context.Background(), route.ID)
	if err != nil {
		t.Fatalf("GetRoute failed: %v", err)
	}
	if updated.Name != "Alpha Route Updated" || updated.Slug != "alpha-route-updated" {
		t.Fatalf("route not updated: %+v", updated)
	}
	if updated.OwnerUsername != "alice" || updated.OwnerRole != string(auth.RoleUser) {
		t.Fatalf("route owner changed unexpectedly: %+v", updated)
	}
	if len(updated.Targets) != 2 || updated.Targets[0].SurveyID != 333 || updated.Targets[1].SurveyID != 444 {
		t.Fatalf("unexpected updated targets: %+v", updated.Targets)
	}

	csrf = fetchCSRF(t, client, fmt.Sprintf("%s/admin/routes/%d", env.ts.URL, route.ID))
	form = url.Values{}
	form.Set("_csrf", csrf)
	form.Set("action", "delete")
	resp, err = client.PostForm(fmt.Sprintf("%s/admin/routes/%d", env.ts.URL, route.ID), form)
	if err != nil {
		t.Fatalf("POST route delete failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.Request.URL.Path != "/admin/routes" {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected route delete to land on routes page, got %s body=%s", resp.Request.URL.Path, string(body))
	}
	if _, err := env.store.GetRoute(context.Background(), route.ID); err == nil {
		t.Fatalf("expected deleted route lookup to fail")
	}
}

func TestRouteSlugMustBeUniqueOnCreateAndUpdate(t *testing.T) {
	env := newTestEnv(t)
	instanceID := seedInstance(t, env, "Shared LS")
	seedUser(t, env, "alice")
	first := seedRoute(t, env, "alice", instanceID, "alpha-route")
	second := seedRoute(t, env, "alice", instanceID, "beta-route")

	client := newClient(t)
	login(t, client, env.ts.URL, "alice", "UserPassword123!")

	csrf := fetchCSRF(t, client, env.ts.URL+"/admin/routes")
	form := url.Values{}
	form.Set("_csrf", csrf)
	form.Set("name", "Duplicate Route")
	form.Set("slug", "alpha-route")
	form.Set("description", "duplicate slug")
	form.Set("instance_id", fmt.Sprintf("%d", instanceID))
	form.Set("survey_ids", "999")
	form.Set("algorithm", "least_completed")
	form.Set("forward_query_mode", "all")
	form.Set("pending_ttl_seconds", "1800")
	form.Set("pending_weight", "1")
	form.Set("fuzzy_threshold", "0")
	form.Set("enabled", "on")
	resp, err := client.PostForm(env.ts.URL+"/admin/routes", form)
	if err != nil {
		t.Fatalf("POST duplicate create failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "route slug already exists") {
		t.Fatalf("unexpected response body: %s", string(body))
	}

	csrf = fetchCSRF(t, client, fmt.Sprintf("%s/admin/routes/%d", env.ts.URL, second.ID))
	form = url.Values{}
	form.Set("_csrf", csrf)
	form.Set("action", "update")
	form.Set("name", second.Name)
	form.Set("slug", first.Slug)
	form.Set("description", second.Description)
	form.Set("instance_id", fmt.Sprintf("%d", instanceID))
	form.Set("survey_ids", "102\n103")
	form.Set("algorithm", "least_completed")
	form.Set("fuzzy_threshold", "0")
	form.Set("pending_ttl_seconds", "1800")
	form.Set("pending_weight", "1")
	form.Set("forward_query_mode", "all")
	form.Set("enabled", "on")
	resp, err = client.PostForm(fmt.Sprintf("%s/admin/routes/%d", env.ts.URL, second.ID), form)
	if err != nil {
		t.Fatalf("POST duplicate update failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, string(body))
	}
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "route slug already exists") {
		t.Fatalf("unexpected response body: %s", string(body))
	}
}
