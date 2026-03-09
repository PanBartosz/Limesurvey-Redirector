package limesurvey

import (
	"strings"
	"testing"

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
				SecretRef:        "LS6_RPC_PASSWORD",
			},
		},
		CompletedResponses:  7,
		IncompleteResponses: 1,
		FullResponses:       6,
		SurveyActive:        true,
	}})

	for _, forbidden := range []string{"LS6_RPC_PASSWORD", "api-user", "remotecontrol"} {
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
