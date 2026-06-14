package routing

import (
	"testing"

	"limesurvey_redirector/internal/models"
)

func TestLeastCompletedChoosesLowestCompletedCandidate(t *testing.T) {
	route := models.Route{Algorithm: "least_completed"}
	result, err := Select(route, []Candidate{
		{Target: models.RouteTarget{ID: 1, SurveyID: 100, Enabled: true, Instance: models.Instance{Enabled: true}}, CompletedResponses: 10, SurveyActive: true},
		{Target: models.RouteTarget{ID: 2, SurveyID: 101, Enabled: true, Instance: models.Instance{Enabled: true}}, CompletedResponses: 3, SurveyActive: true},
	})
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}
	if result.Chosen == nil || result.Chosen.Target.ID != 2 {
		t.Fatalf("expected target 2, got %+v", result.Chosen)
	}
}

func TestWeightedCompletedUsesWeight(t *testing.T) {
	route := models.Route{Algorithm: "weighted_completed"}
	result, err := Select(route, []Candidate{
		{Target: models.RouteTarget{ID: 1, SurveyID: 100, Weight: 1, Enabled: true, Instance: models.Instance{Enabled: true}}, CompletedResponses: 4, SurveyActive: true},
		{Target: models.RouteTarget{ID: 2, SurveyID: 101, Weight: 4, Enabled: true, Instance: models.Instance{Enabled: true}}, CompletedResponses: 8, SurveyActive: true},
	})
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}
	if result.Chosen == nil || result.Chosen.Target.ID != 2 {
		t.Fatalf("expected weighted target 2, got %+v", result.Chosen)
	}
}

func TestDisabledOrInactiveCandidatesAreExcluded(t *testing.T) {
	route := models.Route{Algorithm: "least_completed"}
	_, err := Select(route, []Candidate{
		{Target: models.RouteTarget{ID: 1, SurveyID: 100, Enabled: false, Instance: models.Instance{Enabled: true}}, CompletedResponses: 0, SurveyActive: true},
		{Target: models.RouteTarget{ID: 2, SurveyID: 101, Enabled: true, Instance: models.Instance{Enabled: true}}, CompletedResponses: 0, SurveyActive: false},
	})
	if err == nil {
		t.Fatal("expected no eligible targets error")
	}
}

func TestRecentStaleStatsRemainEligible(t *testing.T) {
	route := models.Route{Algorithm: "least_completed"}
	result, err := Select(route, []Candidate{{
		Target: models.RouteTarget{
			ID:       1,
			SurveyID: 101,
			Enabled:  true,
			Instance: models.Instance{Enabled: true},
		},
		CompletedResponses: 7,
		SurveyActive:       true,
		StatsStale:         true,
		StatsWarning:       "using cached stats after refresh failed",
	}})
	if err != nil {
		t.Fatalf("expected stale candidate to remain eligible: %v", err)
	}
	if result.Chosen == nil || result.Chosen.Target.SurveyID != 101 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.Snapshot) != 1 || !result.Snapshot[0].Stale {
		t.Fatalf("expected stale marker in snapshot: %+v", result.Snapshot)
	}
}
