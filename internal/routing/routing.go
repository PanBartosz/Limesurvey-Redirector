package routing

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"

	"limesurvey_redirector/internal/models"
)

type AlgorithmDefinition struct {
	ID                  string
	Label               string
	Description         string
	UseCase             string
	NeedsFuzzyThreshold bool
	UsesTargetWeights   bool
}

type Candidate struct {
	Target              models.RouteTarget
	CompletedResponses  int
	IncompleteResponses int
	FullResponses       int
	PendingAssignments  int
	SurveyActive        bool
	FetchError          string
}

type ScoredCandidate struct {
	TargetID  int64   `json:"target_id"`
	SurveyID  int64   `json:"survey_id"`
	Eligible  bool    `json:"eligible"`
	Score     float64 `json:"score"`
	Reason    string  `json:"reason"`
	Completed int     `json:"completed"`
	Full      int     `json:"full"`
	Pending   int     `json:"pending"`
	Weight    int     `json:"weight"`
}

type Result struct {
	Definition  AlgorithmDefinition `json:"definition"`
	Chosen      *Candidate          `json:"-"`
	ChosenScore float64             `json:"chosen_score"`
	Snapshot    []ScoredCandidate   `json:"snapshot"`
}

func Definitions() []AlgorithmDefinition {
	return []AlgorithmDefinition{
		{ID: "random", Label: "Random", Description: "Pick any eligible target at random.", UseCase: "Quick smoke tests or intentionally uniform blind rotation."},
		{ID: "least_completed", Label: "Least Completed", Description: "Choose the target with the lowest number of completed responses.", UseCase: "Direct replacement for the old balancing behavior."},
		{ID: "least_full", Label: "Least Full", Description: "Choose the target with the lowest number of full responses.", UseCase: "Useful only if full responses are the metric you trust operationally."},
		{ID: "completed_fuzzy", Label: "Completed Fuzzy", Description: "Find the lowest completed count and then randomly choose within the fuzzy threshold.", UseCase: "Best default for cloned surveys that should stay roughly balanced without hard pinning.", NeedsFuzzyThreshold: true},
		{ID: "full_fuzzy", Label: "Full Fuzzy", Description: "Find the lowest full response count and then randomly choose within the fuzzy threshold.", UseCase: "Alternative fuzzy balancing when full responses matter more than completed responses.", NeedsFuzzyThreshold: true},
		{ID: "weighted_completed", Label: "Weighted Completed", Description: "Normalize completed counts by target weight.", UseCase: "When some survey clones should receive proportionally more respondents than others.", UsesTargetWeights: true},
		{ID: "weighted_fuzzy", Label: "Weighted Fuzzy", Description: "Weighted balancing plus random choice inside the fuzzy threshold.", UseCase: "Weighted distribution with less refresh-driven clumping.", NeedsFuzzyThreshold: true, UsesTargetWeights: true},
	}
}

func DefinitionByID(id string) AlgorithmDefinition {
	for _, definition := range Definitions() {
		if definition.ID == id {
			return definition
		}
	}
	return AlgorithmDefinition{ID: id, Label: id, Description: "Custom algorithm", UseCase: "No description yet."}
}

func Select(route models.Route, candidates []Candidate) (Result, error) {
	eligible := []Candidate{}
	snapshot := []ScoredCandidate{}

	for _, candidate := range candidates {
		score, isEligible, reason := scoreCandidate(route, candidate)
		snapshot = append(snapshot, ScoredCandidate{
			TargetID:  candidate.Target.ID,
			SurveyID:  candidate.Target.SurveyID,
			Eligible:  isEligible,
			Score:     score,
			Reason:    reason,
			Completed: candidate.CompletedResponses,
			Full:      candidate.FullResponses,
			Pending:   candidate.PendingAssignments,
			Weight:    maxInt(candidate.Target.Weight, 1),
		})
		if isEligible {
			eligible = append(eligible, candidate)
		}
	}

	sort.Slice(snapshot, func(i, j int) bool {
		if snapshot[i].Eligible != snapshot[j].Eligible {
			return snapshot[i].Eligible
		}
		if snapshot[i].Score == snapshot[j].Score {
			return snapshot[i].SurveyID < snapshot[j].SurveyID
		}
		return snapshot[i].Score < snapshot[j].Score
	})

	if len(eligible) == 0 {
		return Result{Definition: DefinitionByID(route.Algorithm), Snapshot: snapshot}, errors.New("no eligible targets")
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	chosen, score, err := choose(route, eligible, rng)
	if err != nil {
		return Result{Definition: DefinitionByID(route.Algorithm), Snapshot: snapshot}, err
	}

	return Result{
		Definition:  DefinitionByID(route.Algorithm),
		Chosen:      &chosen,
		ChosenScore: score,
		Snapshot:    snapshot,
	}, nil
}

func choose(route models.Route, candidates []Candidate, rng *rand.Rand) (Candidate, float64, error) {
	if len(candidates) == 0 {
		return Candidate{}, 0, errors.New("no candidates")
	}

	type pair struct {
		candidate Candidate
		score     float64
	}
	pairs := make([]pair, 0, len(candidates))
	for _, candidate := range candidates {
		score, _, _ := scoreCandidate(route, candidate)
		pairs = append(pairs, pair{candidate: candidate, score: score})
	}

	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].score == pairs[j].score {
			return pairs[i].candidate.Target.SurveyID < pairs[j].candidate.Target.SurveyID
		}
		return pairs[i].score < pairs[j].score
	})

	switch route.Algorithm {
	case "random":
		pick := pairs[rng.Intn(len(pairs))]
		return pick.candidate, pick.score, nil
	case "completed_fuzzy", "full_fuzzy", "weighted_fuzzy":
		minScore := pairs[0].score
		threshold := float64(maxInt(route.FuzzyThreshold, 0))
		window := []pair{}
		for _, pair := range pairs {
			if pair.score <= minScore+threshold {
				window = append(window, pair)
			}
		}
		pick := window[rng.Intn(len(window))]
		return pick.candidate, pick.score, nil
	default:
		pick := pairs[0]
		return pick.candidate, pick.score, nil
	}
}

func scoreCandidate(route models.Route, candidate Candidate) (float64, bool, string) {
	if !candidate.Target.Enabled {
		return math.Inf(1), false, "target disabled"
	}
	if !candidate.Target.Instance.Enabled {
		return math.Inf(1), false, "instance disabled"
	}
	if candidate.FetchError != "" {
		return math.Inf(1), false, fmt.Sprintf("stats fetch failed: %s", candidate.FetchError)
	}
	if !candidate.SurveyActive {
		return math.Inf(1), false, "survey inactive"
	}
	if candidate.Target.HardCap != nil && candidate.CompletedResponses >= *candidate.Target.HardCap {
		return math.Inf(1), false, "hard cap reached"
	}

	effectiveCompleted := float64(candidate.CompletedResponses)
	if route.PendingEnabled {
		effectiveCompleted += float64(candidate.PendingAssignments) * route.PendingWeight
	}
	weight := float64(maxInt(candidate.Target.Weight, 1))

	switch route.Algorithm {
	case "random":
		return 0, true, "eligible"
	case "least_full", "full_fuzzy":
		return float64(candidate.FullResponses), true, "eligible"
	case "weighted_completed", "weighted_fuzzy":
		return effectiveCompleted / weight, true, "eligible"
	case "least_completed", "completed_fuzzy", "":
		fallthrough
	default:
		return effectiveCompleted, true, "eligible"
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
