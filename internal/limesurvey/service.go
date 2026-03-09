package limesurvey

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	xmlrpc "alexejk.io/go-xmlrpc"

	"limesurvey_redirector/internal/models"
	"limesurvey_redirector/internal/routing"
)

type Service struct {
	statsTTL       time.Duration
	requestTimeout time.Duration
	mu             sync.Mutex
	cache          map[string]cachedState
}

type Summary struct {
	CompletedResponses  int
	IncompleteResponses int
	FullResponses       int
}

type SurveyState struct {
	Summary Summary
	Active  bool
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

func NewService(statsTTL, requestTimeout time.Duration) *Service {
	return &Service{
		statsTTL:       statsTTL,
		requestTimeout: requestTimeout,
		cache:          map[string]cachedState{},
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
	candidates := make([]routing.Candidate, 0, len(route.Targets))
	for _, target := range route.Targets {
		state, err := s.GetSurveyState(ctx, target.Instance, target.SurveyID)
		candidate := routing.Candidate{Target: target}
		if err != nil {
			candidate.FetchError = err.Error()
			candidates = append(candidates, candidate)
			continue
		}
		candidate.CompletedResponses = state.Summary.CompletedResponses
		candidate.IncompleteResponses = state.Summary.IncompleteResponses
		candidate.FullResponses = state.Summary.FullResponses
		candidate.SurveyActive = state.Active
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func (s *Service) GetSurveyState(ctx context.Context, instance models.Instance, surveyID int64) (SurveyState, error) {
	key := fmt.Sprintf("%d:%d", instance.ID, surveyID)
	s.mu.Lock()
	cached, ok := s.cache[key]
	s.mu.Unlock()
	if ok && time.Since(cached.fetched) < s.statsTTL {
		return cached.state, nil
	}

	client, err := s.newClient(instance)
	if err != nil {
		return SurveyState{}, err
	}
	state, err := client.GetSurveyState(ctx, surveyID)
	if err != nil {
		return SurveyState{}, err
	}

	s.mu.Lock()
	s.cache[key] = cachedState{state: state, fetched: time.Now()}
	s.mu.Unlock()
	return state, nil
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
	password := os.Getenv(instance.SecretRef)
	if password == "" {
		return nil, fmt.Errorf("missing environment secret for %s", instance.SecretRef)
	}

	switch instance.RPCTransport {
	case models.RPCTransportXML:
		return &xmlClient{
			remoteControlURL: instance.RemoteControlURL,
			username:         instance.Username,
			password:         password,
		}, nil
	case models.RPCTransportJSON:
		return &jsonClient{
			remoteControlURL: instance.RemoteControlURL,
			username:         instance.Username,
			password:         password,
			httpTimeout:      s.requestTimeout,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported transport %q", instance.RPCTransport)
	}
}

type xmlClient struct {
	remoteControlURL string
	username         string
	password         string
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
}

func (c *xmlClient) ListSurveys(ctx context.Context) ([]SurveyOverview, error) {
	client, err := xmlrpc.NewClient(c.remoteControlURL)
	if err != nil {
		return nil, err
	}
	sessionKey, err := c.getSessionKey(client)
	if err != nil {
		return nil, err
	}
	defer c.releaseSessionKey(client, sessionKey)

	var raw any
	if err := client.Call("list_surveys", []any{sessionKey}, &raw); err != nil {
		return nil, err
	}
	return parseSurveyList(raw), nil
}

func (c *xmlClient) GetSurveyState(ctx context.Context, surveyID int64) (SurveyState, error) {
	client, err := xmlrpc.NewClient(c.remoteControlURL)
	if err != nil {
		return SurveyState{}, err
	}
	sessionKey, err := c.getSessionKey(client)
	if err != nil {
		return SurveyState{}, err
	}
	defer c.releaseSessionKey(client, sessionKey)

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

func (c *xmlClient) releaseSessionKey(client *xmlrpc.Client, sessionKey string) {
	var ignored any
	_ = client.Call("release_session_key", []any{sessionKey}, &ignored)
}

func (c *jsonClient) ListSurveys(ctx context.Context) ([]SurveyOverview, error) {
	sessionKey, err := c.getSessionKey(ctx)
	if err != nil {
		return nil, err
	}
	defer c.releaseSessionKey(ctx, sessionKey)

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
	defer c.releaseSessionKey(ctx, sessionKey)

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
