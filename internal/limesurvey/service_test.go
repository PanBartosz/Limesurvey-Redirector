package limesurvey

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"limesurvey_redirector/internal/credentials"
	"limesurvey_redirector/internal/models"
	"limesurvey_redirector/internal/routing"
)

func TestSnapshotJSONRedactsSensitiveInstanceFields(t *testing.T) {
	payload := SnapshotJSON([]routing.Candidate{{
		Target: models.RouteTarget{
			ID:       11,
			SurveyID: 222,
			Instance: models.Instance{
				ID:               5,
				Name:             "LS6",
				RemoteControlURL: "http://internal.example/admin/remotecontrol",
				Username:         "api-user",
			},
		},
		CompletedResponses:  7,
		IncompleteResponses: 1,
		FullResponses:       6,
		SurveyActive:        true,
	}})

	for _, forbidden := range []string{"encrypted_password", "api-user", "remotecontrol"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("snapshot leaked sensitive field %q: %s", forbidden, payload)
		}
	}
	for _, expected := range []string{"\"instance_name\":\"LS6\"", "\"target_id\":11", "\"survey_id\":222"} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("snapshot missing expected field %q: %s", expected, payload)
		}
	}
}

func TestResolvePasswordDecryptsStoredCredentials(t *testing.T) {
	protector, err := credentials.NewProtector("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewProtector failed: %v", err)
	}
	encrypted, err := protector.Encrypt("rpc-password")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	service := NewService(time.Second, time.Minute, time.Second, protector)
	password, err := service.resolvePassword(models.Instance{Name: "LS6", EncryptedPassword: encrypted})
	if err != nil {
		t.Fatalf("resolvePassword failed: %v", err)
	}
	if password != "rpc-password" {
		t.Fatalf("unexpected password %q", password)
	}
}

func TestResolvePasswordRejectsMissingStoredCredentials(t *testing.T) {
	protector, err := credentials.NewProtector("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewProtector failed: %v", err)
	}
	service := NewService(time.Second, time.Minute, time.Second, protector)
	_, err = service.resolvePassword(models.Instance{Name: "LS6"})
	if err == nil {
		t.Fatal("expected missing stored credentials to fail")
	}
}

func TestBuildCandidatesBoundsSlowJSONRPCByOneDeadline(t *testing.T) {
	var slow atomic.Bool
	slow.Store(true)
	server := newJSONRPCStateServer(t, &slow)
	instance, protector := testJSONRPCInstance(t, server.URL)
	service := NewService(10*time.Millisecond, time.Minute, 75*time.Millisecond, protector)
	route := models.Route{
		Targets: []models.RouteTarget{
			{ID: 1, SurveyID: 101, Enabled: true, Instance: instance},
			{ID: 2, SurveyID: 102, Enabled: true, Instance: instance},
			{ID: 3, SurveyID: 103, Enabled: true, Instance: instance},
		},
	}

	start := time.Now()
	candidates, err := service.BuildCandidates(context.Background(), route)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("BuildCandidates failed: %v", err)
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("expected one bounded routing deadline, took %s", elapsed)
	}
	for _, candidate := range candidates {
		if candidate.FetchError == "" {
			t.Fatalf("expected timeout error for candidate %+v", candidate)
		}
	}
}

func TestGetSurveyStateUsesRecentStaleCacheAfterTimeout(t *testing.T) {
	var slow atomic.Bool
	server := newJSONRPCStateServer(t, &slow)
	instance, protector := testJSONRPCInstance(t, server.URL)
	service := NewService(5*time.Millisecond, time.Minute, 75*time.Millisecond, protector)

	state, err := service.GetSurveyState(context.Background(), instance, 101)
	if err != nil {
		t.Fatalf("initial GetSurveyState failed: %v", err)
	}
	if state.Summary.CompletedResponses != 7 || !state.Active {
		t.Fatalf("unexpected initial state: %+v", state)
	}

	time.Sleep(10 * time.Millisecond)
	slow.Store(true)
	state, err = service.GetSurveyState(context.Background(), instance, 101)
	if err != nil {
		t.Fatalf("expected stale fallback, got error: %v", err)
	}
	if !state.Stale || state.FetchWarning == "" {
		t.Fatalf("expected stale state with warning, got %+v", state)
	}
	if state.Summary.CompletedResponses != 7 || !state.Active {
		t.Fatalf("stale state lost cached values: %+v", state)
	}
}

func newJSONRPCStateServer(t *testing.T, slow *atomic.Bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slow.Load() {
			timer := time.NewTimer(500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
			case <-timer.C:
			}
			return
		}
		var request jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var result any
		switch request.Method {
		case "get_session_key":
			result = "session-key"
		case "get_summary":
			result = map[string]any{
				"CompletedResponses":  7,
				"IncompleteResponses": 2,
				"FullResponses":       5,
			}
		case "get_survey_properties":
			result = map[string]any{"active": "Y"}
		case "release_session_key":
			result = true
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"result":  result,
			"id":      request.ID,
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func testJSONRPCInstance(t *testing.T, endpoint string) (models.Instance, *credentials.Protector) {
	t.Helper()
	protector, err := credentials.NewProtector("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewProtector failed: %v", err)
	}
	encrypted, err := protector.Encrypt("rpc-password")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	return models.Instance{
		ID:                1,
		Name:              "LS6",
		RemoteControlURL:  endpoint,
		RPCTransport:      models.RPCTransportJSON,
		Username:          "api-user",
		EncryptedPassword: encrypted,
		Enabled:           true,
	}, protector
}
