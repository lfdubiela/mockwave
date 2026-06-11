# Chaos Faults Phase 5 — Scenarios Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add timed chaos **Scenarios** — an ordered list of phases, each applying a fault profile to a set of target rules for a fixed duration, then advancing (including a recovery phase with no faults).

**Architecture:** A `Scenario` is a persisted entity (`store.ScenarioStore`, optional capability). A single in-process `ScenarioController` (in `internal/chaos`) holds the at-most-one active run as an atomic snapshot: which rule IDs are targeted and which profile ID applies in the current phase (empty = recovery). The `FaultStage` consults the controller first: if the matched rule is targeted by the active scenario, the controller's phase profile *overrides* the bucket's `fault_profile_id` (in-memory overlay — stored rules are never mutated). A runner goroutine advances phases on a timer; start/stop and the kill switch abort it. Admin API/CLI/UI expose CRUD + start/stop + live phase.

**Tech Stack:** Go 1.26, `sync/atomic` snapshot pointer, `time.Timer`-driven runner with context cancellation, testify, cobra, vanilla-JS UI.

**Spec:** `docs/specs/2026-06-11-chaos-faults-design.md` (§Scenarios). Depends on phases 1–2 (shipped). Independent of phases 3–4 but composes with them (a phase profile can use any fault type).

---

### Task 1: Domain — Scenario entity

**Files:**
- Modify: `domain/model.go`
- Test: `domain/model_test.go`

- [ ] **Step 1: Write failing tests** (append to `domain/model_test.go`)

```go
func TestScenarioValidate(t *testing.T) {
	valid := domain.Scenario{
		ID:      "drill",
		Name:    "DB degradation drill",
		RuleIDs: []string{"r1", "r2"},
		Phases: []domain.ScenarioPhase{
			{DurationSec: 300, FaultProfileID: "mild"},
			{DurationSec: 120, FaultProfileID: ""}, // recovery
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid scenario: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*domain.Scenario)
	}{
		{"missing id", func(s *domain.Scenario) { s.ID = "" }},
		{"no rule ids", func(s *domain.Scenario) { s.RuleIDs = nil }},
		{"no phases", func(s *domain.Scenario) { s.Phases = nil }},
		{"phase zero duration", func(s *domain.Scenario) { s.Phases[0].DurationSec = 0 }},
		{"phase negative duration", func(s *domain.Scenario) { s.Phases[0].DurationSec = -5 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := valid
			s.RuleIDs = append([]string(nil), valid.RuleIDs...)
			s.Phases = append([]domain.ScenarioPhase(nil), valid.Phases...)
			tc.mut(&s)
			if err := s.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./domain/ -run TestScenario -v`
Expected: FAIL (undefined: domain.Scenario)

- [ ] **Step 3: Implement** (in `domain/model.go`)

```go
// ScenarioPhase is one timed step of a Scenario. FaultProfileID "" means a
// recovery phase: targeted rules run with no injected faults.
type ScenarioPhase struct {
	DurationSec    int    `json:"duration_sec"`
	FaultProfileID string `json:"fault_profile_id,omitempty"`
}

// Scenario applies a sequence of fault profiles to a set of rules over time.
type Scenario struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	RuleIDs []string        `json:"rule_ids"`
	Phases  []ScenarioPhase `json:"phases"`
}

func (s Scenario) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("scenario id is required")
	}
	if len(s.RuleIDs) == 0 {
		return fmt.Errorf("scenario must target at least one rule")
	}
	if len(s.Phases) == 0 {
		return fmt.Errorf("scenario must have at least one phase")
	}
	for i, p := range s.Phases {
		if p.DurationSec <= 0 {
			return fmt.Errorf("phase[%d] duration_sec must be > 0, got %d", i, p.DurationSec)
		}
	}
	return nil
}
```

Add to `Config`:

```go
	Scenarios []Scenario `json:"scenarios,omitempty"`
```

- [ ] **Step 4: Run tests**

Run: `go test ./domain/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add domain/model.go domain/model_test.go
git commit -m "feat(domain): Scenario entity with timed phases"
```

