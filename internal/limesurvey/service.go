package limesurvey

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	xmlrpc "alexejk.io/go-xmlrpc"

	"limesurvey_redirector/internal/credentials"
	"limesurvey_redirector/internal/models"
	"limesurvey_redirector/internal/routing"
)

type Service struct {
	statsTTL         time.Duration
	staleStatsMaxAge time.Duration
	requestTimeout   time.Duration
	instanceSecrets  *credentials.Protector
	mu               sync.Mutex
	cache            map[string]cachedState
	inflight         map[string]*stateLoad
}

type Summary struct {
	CompletedResponses  int
	IncompleteResponses int
	FullResponses       int
}

type SurveyState struct {
	Summary      Summary
	Active       bool
	Stale        bool
	FetchWarning string
}

type SurveyOverview struct {
	SurveyID int64  `json:"survey_id"`
	Title    string `json:"title"`
	Active   bool   `json:"active"`
}

type cachedState struct {
	state   SurveyState
	fetched time.Time
}

type stateLoad struct {
	state SurveyState
	err   error
	done  chan struct{}
}

func NewService(statsTTL, staleStatsMaxAge, requestTimeout time.Duration, instanceSecrets *credentials.Protector) *Service {
	return &Service{
		statsTTL:         statsTTL,
		staleStatsMaxAge: staleStatsMaxAge,
		requestTimeout:   requestTimeout,
		instanceSecrets:  instanceSecrets,
		cache:            map[string]cachedState{},
		inflight:         map[string]*stateLoad{},
	}
}

func (s *Service) ListSurveys(ctx context.Context, instance models.Instance) ([]SurveyOverview, error) {
	client, err := s.newClient(instance)
	if err != nil {
		return nil, err
	}
	return client.ListSurveys(ctx)
}

func (s *Service) BuildCandidates(ctx context.Context, route models.Route) ([]routing.Candidate, error) {
	ctx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	candidates := make([]routing.Candidate, len(route.Targets))
	var wg sync.WaitGroup
	for i, target := range route.Targets {
		wg.Add(1)
		go func(index int, target models.RouteTarget) {
			defer wg.Done()
			state, err := s.GetSurveyState(ctx, target.Instance, target.SurveyID)
			candidate := routing.Candidate{Target: target}
			if err != nil {
				candidate.FetchError = err.Error()
				candidates[index] = candidate
				return
			}
			candidate.CompletedResponses = state.Summary.CompletedResponses
			candidate.IncompleteResponses = state.Summary.IncompleteResponses
			candidate.FullResponses = state.Summary.FullResponses
			candidate.SurveyActive = state.Active
			candidate.StatsStale = state.Stale
			candidate.StatsWarning = state.FetchWarning
			candidates[index] = candidate
		}(i, target)
	}
	wg.Wait()
	return candidates, nil
}

func (s *Service) GetSurveyState(ctx context.Context, instance models.Instance, surveyID int64) (SurveyState, error) {
	key := fmt.Sprintf("%d:%d", instance.ID, surveyID)
	now := time.Now()

	s.mu.Lock()
	cached, cachedOK := s.cache[key]
	if cachedOK && now.Sub(cached.fetched) < s.statsTTL {
		s.mu.Unlock()
		return cached.state, nil
	}
	load, loading := s.inflight[key]
	if !loading {
		load = &stateLoad{done: make(chan struct{})}
		s.inflight[key] = load
		go s.loadSurveyState(key, instance, surveyID, load)
	}
	s.mu.Unlock()

	select {
	case <-load.done:
		if load.err == nil {
			return load.state, nil
		}
		return s.staleStateOrError(cached, cachedOK, load.err)
	case <-ctx.Done():
		return s.staleStateOrError(cached, cachedOK, ctx.Err())
	}
}

func (s *Service) loadSurveyState(key string, instance models.Instance, surveyID int64, load *stateLoad) {
	ctx, cancel := context.WithTimeout(context.Background(), s.requestTimeout)
	defer cancel()

	client, err := s.newClient(instance)
	var state SurveyState
	if err == nil {
		state, err = client.GetSurveyState(ctx, surveyID)
	}

	s.mu.Lock()
	if err == nil {
		state.Stale = false
		state.FetchWarning = ""
		s.cache[key] = cachedState{state: state, fetched: time.Now()}
	}
	load.state = state
	load.err = err
	delete(s.inflight, key)
	close(load.done)
	s.mu.Unlock()
}

func (s *Service) staleStateOrError(cached cachedState, ok bool, fetchErr error) (SurveyState, error) {
	if ok && s.staleStatsMaxAge > 0 && time.Since(cached.fetched) <= s.staleStatsMaxAge {
		state := cached.state
		state.Stale = true
		state.FetchWarning = fmt.Sprintf("using cached stats after refresh failed: %v", fetchErr)
		return state, nil
	}
	return SurveyState{}, fetchErr
}

