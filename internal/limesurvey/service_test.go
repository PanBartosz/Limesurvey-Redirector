package limesurvey

import (
	"strings"
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
	service := NewService(time.Second, time.Second, protector)
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
	service := NewService(time.Second, time.Second, protector)
	_, err = service.resolvePassword(models.Instance{Name: "LS6"})
	if err == nil {
		t.Fatal("expected missing stored credentials to fail")
	}
}