---

### Task 2: store.ScenarioStore + jsonfile implementation

**Files:**
- Modify: `store/store.go`
- Modify: `internal/adapters/out/jsonfile/store.go`
- Test: `internal/adapters/out/jsonfile/store_test.go`

Optional capability, same pattern as `FaultStore`.

- [ ] **Step 1: Write failing test** (append to jsonfile `store_test.go`, reuse the temp-file helper)

```go
func TestScenarioCRUD(t *testing.T) {
	s, err := jsonfile.NewStore(writeConfig(t, domain.Config{}))
	require.NoError(t, err)
	sc := domain.Scenario{ID: "sc1", Name: "n", RuleIDs: []string{"r1"},
		Phases: []domain.ScenarioPhase{{DurationSec: 10, FaultProfileID: "p"}}}

	require.NoError(t, s.SaveScenario(sc))
	got, err := s.GetScenario("sc1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "n", got.Name)

	list, err := s.ListScenarios()
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// update by same ID
	sc.Name = "n2"
	require.NoError(t, s.SaveScenario(sc))
	list, _ = s.ListScenarios()
	assert.Len(t, list, 1)
	assert.Equal(t, "n2", list[0].Name)

	require.NoError(t, s.DeleteScenario("sc1"))
	got, err = s.GetScenario("sc1")
	require.NoError(t, err)
	assert.Nil(t, got)
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/adapters/out/jsonfile/ -run TestScenarioCRUD -v`
Expected: FAIL

- [ ] **Step 3: Add interface** (append to `store/store.go`)

```go
// ScenarioStore is an optional capability for stores that persist chaos
// scenarios. GetScenario returns (nil, nil) when the scenario does not exist.
type ScenarioStore interface {
	ListScenarios() ([]domain.Scenario, error)
	GetScenario(id string) (*domain.Scenario, error)
	SaveScenario(s domain.Scenario) error
	DeleteScenario(id string) error
}
```

- [ ] **Step 4: Implement in jsonfile** — mirror the existing `*FaultProfile` methods exactly (mutex, `flush()`, upsert-by-ID, filter-on-delete, copy-on-list/get, `(nil,nil)` on missing). Operate on `cfg.Scenarios`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/adapters/out/jsonfile/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add store/store.go internal/adapters/out/jsonfile/
git commit -m "feat(store): optional ScenarioStore capability, jsonfile implementation"
```

---

### Task 3: ScenarioController — active-run snapshot + overlay lookup

**Files:**
- Create: `internal/chaos/scenario.go`
- Test: `internal/chaos/scenario_test.go`

The controller holds an atomic snapshot of the active run. Lookups are lock-free on the hot path (FaultStage). The runner goroutine is in Task 4.

- [ ] **Step 1: Write failing tests**

```go
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
	// targeted rule → overlay with phase profile
	profID, overridden := c.Overlay("r1")
	if !overridden || profID != "mild" {
		t.Fatalf("expected overlay mild, got %q overridden=%v", profID, overridden)
	}
	// untargeted rule → no overlay
	if _, ov := c.Overlay("r2"); ov {
		t.Fatal("untargeted rule must not be overlaid")
	}
}

func TestScenarioController_RecoveryPhaseOverlaysEmpty(t *testing.T) {
	c := chaos.NewScenarioController()
	c.SetActive(&chaos.ActiveRun{
		RuleIDs: map[string]bool{"r1": true}, PhaseProfileID: "", // recovery
	})
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
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/chaos/ -run TestScenarioController -v`
Expected: FAIL

- [ ] **Step 3: Implement** (`internal/chaos/scenario.go`)

```go
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
	PhaseEndsUnix  int64  // wall-clock end of current phase, for status display
}

// ScenarioController holds the at-most-one active scenario run as an atomic
// snapshot. Overlay is lock-free on the request hot path.
type ScenarioController struct {
	active atomic.Pointer[ActiveRun]
}

func NewScenarioController() *ScenarioController { return &ScenarioController{} }