func SnapshotJSON(candidates []routing.Candidate) string {
	type snapshotCandidate struct {
		TargetID            int64  `json:"target_id"`
		SurveyID            int64  `json:"survey_id"`
		InstanceID          int64  `json:"instance_id"`
		InstanceName        string `json:"instance_name"`
		CompletedResponses  int    `json:"completed_responses"`
		IncompleteResponses int    `json:"incomplete_responses"`
		FullResponses       int    `json:"full_responses"`
		PendingAssignments  int    `json:"pending_assignments"`
		SurveyActive        bool   `json:"survey_active"`
		FetchError          string `json:"fetch_error,omitempty"`
		StatsStale          bool   `json:"stats_stale,omitempty"`
		StatsWarning        string `json:"stats_warning,omitempty"`
	}

	snapshot := make([]snapshotCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		snapshot = append(snapshot, snapshotCandidate{
			TargetID:            candidate.Target.ID,
			SurveyID:            candidate.Target.SurveyID,
			InstanceID:          candidate.Target.Instance.ID,
			InstanceName:        candidate.Target.Instance.Name,
			CompletedResponses:  candidate.CompletedResponses,
			IncompleteResponses: candidate.IncompleteResponses,
			FullResponses:       candidate.FullResponses,
			PendingAssignments:  candidate.PendingAssignments,
			SurveyActive:        candidate.SurveyActive,
			FetchError:          candidate.FetchError,
			StatsStale:          candidate.StatsStale,
			StatsWarning:        candidate.StatsWarning,
		})
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		return "[]"
	}
	return string(payload)
}

type client interface {
	GetSurveyState(ctx context.Context, surveyID int64) (SurveyState, error)
	ListSurveys(ctx context.Context) ([]SurveyOverview, error)
}

