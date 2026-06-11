package chaos_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/chaos"
)

func TestRunner_AdvancesPhasesThenClears(t *testing.T) {
	ctrl := chaos.NewScenarioController()
	var mu sync.Mutex
	var phaseProfiles []string
	hook := func(r *chaos.ActiveRun) {
		mu.Lock()
		phaseProfiles = append(phaseProfiles, r.PhaseProfileID)
		mu.Unlock()
	}
	sc := domain.Scenario{
		ID: "s", Name: "s", RuleIDs: []string{"r1"},
		Phases: []domain.ScenarioPhase{
			{DurationSec: 1, FaultProfileID: "a"},
			{DurationSec: 1, FaultProfileID: "b"},
			{DurationSec: 1, FaultProfileID: ""},
		},
	}
	runner := chaos.NewScenarioRunner(ctrl, func(time.Duration) {}, hook)
	runner.Run(context.Background(), sc)
	mu.Lock()
	defer mu.Unlock()
	if len(phaseProfiles) != 3 || phaseProfiles[0] != "a" || phaseProfiles[1] != "b" || phaseProfiles[2] != "" {
		t.Fatalf("expected phases a,b,recovery; got %v", phaseProfiles)
	}
	if ctrl.Active() != nil {
		t.Fatal("controller should be cleared after the run completes")
	}
}

func TestRunner_ContextCancelAbortsAndClears(t *testing.T) {
	ctrl := chaos.NewScenarioController()
	sc := domain.Scenario{
		ID: "s", Name: "s", RuleIDs: []string{"r1"},
		Phases: []domain.ScenarioPhase{{DurationSec: 100, FaultProfileID: "a"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	sleep := func(d time.Duration) { <-ctx.Done() }
	cancel()
	chaos.NewScenarioRunner(ctrl, sleep, nil).Run(ctx, sc)
	if ctrl.Active() != nil {
		t.Fatal("cancelled run must clear the controller")
	}
}