// Overlay reports whether the active scenario targets ruleID, and if so the
// profile ID its current phase applies ("" for recovery). When not overridden,
// the caller keeps the bucket's own fault_profile_id.
func (c *ScenarioController) Overlay(ruleID string) (profileID string, overridden bool) {
	run := c.active.Load()
	if run == nil || !run.RuleIDs[ruleID] {
		return "", false
	}
	return run.PhaseProfileID, true
}

// Active returns the current run snapshot, or nil when idle.
func (c *ScenarioController) Active() *ActiveRun { return c.active.Load() }

// SetActive installs a run snapshot (called by the runner on each phase).
func (c *ScenarioController) SetActive(r *ActiveRun) { c.active.Store(r) }

// Clear removes the active run (stop / abort / completion).
func (c *ScenarioController) Clear() { c.active.Store(nil) }
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/chaos/ -run TestScenarioController -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/chaos/scenario.go internal/chaos/scenario_test.go
git commit -m "feat(chaos): scenario controller with atomic overlay snapshot"
```

---

### Task 4: ScenarioRunner — timed phase advancement

**Files:**
- Create: `internal/chaos/runner.go`
- Test: `internal/chaos/runner_test.go`

The runner drives a scenario through its phases. Tests use a tiny injectable sleep so they don't wait real seconds.

- [ ] **Step 1: Write failing tests**

```go
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
	// record each phase the controller is set to
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
	// sleepFn returns immediately so the test runs fast
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
	// sleepFn that blocks until ctx is cancelled, simulating a long phase
	sleep := func(d time.Duration) { <-ctx.Done() }
	cancel()
	chaos.NewScenarioRunner(ctrl, sleep, nil).Run(ctx, sc)
	if ctrl.Active() != nil {
		t.Fatal("cancelled run must clear the controller")
	}
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/chaos/ -run TestRunner -v`
Expected: FAIL

- [ ] **Step 3: Implement** (`internal/chaos/runner.go`)

```go
package chaos

import (
	"context"
	"time"

	"github.com/mockwave/mockwave/domain"
)

// ScenarioRunner advances a scenario through its phases, updating the
// controller's active snapshot per phase. sleepFn is injectable for tests;
// production passes a context-aware sleep. phaseHook (optional) is called with
// each phase's snapshot before sleeping — used by tests to observe phases.
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
			ScenarioID:     sc.ID,
			ScenarioName:   sc.Name,
			RuleIDs:        ruleSet,
			PhaseIndex:     i,
			PhaseCount:     len(sc.Phases),
			PhaseProfileID: phase.FaultProfileID,
		}
		r.ctrl.SetActive(run)
		if r.hook != nil {
			r.hook(run)
		}
		r.sleepFn(time.Duration(phase.DurationSec) * time.Second)
	}
}
```

Note: the production `sleepFn` (wired in Task 6) must return early on context cancellation so stop is responsive — see Task 6 for the context-aware sleep helper.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/chaos/ -run TestRunner -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/chaos/runner.go internal/chaos/runner_test.go
git commit -m "feat(chaos): scenario runner with timed phase advancement"
```

---

### Task 5: FaultStage — consult scenario overlay

**Files:**
- Modify: `internal/chaos/stage.go`
- Test: `internal/chaos/stage_test.go`

The stage gains an optional `*ScenarioController`. Before resolving the profile, it asks the controller for an overlay keyed by the matched rule ID. When overridden, the phase profile ID replaces `pctx.FaultProfileID` (empty = recovery, so no fault fires).

- [ ] **Step 1: Write failing tests** (append to `internal/chaos/stage_test.go`)