func (s *Service) newClient(instance models.Instance) (client, error) {
	password, err := s.resolvePassword(instance)
	if err != nil {
		return nil, err
	}

	switch instance.RPCTransport {
	case models.RPCTransportXML:
		return &xmlClient{
			remoteControlURL: instance.RemoteControlURL,
			username:         instance.Username,
			password:         password,
			requestTimeout:   s.requestTimeout,
		}, nil
	case models.RPCTransportJSON:
		return &jsonClient{
			remoteControlURL: instance.RemoteControlURL,
			username:         instance.Username,
			password:         password,
			httpTimeout:      s.requestTimeout,
			httpClient:       &http.Client{Timeout: s.requestTimeout},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported transport %q", instance.RPCTransport)
	}
}

func (s *Service) resolvePassword(instance models.Instance) (string, error) {
	if strings.TrimSpace(instance.EncryptedPassword) != "" {
		password, err := s.instanceSecrets.Decrypt(instance.EncryptedPassword)
		if err != nil {
			return "", fmt.Errorf("decrypt stored credentials for instance %q: %w", instance.Name, err)
		}
		if password == "" {
			return "", fmt.Errorf("stored credentials for instance %q are empty", instance.Name)
		}
		return password, nil
	}
	return "", fmt.Errorf("instance %q has no stored credentials", instance.Name)
}

type xmlClient struct {
	remoteControlURL string
	username         string
	password         string
	requestTimeout   time.Duration
}

type sessionResponse struct {
	SKey string `xmlrpc:"result"`
}

type xmlSummaryResponse struct {
	Stats map[string]any `xmlrpc:"result"`
}

type xmlPropertiesResponse struct {
	Stats map[string]any `xmlrpc:"result"`
}

type jsonClient struct {
	remoteControlURL string
	username         string
	password         string
	httpTimeout      time.Duration
	httpClient       *http.Client
}

func (c *xmlClient) ListSurveys(ctx context.Context) ([]SurveyOverview, error) {
	client, err := c.newRPCClient(ctx)
	if err != nil {
		return nil, err
	}
	sessionKey, err := c.getSessionKey(client)
	if err != nil {
		return nil, err
	}
	defer c.releaseSessionKey(sessionKey)

	var raw any
	if err := client.Call("list_surveys", []any{sessionKey}, &raw); err != nil {
		return nil, err
	}
	return parseSurveyList(raw), nil
}

func (c *xmlClient) GetSurveyState(ctx context.Context, surveyID int64) (SurveyState, error) {
	client, err := c.newRPCClient(ctx)
	if err != nil {
		return SurveyState{}, err
	}
	sessionKey, err := c.getSessionKey(client)
	if err != nil {
		return SurveyState{}, err
	}
	defer c.releaseSessionKey(sessionKey)

	var summary any
	if err := client.Call("get_summary", []any{sessionKey, int(surveyID), "all"}, &summary); err != nil {
		return SurveyState{}, err
	}
	var props any
	if err := client.Call("get_survey_properties", []any{sessionKey, int(surveyID), []string{"active"}}, &props); err != nil {
		return SurveyState{}, err
	}

	return SurveyState{
		Summary: parseSummary(summary),
		Active:  parseActive(props),
	}, nil
}

func (c *xmlClient) getSessionKey(client *xmlrpc.Client) (string, error) {
	var response any
	if err := client.Call("get_session_key", []any{c.username, c.password}, &response); err != nil {
		return "", err
	}
	if key := parseSessionKey(response); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("empty XML-RPC session key")
}

func (c *xmlClient) releaseSessionKey(sessionKey string) {
	go func() {
		cleanupTimeout := minDuration(c.requestTimeout, 2*time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		client, err := c.newRPCClient(ctx)
		if err != nil {
			return
		}
		var ignored any
		_ = client.Call("release_session_key", []any{sessionKey}, &ignored)
	}()
}

func (c *xmlClient) newRPCClient(ctx context.Context) (*xmlrpc.Client, error) {
	httpClient := &http.Client{
		Timeout: c.requestTimeout,
		Transport: requestContextTransport{
			ctx:  ctx,
			base: http.DefaultTransport,
		},
	}
	return xmlrpc.NewClient(c.remoteControlURL, xmlrpc.HttpClient(httpClient))
}

type requestContextTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t requestContextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(req.Clone(t.ctx))
}

func (c *jsonClient) ListSurveys(ctx context.Context) ([]SurveyOverview, error) {
	sessionKey, err := c.getSessionKey(ctx)
	if err != nil {
		return nil, err
	}
	defer c.releaseSessionKey(sessionKey)

	var response any
	if err := c.call(ctx, "list_surveys", []any{sessionKey}, &response); err != nil {
		return nil, err
	}
	return parseSurveyList(response), nil
}

func (c *jsonClient) GetSurveyState(ctx context.Context, surveyID int64) (SurveyState, error) {
	sessionKey, err := c.getSessionKey(ctx)
	if err != nil {
		return SurveyState{}, err
	}
	defer c.releaseSessionKey(sessionKey)

	var summary any
	if err := c.call(ctx, "get_summary", []any{sessionKey, surveyID, "all"}, &summary); err != nil {
		return SurveyState{}, err
	}
	var props any
	if err := c.call(ctx, "get_survey_properties", []any{sessionKey, surveyID, []string{"active"}}, &props); err != nil {
		return SurveyState{}, err
	}

	return SurveyState{
		Summary: parseSummary(summary),
		Active:  parseActive(props),
	}, nil
}

func parseSessionKey(raw any) string {
	switch typed := raw.(type) {
	case string:
		return typed
	case map[string]any:
		return stringify(typed["result"])
	default:
		return ""
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func parseSurveyList(raw any) []SurveyOverview {
	if wrapped, ok := raw.(map[string]any); ok {
		if result, ok := wrapped["result"]; ok {
			raw = result
		}
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	list := make([]SurveyOverview, 0, len(items))
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		list = append(list, SurveyOverview{
			SurveyID: int64(extractInt(entry, "sid", "surveyid", "survey_id")),
			Title:    extractString(entry, "surveyls_title", "title", "name"),
			Active:   strings.EqualFold(extractString(entry, "active"), "Y"),
		})
	}
	return list
}

func parseSummary(raw any) Summary {
	if wrapped, ok := raw.(map[string]any); ok {
		if result, ok := wrapped["result"]; ok {
			raw = result
		}
	}
	entry, ok := raw.(map[string]any)
	if !ok {
		return Summary{}
	}
	if nested, ok := entry["Stats"].(map[string]any); ok {
		entry = nested
	} else if nested, ok := entry["stats"].(map[string]any); ok {
		entry = nested
	}
	return Summary{
		CompletedResponses:  extractInt(entry, "CompletedResponses", "completed_responses", "completedresponses"),
		IncompleteResponses: extractInt(entry, "IncompleteResponses", "incomplete_responses", "incompleteresponses"),
		FullResponses:       extractInt(entry, "FullResponses", "full_responses", "fullresponses"),
	}
}

func parseActive(raw any) bool {
	if wrapped, ok := raw.(map[string]any); ok {
		if result, ok := wrapped["result"]; ok {
			raw = result
		}
	}
	entry, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	value := extractString(entry, "active", "Active")
	return strings.EqualFold(value, "Y") || strings.EqualFold(value, "true")
}

func extractString(entry map[string]any, keys ...string) string {
	for _, key := range keys {
		for existingKey, value := range entry {
			if normalizeKey(existingKey) == normalizeKey(key) {
				return stringify(value)
			}
		}
	}
	return ""
}

func extractInt(entry map[string]any, keys ...string) int {
	for _, key := range keys {
		for existingKey, value := range entry {
			if normalizeKey(existingKey) != normalizeKey(key) {
				continue
			}
			switch typed := value.(type) {
			case float64:
				return int(typed)
			case int:
				return typed
			case int64:
				return int(typed)
			case string:
				parsed, err := strconv.Atoi(strings.TrimSpace(typed))
				if err == nil {
					return parsed
				}
			}
		}
	}
	return 0
}

func normalizeKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "")
	return value
}

func stringify(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return ""
	}
}
