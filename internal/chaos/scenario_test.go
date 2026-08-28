package chaos_test

import (
	"testing"

	"github.com/mockwave/mockwave/internal/chaos"
)

func TestScenarioController_NoActiveRun(t *testing.T) {
	c := chaos.NewScenarioController()
	profID, overridden := c.Overlay("r1")
	if overridden {
		t.Fatal("no active run → no overlay")
	}
	_ = profID
	if c.Active() != nil {
		t.Fatal("expected no active scenario")
	}
}

func TestScenarioController_OverlayForTargetedRule(t *testing.T) {
	c := chaos.NewScenarioController()
	c.SetActive(&chaos.ActiveRun{
		ScenarioID: "drill", ScenarioName: "Drill",
		RuleIDs:    map[string]bool{"r1": true},
		PhaseIndex: 0, PhaseProfileID: "mild", PhaseCount: 2,
	})
	profID, overridden := c.Overlay("r1")
	if !overridden || profID != "mild" {
		t.Fatalf("expected overlay mild, got %q overridden=%v", profID, overridden)
	}
	if _, ov := c.Overlay("r2"); ov {
		t.Fatal("untargeted rule must not be overlaid")
	}
}

func TestScenarioController_RecoveryPhaseOverlaysEmpty(t *testing.T) {
	c := chaos.NewScenarioController()
	c.SetActive(&chaos.ActiveRun{RuleIDs: map[string]bool{"r1": true}, PhaseProfileID: ""})
	profID, overridden := c.Overlay("r1")
	if !overridden || profID != "" {
		t.Fatalf("recovery phase overlays empty profile, got %q overridden=%v", profID, overridden)
	}
}

func TestScenarioController_ClearStopsOverlay(t *testing.T) {
	c := chaos.NewScenarioController()
	c.SetActive(&chaos.ActiveRun{RuleIDs: map[string]bool{"r1": true}, PhaseProfileID: "x"})
	c.Clear()
	if _, ov := c.Overlay("r1"); ov {
		t.Fatal("Clear must remove overlay")
	}
}