```go
func TestFaultStage_ScenarioOverlayOverridesBucketProfile(t *testing.T) {
	ctrl := chaos.NewScenarioController()
	profiles := map[string]domain.FaultProfile{
		"bucket-prof": {ID: "bucket-prof", Enabled: true, Faults: []domain.Fault{
			{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 500}}}},
		"phase-prof": {ID: "phase-prof", Enabled: true, Faults: []domain.Fault{
			{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}}}},
	}
	stage := chaos.NewFaultStageWithScenario(profiles, chaos.NewKillSwitch(), ctrl)
	ctrl.SetActive(&chaos.ActiveRun{RuleIDs: map[string]bool{"r1": true}, PhaseProfileID: "phase-prof"})

	pctx := &pipeline.PipelineContext{Matched: &domain.Rule{ID: "r1"}, FaultProfileID: "bucket-prof"}
	_ = stage.Execute(context.Background(), pctx)
	if pctx.Response.Status != 503 {
		t.Fatalf("scenario phase profile should win, got %d", pctx.Response.Status)
	}
}

func TestFaultStage_ScenarioRecoveryPhaseSuppressesFaults(t *testing.T) {
	ctrl := chaos.NewScenarioController()
	profiles := map[string]domain.FaultProfile{
		"bucket-prof": {ID: "bucket-prof", Enabled: true, Faults: []domain.Fault{
			{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 500}}}},
	}
	stage := chaos.NewFaultStageWithScenario(profiles, chaos.NewKillSwitch(), ctrl)
	ctrl.SetActive(&chaos.ActiveRun{RuleIDs: map[string]bool{"r1": true}, PhaseProfileID: ""}) // recovery

	pctx := &pipeline.PipelineContext{Matched: &domain.Rule{ID: "r1"}, FaultProfileID: "bucket-prof"}
	_ = stage.Execute(context.Background(), pctx)
	if pctx.FaultShortCircuit {
		t.Fatal("recovery phase must suppress the bucket's own fault")
	}
}

func TestFaultStage_UntargetedRuleKeepsBucketProfile(t *testing.T) {
	ctrl := chaos.NewScenarioController()
	profiles := map[string]domain.FaultProfile{
		"bucket-prof": {ID: "bucket-prof", Enabled: true, Faults: []domain.Fault{
			{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 500}}}},
	}
	stage := chaos.NewFaultStageWithScenario(profiles, chaos.NewKillSwitch(), ctrl)
	ctrl.SetActive(&chaos.ActiveRun{RuleIDs: map[string]bool{"other": true}, PhaseProfileID: "x"})

	pctx := &pipeline.PipelineContext{Matched: &domain.Rule{ID: "r1"}, FaultProfileID: "bucket-prof"}
	_ = stage.Execute(context.Background(), pctx)
	if pctx.Response == nil || pctx.Response.Status != 500 {
		t.Fatal("untargeted rule keeps its own bucket profile")
	}
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/chaos/ -run TestFaultStage_Scenario -v`
Expected: FAIL (undefined: NewFaultStageWithScenario)

- [ ] **Step 3: Implement** — add a controller field (nil-safe) and a new constructor; keep `NewFaultStage` working by delegating.

```go
type FaultStage struct {
	profiles map[string]domain.FaultProfile
	ks       *KillSwitch
	scenario *ScenarioController // optional; nil = no scenario overlay
	mu       sync.Mutex
	rng      *rand.Rand
	retry    *retryCounter // present if phase 4 landed; otherwise omit this field
}

func NewFaultStage(profiles map[string]domain.FaultProfile, ks *KillSwitch) *FaultStage {
	return NewFaultStageWithScenario(profiles, ks, nil)
}

func NewFaultStageWithScenario(profiles map[string]domain.FaultProfile, ks *KillSwitch, sc *ScenarioController) *FaultStage {
	return &FaultStage{
		profiles: profiles,
		ks:       ks,
		scenario: sc,
		rng:      rand.New(rand.NewSource(rand.Int63())),
		// retry: newRetryCounter(time.Now), // include only if phase 4 is present
	}
}
```

At the top of `Execute`, after the kill-switch check, apply the overlay:

```go
	if s.ks.Halted() {
		return nil
	}
	effectiveProfileID := pctx.FaultProfileID
	if s.scenario != nil {
		if phaseProfileID, overridden := s.scenario.Overlay(matchedRuleID(pctx)); overridden {
			effectiveProfileID = phaseProfileID
		}
	}
	if effectiveProfileID == "" {
		return nil
	}
	p, ok := s.profiles[effectiveProfileID]
	if !ok || !p.Enabled {
		return nil
	}
```

