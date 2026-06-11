package chaos

import (
	"context"
	"time"

	"github.com/mockwave/mockwave/domain"
)

type ScenarioRunner struct {
	ctrl    *ScenarioController
	sleepFn func(time.Duration)
	hook    func(*ActiveRun)
}

func NewScenarioRunner(ctrl *ScenarioController, sleepFn func(time.Duration), hook func(*ActiveRun)) *ScenarioRunner {
	return &ScenarioRunner{ctrl: ctrl, sleepFn: sleepFn, hook: hook}
}

// Run executes the scenario synchronously (callers launch it in a goroutine).
// It clears the controller when the run ends for any reason.
func (r *ScenarioRunner) Run(ctx context.Context, sc domain.Scenario) {
	defer r.ctrl.Clear()
	ruleSet := make(map[string]bool, len(sc.RuleIDs))
	for _, id := range sc.RuleIDs {
		ruleSet[id] = true
	}
	for i, phase := range sc.Phases {
		if ctx.Err() != nil {
			return
		}
		run := &ActiveRun{
			ScenarioID: sc.ID, ScenarioName: sc.Name, RuleIDs: ruleSet,
			PhaseIndex: i, PhaseCount: len(sc.Phases), PhaseProfileID: phase.FaultProfileID,
		}
		r.ctrl.SetActive(run)
		if r.hook != nil {
			r.hook(run)
		}
		r.sleepFn(time.Duration(phase.DurationSec) * time.Second)
	}
}
