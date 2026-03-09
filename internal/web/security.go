package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"limesurvey_redirector/internal/models"
	"limesurvey_redirector/internal/routing"
)

const (
	csrfCookieName = "lsr_csrf"
	csrfFieldName  = "_csrf"
	formMaxBytes   = 1 << 20
)

var (
	usernamePattern   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{1,62}[a-z0-9])?$`)
	slugPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	queryKeyPattern   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)
	allowedSchemes    = map[string]struct{}{"http": {}, "https": {}}
	allowedTransports = map[models.RPCTransport]struct{}{
		models.RPCTransportJSON: {},
		models.RPCTransportXML:  {},
	}
	allowedForwardModes = map[models.QueryForwardMode]struct{}{
		models.QueryForwardAll:  {},
		models.QueryForwardNone: {},
	}
	allowedStickinessModes = map[models.StickinessMode]struct{}{
		models.StickinessOff:      {},
		models.StickinessCookie:   {},
		models.StickinessQueryKey: {},
	}
)

type csrfProtector struct {
	secret        []byte
	secureCookies bool
}

type simulationResponse struct {
	Route      simulationRoute       `json:"route"`
	Candidates []simulationCandidate `json:"candidates"`
	Result     routing.Result        `json:"result"`
	Error      string                `json:"error,omitempty"`
}

type simulationRoute struct {
	ID                int64                   `json:"id"`
	Slug              string                  `json:"slug"`
	Name              string                  `json:"name"`
	Description       string                  `json:"description"`
	Algorithm         string                  `json:"algorithm"`
	FuzzyThreshold    int                     `json:"fuzzy_threshold"`
	PendingEnabled    bool                    `json:"pending_enabled"`
	PendingTTLSeconds int                     `json:"pending_ttl_seconds"`
	PendingWeight     float64                 `json:"pending_weight"`
	ForwardQueryMode  models.QueryForwardMode `json:"forward_query_mode"`
	FallbackURL       string                  `json:"fallback_url"`
	Enabled           bool                    `json:"enabled"`
	StickinessEnabled bool                    `json:"stickiness_enabled"`
	StickinessMode    models.StickinessMode   `json:"stickiness_mode"`
	StickinessParam   string                  `json:"stickiness_param"`
	Targets           []simulationTarget      `json:"targets"`
}

type simulationTarget struct {
	ID          int64                  `json:"id"`
	SurveyID    int64                  `json:"survey_id"`
	DisplayName string                 `json:"display_name"`
	Weight      int                    `json:"weight"`
	HardCap     *int                   `json:"hard_cap,omitempty"`
	Priority    int                    `json:"priority"`
	Enabled     bool                   `json:"enabled"`
	Instance    models.InstanceSummary `json:"instance"`
}

type simulationCandidate struct {
	TargetID            int64                  `json:"target_id"`
	SurveyID            int64                  `json:"survey_id"`
	Instance            models.InstanceSummary `json:"instance"`
	CompletedResponses  int                    `json:"completed_responses"`
	IncompleteResponses int                    `json:"incomplete_responses"`
	FullResponses       int                    `json:"full_responses"`
	PendingAssignments  int                    `json:"pending_assignments"`
	SurveyActive        bool                   `json:"survey_active"`
	FetchError          string                 `json:"fetch_error,omitempty"`
}

func newCSRFProtector(secret string, secureCookies bool) *csrfProtector {
	return &csrfProtector{secret: []byte(secret), secureCookies: secureCookies}
}

func (p *csrfProtector) ensureToken(w http.ResponseWriter, r *http.Request) string {
	if token, ok := p.tokenFromCookie(r); ok {
		return token
	}
	token := randomToken()
	p.setCookie(w, token)
	return token
}

func (p *csrfProtector) validate(r *http.Request) bool {
	cookieToken, ok := p.tokenFromCookie(r)
	if !ok {
		return false
	}
	submitted := strings.TrimSpace(r.FormValue(csrfFieldName))
	if submitted == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookieToken), []byte(submitted)) == 1
}

func (p *csrfProtector) tokenFromCookie(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return "", false
	}
	token, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	if !hmac.Equal(sig, p.sign(token)) {
		return "", false
	}
	return string(token), true
}

func (p *csrfProtector) setCookie(w http.ResponseWriter, token string) {
	tokenBytes := []byte(token)
	value := base64.RawURLEncoding.EncodeToString(tokenBytes) + "." + base64.RawURLEncoding.EncodeToString(p.sign(tokenBytes))
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   p.secureCookies,
		MaxAge:   86400,
	})
}

func (p *csrfProtector) sign(token []byte) []byte {
	h := hmac.New(sha256.New, p.secret)
	_, _ = h.Write(token)
	return h.Sum(nil)
}

func randomToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/admin") || strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func parsePostForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, formMaxBytes)
	return r.ParseForm()
}

func writeInternalServerError(w http.ResponseWriter, err error) {
	log.Printf("internal server error: %v", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func redactQueryString(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "[invalid-query]"
	}
	for key, items := range values {
		for i := range items {
			items[i] = "REDACTED"
		}
		values[key] = items
	}
	return values.Encode()
}

func validateUsername(username string) error {
	if !usernamePattern.MatchString(strings.ToLower(strings.TrimSpace(username))) {
		return fmt.Errorf("username must use 3-64 lowercase letters, numbers, dots, underscores, or dashes")
	}
	return nil
}

func validateUserPassword(password string) error {
	if len(password) < 12 {
		return fmt.Errorf("password must be at least 12 characters")
	}
	return nil
}

func validateInstancePassword(password string) error {
	if password == "" {
		return fmt.Errorf("instance API password is required")
	}
	if len(password) > 512 {
		return fmt.Errorf("instance API password must be at most 512 characters")
	}
	return nil
}

func validateSlug(slug string) error {
	slug = strings.TrimSpace(slug)
	if len(slug) < 3 || len(slug) > 64 || !slugPattern.MatchString(slug) {
		return fmt.Errorf("slug must use lowercase letters, numbers, and dashes only")
	}
	return nil
}

func validateQueryParamKey(param string) error {
	if !queryKeyPattern.MatchString(strings.TrimSpace(param)) {
		return fmt.Errorf("query parameter name is invalid")
	}
	return nil
}

func validateAbsoluteHTTPURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid URL")
	}
	if parsed == nil || !parsed.IsAbs() || parsed.Host == "" {
		return nil, fmt.Errorf("URL must be absolute")
	}
	if _, ok := allowedSchemes[strings.ToLower(parsed.Scheme)]; !ok {
		return nil, fmt.Errorf("URL must use http or https")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("URL must not include credentials or fragments")
	}
	return parsed, nil
}

func allowedFallbackHosts(instances []models.Instance, publicBaseURL string) map[string]struct{} {
	hosts := map[string]struct{}{}
	for _, instance := range instances {
		parsed, err := url.Parse(strings.TrimSpace(instance.SurveyBaseURL))
		if err == nil && parsed.Host != "" {
			hosts[strings.ToLower(parsed.Host)] = struct{}{}
		}
	}
	if parsed, err := url.Parse(strings.TrimSpace(publicBaseURL)); err == nil && parsed.Host != "" {
		hosts[strings.ToLower(parsed.Host)] = struct{}{}
	}
	return hosts
}

func validateFallbackURL(raw string, hosts map[string]struct{}) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	parsed, err := validateAbsoluteHTTPURL(trimmed)
	if err != nil {
		return "", err
	}
	if _, ok := hosts[strings.ToLower(parsed.Host)]; !ok {
		return "", fmt.Errorf("fallback URL host is not allowed")
	}
	return parsed.String(), nil
}

func scrubInstancesForUser(instances []models.Instance) []models.Instance {
	out := make([]models.Instance, 0, len(instances))
	for _, instance := range instances {
		out = append(out, models.Instance{
			ID:           instance.ID,
			Name:         instance.Name,
			RPCTransport: instance.RPCTransport,
			Enabled:      instance.Enabled,
		})
	}
	return out
}

func buildSimulationResponse(route models.Route, candidates []routing.Candidate, result routing.Result, runErr error) simulationResponse {
	payload := simulationResponse{
		Route: simulationRoute{
			ID:                route.ID,
			Slug:              route.Slug,
			Name:              route.Name,
			Description:       route.Description,
			Algorithm:         route.Algorithm,
			FuzzyThreshold:    route.FuzzyThreshold,
			PendingEnabled:    route.PendingEnabled,
			PendingTTLSeconds: route.PendingTTLSeconds,
			PendingWeight:     route.PendingWeight,
			ForwardQueryMode:  route.ForwardQueryMode,
			FallbackURL:       route.FallbackURL,
			Enabled:           route.Enabled,
			StickinessEnabled: route.StickinessEnabled,
			StickinessMode:    route.StickinessMode,
			StickinessParam:   route.StickinessParam,
			Targets:           make([]simulationTarget, 0, len(route.Targets)),
		},
		Candidates: make([]simulationCandidate, 0, len(candidates)),
		Result:     result,
	}
	for _, target := range route.Targets {
		payload.Route.Targets = append(payload.Route.Targets, simulationTarget{
			ID:          target.ID,
			SurveyID:    target.SurveyID,
			DisplayName: target.DisplayName,
			Weight:      target.Weight,
			HardCap:     target.HardCap,
			Priority:    target.Priority,
			Enabled:     target.Enabled,
			Instance:    toInstanceSummary(target.Instance),
		})
	}
	for _, candidate := range candidates {
		payload.Candidates = append(payload.Candidates, simulationCandidate{
			TargetID:            candidate.Target.ID,
			SurveyID:            candidate.Target.SurveyID,
			Instance:            toInstanceSummary(candidate.Target.Instance),
			CompletedResponses:  candidate.CompletedResponses,
			IncompleteResponses: candidate.IncompleteResponses,
			FullResponses:       candidate.FullResponses,
			PendingAssignments:  candidate.PendingAssignments,
			SurveyActive:        candidate.SurveyActive,
			FetchError:          candidate.FetchError,
		})
	}
	if runErr != nil {
		payload.Error = runErr.Error()
	}
	return payload
}

func toInstanceSummary(instance models.Instance) models.InstanceSummary {
	return models.InstanceSummary{
		ID:           instance.ID,
		Name:         instance.Name,
		RPCTransport: string(instance.RPCTransport),
	}
}

func routeVisibleToPrincipal(isAdmin bool, username, role string, route models.Route) bool {
	if isAdmin {
		return true
	}
	return strings.EqualFold(route.OwnerUsername, strings.TrimSpace(username)) && route.OwnerRole == strings.TrimSpace(role)
}