Replace the prior `pctx.FaultProfileID`-based guard with the block above. Add the helper:

```go
func matchedRuleID(pctx *pipeline.PipelineContext) string {
	if pctx.Matched == nil {
		return ""
	}
	return pctx.Matched.ID
}
```

Note: the kill switch must abort scenarios — that is enforced at the runner level (Task 6 cancels the run on halt), but the stage's `s.ks.Halted()` early-return also guarantees no faults fire while halted even mid-phase. Keep both.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/chaos/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/chaos/stage.go internal/chaos/stage_test.go
git commit -m "feat(chaos): fault stage consults scenario overlay"
```

---

### Task 6: Server wiring — controller, runner lifecycle, kill-switch abort

**Files:**
- Modify: `server/server.go`
- Test: `server/server_test.go`

- [ ] **Step 1: Add fields + accessors** — `Server` gains `scenario *chaos.ScenarioController` and run-management state:

```go
	scenario      *chaos.ScenarioController
	scenarioCancel context.CancelFunc // cancels the active runner; nil when idle
	scenarioMu     sync.Mutex         // guards scenarioCancel + start/stop
```

In `New`, before `rebuild()`: `s.scenario = chaos.NewScenarioController()`.

Accessor:

```go
// Scenario returns the server's scenario controller.
func (s *Server) Scenario() *chaos.ScenarioController { return s.scenario }
```

- [ ] **Step 2: Build the stage with the controller** — in `rebuild()`, change the FaultStage construction to:

```go
	faultStage := chaos.NewFaultStageWithScenario(profMap, s.killSwitch, s.scenario)
