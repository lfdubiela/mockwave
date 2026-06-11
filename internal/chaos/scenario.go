package chaos

import "sync/atomic"

// ActiveRun is an immutable snapshot of a scenario's current phase. The runner
// replaces the whole pointer on each phase change; readers never mutate it.
type ActiveRun struct {
	ScenarioID     string
	ScenarioName   string
	RuleIDs        map[string]bool
	PhaseIndex     int
	PhaseCount     int
	PhaseProfileID string // "" = recovery phase (no faults)
	PhaseEndsUnix  int64
}

type ScenarioController struct {
	active atomic.Pointer[ActiveRun]
}

func NewScenarioController() *ScenarioController { return &ScenarioController{} }

func (c *ScenarioController) Overlay(ruleID string) (profileID string, overridden bool) {
	run := c.active.Load()
	if run == nil || !run.RuleIDs[ruleID] {
		return "", false
	}
	return run.PhaseProfileID, true
}

func (c *ScenarioController) Active() *ActiveRun     { return c.active.Load() }
func (c *ScenarioController) SetActive(r *ActiveRun) { c.active.Store(r) }
func (c *ScenarioController) Clear()                 { c.active.Store(nil) }