```

- [ ] **Step 3: Add StartScenario / StopScenario** with the context-aware sleep and single-active enforcement:

```go
// StartScenario launches sc in a background runner. Returns an error if another
// scenario is already running.
func (s *Server) StartScenario(sc domain.Scenario) error {
	s.scenarioMu.Lock()
	defer s.scenarioMu.Unlock()
	if s.scenarioCancel != nil {
		return fmt.Errorf("a scenario is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.scenarioCancel = cancel
	sleep := func(d time.Duration) {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
		}
	}
	runner := chaos.NewScenarioRunner(s.scenario, sleep, nil)
	go func() {
		runner.Run(ctx, sc)
		s.scenarioMu.Lock()
		if s.scenarioCancel != nil {
			s.scenarioCancel = nil
		}
		s.scenarioMu.Unlock()
	}()
	return nil
}

// StopScenario aborts the active scenario, if any.
func (s *Server) StopScenario() {
	s.scenarioMu.Lock()
	defer s.scenarioMu.Unlock()
	if s.scenarioCancel != nil {
		s.scenarioCancel()
		s.scenarioCancel = nil
	}
}
```

- [ ] **Step 4: Kill switch aborts scenarios** — the existing chaos halt path must also stop any running scenario. The kill switch lives in `internal/chaos` and is toggled by the admin handler; the cleanest seam is to have the admin halt handler call `server.StopScenario()` too (wired in Task 7). Document here that halt → stop is enforced at the API layer, and the stage's `Halted()` guard suppresses faults regardless.

- [ ] **Step 5: Server test** (`server/server_test.go`) — file-backed store with rule `r1` (100% simulate → 200), bucket fault profile `none`, and a fault profile `boom` (error 503). Start a scenario targeting `r1` with one long phase using `boom`; assert a request to `r1` returns 503 while active; `StopScenario()`; assert request returns 200 again. (Use a 1-phase scenario with a multi-second duration; stop before it elapses.)

- [ ] **Step 6: Run** — `go test ./server/ -v` → PASS.

- [ ] **Step 7: Commit**

```bash
git add server/server.go server/server_test.go
git commit -m "feat(server): scenario controller, runner lifecycle, start/stop"
```

---

### Task 7: Admin API — scenarios CRUD + start/stop + status

**Files:**
- Modify: `internal/adapters/cfg/restapi/server.go`
- Modify: `server/admin.go`
- Modify: `internal/adapters/cfg/restapi/openapi.yaml`
- Test: `internal/adapters/cfg/restapi/server_test.go`

Endpoints:

```
GET    /api/scenarios          → 200 [Scenario] (501 if store lacks ScenarioStore)
POST   /api/scenarios          → 201 (validate; 409 dup; 422 invalid; 422 unknown rule_id or fault_profile_id)
GET    /api/scenarios/{id}     → 200 | 404
PUT    /api/scenarios/{id}     → 200 | 404 | 422
DELETE /api/scenarios/{id}     → 204 | 404
POST   /api/scenarios/{id}/start → 202 | 404 | 409 (already running)
POST   /api/scenarios/{id}/stop  → 204
GET    /api/chaos/status       → extend body to {"halted":bool, "active_scenario": {...}|null}
```

The handler needs server-level hooks for start/stop/active that the restapi package cannot import directly (avoid a cycle). Follow the `WithKillSwitch` precedent: add `MuxOption`s carrying small function/interface values.

- [ ] **Step 1: Define a scenario-control seam** — add to the restapi package:

```go
// ScenarioController is the minimal surface the admin API needs to drive
// scenarios; the server provides an adapter. Kept as an interface to avoid a
// dependency on the server package.
type ScenarioControl interface {
	Start(id string) error          // 404 → ErrScenarioNotFound; 409 → ErrScenarioRunning
	Stop()
	ActiveStatus() any              // JSON-serializable active-run summary, or nil
}

var (
	ErrScenarioNotFound = errors.New("scenario not found")
	ErrScenarioRunning  = errors.New("a scenario is already running")
)
```

Add `MuxOption` `WithScenarioControl(sc ScenarioControl)` storing it on the api struct (mirror `WithKillSwitch`).

- [ ] **Step 2: Write failing tests** — CRUD round trip with a `scenarioMemStore` helper (embed memStore + ScenarioStore methods + FaultStore so rule/profile validation passes); 422 on unknown `rule_id` and unknown `fault_profile_id`; 501 when store lacks ScenarioStore; start returns 202, second start 409, stop 204; `GET /api/chaos/status` includes `active_scenario` when running. Use a fake `ScenarioControl` recording Start/Stop calls and returning a canned `ActiveStatus()`.

- [ ] **Step 3: Implement handlers** — `scenarios`, `scenarioByID`, `scenarioStart`, `scenarioStop`; register routes in `NewMux`. Validation on create/update: each `rule_id` must exist (`GetRules`/scan); each phase `fault_profile_id` (when non-empty) must resolve via `FaultStore` → else 422. After CRUD writes call `a.reload()`. Start maps `ErrScenarioNotFound`→404, `ErrScenarioRunning`→409, else 202. Extend `chaosStatus` to include `active_scenario: a.scenarioControl.ActiveStatus()` when the control is wired.

- [ ] **Step 4: Server adapter + wiring** — in `server/admin.go`, pass `restapi.WithScenarioControl(scenarioControlAdapter{s})` where the adapter implements the interface:

```go
type scenarioControlAdapter struct{ s *Server }

func (a scenarioControlAdapter) Start(id string) error {
	sc, err := a.s.scenarioByID(id) // small helper: load from store, (nil → ErrScenarioNotFound)
	if err != nil { return err }
	if startErr := a.s.StartScenario(*sc); startErr != nil {
		return restapi.ErrScenarioRunning
	}
	return nil
}
func (a scenarioControlAdapter) Stop() { a.s.StopScenario() }
func (a scenarioControlAdapter) ActiveStatus() any {
	run := a.s.scenario.Active()
	if run == nil { return nil }
	return map[string]any{
		"scenario_id": run.ScenarioID, "scenario_name": run.ScenarioName,
		"phase_index": run.PhaseIndex, "phase_count": run.PhaseCount,
		"phase_profile_id": run.PhaseProfileID,
	}
}
```

Add `Server.scenarioByID(id string) (*domain.Scenario, error)` returning `restapi.ErrScenarioNotFound` on miss (load via the store's `ScenarioStore`; if the store lacks the capability, return that error too). Also make the admin **halt** handler call `Stop()` — wire by having `WithKillSwitch` halt also invoke scenario stop, or add the stop call in the `chaosHalt` handler when `scenarioControl != nil`. Choose the handler-level call: in `chaosHalt`, after halting, `if a.scenarioControl != nil { a.scenarioControl.Stop() }`.

- [ ] **Step 5: openapi.yaml** — add the scenario paths, `Scenario`/`ScenarioPhase` schemas, and extend `ChaosStatus` with `active_scenario`.

- [ ] **Step 6: Run** — `go test ./internal/adapters/cfg/restapi/ ./server/ -v` → PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/cfg/restapi/ server/
git commit -m "feat(restapi): scenario CRUD, start/stop, status with active scenario"
```

---

### Task 8: CLI — `mockwave scenario`

**Files:**
- Modify: `cmd/mockwave/chaos.go`
- Modify: `cmd/mockwave/main.go`
- Test: `cmd/mockwave/chaos_test.go`

```
mockwave scenario list
mockwave scenario start <id>
mockwave scenario stop <id>
```

- [ ] **Step 1: Write failing tests** — against `httptest` fakes, mirror the existing `runFault*` tests: `runScenarioList` → GET /api/scenarios; `runScenarioStart` → POST /api/scenarios/<id>/start expecting 202; `runScenarioStop` → POST /api/scenarios/<id>/stop expecting 204; non-2xx → error with status+body.

- [ ] **Step 2: Implement** — add `scenarioCmd()` following the `faultCmd()`/`chaosCmd()` structure (same `--admin-url` flag, `adminDo`/`checkStatus` helpers). `start`/`stop` take an `<id>` arg (`cobra.ExactArgs(1)`).

```go
func scenarioCmd() *cobra.Command {
	var adminURL string
	cmd := &cobra.Command{Use: "scenario", Short: "Manage and run chaos scenarios"}
	cmd.PersistentFlags().StringVar(&adminURL, "admin-url", "http://localhost:9090", "mockwave admin API base URL")

	list := &cobra.Command{Use: "list", Short: "List scenarios", RunE: func(cmd *cobra.Command, _ []string) error {
		return runScenarioList(adminURL, cmd.OutOrStdout())
	}}
	start := &cobra.Command{Use: "start <id>", Args: cobra.ExactArgs(1), Short: "Start a scenario", RunE: func(_ *cobra.Command, args []string) error {
		return runScenarioStart(adminURL, args[0])
	}}
	stop := &cobra.Command{Use: "stop <id>", Args: cobra.ExactArgs(1), Short: "Stop a scenario", RunE: func(_ *cobra.Command, args []string) error {
		return runScenarioStop(adminURL, args[0])
	}}
	cmd.AddCommand(list, start, stop)
	return cmd
}

func runScenarioList(base string, out io.Writer) error {
	resp, err := adminDo(base, http.MethodGet, "/api/scenarios", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return err
	}
	_, err = io.Copy(out, resp.Body)
	return err
}

func runScenarioStart(base, id string) error {
	resp, err := adminDo(base, http.MethodPost, "/api/scenarios/"+id+"/start", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp, http.StatusAccepted)
}

func runScenarioStop(base, id string) error {
	resp, err := adminDo(base, http.MethodPost, "/api/scenarios/"+id+"/stop", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp, http.StatusNoContent)
}
```

Register in `main.go`: add `scenarioCmd()` to the `root.AddCommand(...)` call.

- [ ] **Step 3: Run** — `go test ./cmd/mockwave/ -v` → PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/mockwave/chaos.go cmd/mockwave/main.go cmd/mockwave/chaos_test.go
git commit -m "feat(cli): scenario list/start/stop commands"
```

---

### Task 9: Admin UI — Scenarios section

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html`

- [ ] **Step 1: Scenario CRUD UI** in the Chaos tab (below the fault profiles section): table from `GET /api/scenarios` (id, name, #rules, #phases); editor with id, name, a multi-select / comma list of rule IDs, and dynamic phase rows (duration_sec + fault-profile select populated from `GET /api/faults`, with a "— recovery —" option = empty). Submit POST/PUT. Hide with a notice on 501. Match the fault-profile editor patterns built in phase 2.

- [ ] **Step 2: Start/stop + live phase** — each scenario row gets Start/Stop buttons (POST start/stop). Poll `GET /api/chaos/status` (already polled every 10s from the phase-2 fix) and, when `active_scenario` is present, show a banner: scenario name, phase index/count, current profile. Reuse the existing 10s poll — extend its handler to render the active-scenario banner.

- [ ] **Step 3: Verify** — `node --check` extracted script; `go build -o /tmp/mockwave ./cmd/mockwave`; start, create a scenario via UI, start it, observe the banner, stop it.

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(admin): scenarios UI with live phase banner"
```

---

### Task 10: Import/export + integration + docs

**Files:**
- Modify: `internal/adapters/cfg/restapi/transfer.go`, `transfer_test.go`
- Create: `tests/integration/chaos_scenario_test.go`
- Modify: `README.md`, `docs/extending.md`

- [ ] **Step 1: Import/export** — `Config.Scenarios` already serializes. Extend export to optionally include scenarios (decide policy: export scenarios whose every `rule_id` is in the exported rule set; document it). Import: upsert scenarios on commit, validating that referenced rule_ids and phase fault_profile_ids resolve in payload-or-store (422 otherwise); preview reports scenario ID conflicts in a `scenario_conflicts` array. Mirror the fault-profile transfer tests.

- [ ] **Step 2: Integration test** (`tests/integration/chaos_scenario_test.go`) — full server, rule `r1` (200) + fault profile `boom` (503) + scenario targeting `r1` with a single multi-second `boom` phase. POST `/api/scenarios/{id}/start`; request to `r1` → 503; `GET /api/chaos/status` shows `active_scenario`; POST `.../stop`; request → 200. Use a short-but-not-instant phase and stop explicitly so the test does not depend on phase elapse.

- [ ] **Step 3: Run** — `go test ./tests/integration/ -run Chaos -v` → PASS.

- [ ] **Step 4: Docs** — README "Chaos Testing": Scenarios section (concept, phases, recovery, one active at a time, kill-switch aborts, per-process run state), a scenario JSON example, and CLI usage. `docs/extending.md`: document `store.ScenarioStore` alongside `FaultStore`.

- [ ] **Step 5: Full suite** — `go test ./... && go vet ./...` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/cfg/restapi/transfer.go internal/adapters/cfg/restapi/transfer_test.go tests/integration/chaos_scenario_test.go README.md docs/extending.md
git commit -m "feat(chaos): scenario import/export, integration tests, docs"
```

---

## Out of scope (other plans)
- Phase 3: connection-level faults — `docs/plans/2026-06-11-chaos-faults-phase3-plan.md`.
- Phase 4: retryStorm — `docs/plans/2026-06-11-chaos-faults-phase4-plan.md`.
- Remote-store (dynamo/mongo/cosmos) `ScenarioStore` implementations — follow-up.
- Persisting run state across restarts (runs are in-memory by design).

## Notes / risks
- Single active scenario enforced by `scenarioCancel != nil`. The runner goroutine clears it on completion; `StopScenario` cancels and clears. Guard all access with `scenarioMu`.
- The FaultStage reads the controller via an atomic load — zero added lock contention on the request hot path.
- If phase 4 (retryStorm) is NOT yet merged when this plan runs, omit the `retry` field and its initialization from the FaultStage edits in Task 5 (noted inline).
- Kill switch and scenario stop are independent mechanisms; halting suppresses faults immediately (stage guard) and the admin halt handler also stops the run so it does not silently resume on `resume`.
