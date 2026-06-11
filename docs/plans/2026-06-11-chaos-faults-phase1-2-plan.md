# Chaos Fault Injection — Phases 1–2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the README "Chaos Testing" section for current capabilities, then the core fault engine: `FaultProfile` entity, `FaultStage` pipeline stage with `jitter` and `error` faults, global kill switch, admin API, CLI commands, and admin UI tab.

**Architecture:** `FaultProfile` is a new domain entity persisted through an optional store capability (`store.FaultStore`, same pattern as `store.VersionedStore`). A new pipeline stage between the percentile router and the simulation stage rolls fault probabilities and either annotates the response (jitter) or short-circuits it (error). A process-global atomic kill switch makes all fault evaluation a no-op. Admin API/CLI/UI follow existing patterns in `internal/adapters/cfg/restapi` and `cmd/mockwave`.

**Tech Stack:** Go 1.26, stdlib `net/http`, cobra CLI, vanilla-JS single-file admin UI, testify.

**Spec:** `docs/specs/2026-06-11-chaos-faults-design.md`. Connection-level faults (`hang`, `reset`, `halfResponse`, `slowBody`), `retryStorm`, and Scenarios are **out of scope** here (phases 3–5, separate plans).

---

### Task 1: README "Chaos Testing" section (current capabilities only)

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add the section**

Insert after the traffic-splitting/forwarding documentation (find the section documenting `weight`/`forward` buckets) a new `## Chaos Testing` section:

```markdown
## Chaos Testing

Mockwave can act as an API-level chaos tool: instead of breaking real infrastructure
(Gremlin-style host agents), it injects failures at the boundary your client actually
sees — mock responses and forwarded traffic. Everything below works today:

| Failure mode | How |
|---|---|
| Latency on mock responses | `response.delay_ms` on a simulation |
| Latency on real upstream calls | `delay_ms` on a `forward` bucket (net effect = max(delay, upstream latency)) |
| Partial degradation / blast radius | weighted buckets — e.g. 90% forward to the real service, 10% to a failing simulation |
| Dependency returning errors | a simulation with `status: 503` (or any code/body) on a percentage of traffic |
| Conditional failures | match criteria (header/path/query/body) route only specific requests to a failing rule |
| Dynamic failures | JavaScript `script` on a simulation computes status/body per request |

Example — 20% of `/payments/**` traffic gets a 503, the rest reaches the real service:

```json
{
  "rules": [{
    "id": "payments-chaos",
    "name": "Payments degradation",
    "match": {"protocol": "http", "path": "/payments/**"},
    "buckets": [
      {"weight": 80, "action": "forward", "forward_url": "https://payments.internal"},
      {"weight": 20, "action": "simulate", "simulation_id": "payments-503"}
    ]
  }],
  "simulations": [{
    "id": "payments-503",
    "protocol": "http",
    "response": {"status": 503, "delay_ms": 1500, "body": {"error": "service unavailable"}}
  }]
}
```
```

- [ ] **Step 2: Verify markdown renders** (preview or `grep -n "Chaos Testing" README.md`)

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: chaos testing section covering current capabilities"
```

---

### Task 2: Domain model — FaultProfile, Fault, bucket reference

**Files:**
- Modify: `domain/model.go`
- Test: `domain/model_test.go`

- [ ] **Step 1: Write failing tests** (append to `domain/model_test.go`)

```go
func TestFaultProfileValidate(t *testing.T) {
	valid := domain.FaultProfile{
		ID:      "flaky-db",
		Name:    "Flaky database",
		Enabled: true,
		Faults: []domain.Fault{
			{Type: domain.FaultJitter, Probability: 1.0, Params: domain.FaultParams{BaseDelayMs: 200, JitterMs: 300}},
			{Type: domain.FaultError, Probability: 0.3, Params: domain.FaultParams{StatusCode: 503, Body: `{"error":"unavailable"}`}},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid profile: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*domain.FaultProfile)
	}{
		{"missing id", func(p *domain.FaultProfile) { p.ID = "" }},
		{"no faults", func(p *domain.FaultProfile) { p.Faults = nil }},
		{"unknown type", func(p *domain.FaultProfile) { p.Faults[0].Type = "explode" }},
		{"probability below 0", func(p *domain.FaultProfile) { p.Faults[0].Probability = -0.1 }},
		{"probability above 1", func(p *domain.FaultProfile) { p.Faults[0].Probability = 1.1 }},
		{"jitter without jitter_ms", func(p *domain.FaultProfile) { p.Faults[0].Params.JitterMs = 0; p.Faults[0].Params.BaseDelayMs = 0 }},
		{"error without status", func(p *domain.FaultProfile) { p.Faults[1].Params.StatusCode = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			p.Faults = append([]domain.Fault(nil), valid.Faults...)
			tc.mut(&p)
			if err := p.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestBucketFaultProfileIDRoundTrip(t *testing.T) {
	b := domain.WeightedBucket{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s1", FaultProfileID: "flaky-db"}
	if err := b.Validate(); err != nil {
		t.Fatalf("bucket with fault profile id should be valid: %v", err)
	}
	data, _ := json.Marshal(b)
	if !strings.Contains(string(data), `"fault_profile_id":"flaky-db"`) {
		t.Fatalf("missing fault_profile_id in %s", data)
	}
	// omitempty: bucket without profile stays byte-compatible
	b2 := domain.WeightedBucket{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s1"}
	data2, _ := json.Marshal(b2)
	if strings.Contains(string(data2), "fault_profile_id") {
		t.Fatalf("fault_profile_id should be omitted: %s", data2)
	}
}
```

Add `"encoding/json"` and `"strings"` to the test file imports if absent.

- [ ] **Step 2: Run, verify failure**

Run: `go test ./domain/ -run 'TestFaultProfile|TestBucketFault' -v`
Expected: FAIL (undefined: domain.FaultProfile, etc.)

- [ ] **Step 3: Implement** (append to `domain/model.go`; add `FaultProfileID` to `WeightedBucket`)

```go
// Fault types supported by FaultProfile.
const (
	FaultJitter = "jitter"
	FaultError  = "error"
)

// FaultParams holds type-specific fault parameters. One flat struct keeps the
// JSON shape simple; Validate enforces which fields each type requires.
type FaultParams struct {
	BaseDelayMs int               `json:"baseDelayMs,omitempty"` // jitter: fixed delay added to every affected request
	JitterMs    int               `json:"jitterMs,omitempty"`    // jitter: random extra delay in [0, JitterMs)
	StatusCode  int               `json:"statusCode,omitempty"`  // error: HTTP status to return
	Body        string            `json:"body,omitempty"`        // error: response body (raw string)
	Headers     map[string]string `json:"headers,omitempty"`     // error: response headers
}

// Fault is one failure mode inside a FaultProfile.
type Fault struct {
	Type        string      `json:"type"`
	Probability float64     `json:"probability"` // [0,1], rolled per request
	Params      FaultParams `json:"params,omitempty"`
}

func (f Fault) Validate() error {
	if f.Probability < 0 || f.Probability > 1 {
		return fmt.Errorf("fault probability must be in [0,1], got %v", f.Probability)
	}
	switch f.Type {
	case FaultJitter:
		if f.Params.BaseDelayMs <= 0 && f.Params.JitterMs <= 0 {
			return fmt.Errorf("jitter fault requires baseDelayMs or jitterMs > 0")
		}
	case FaultError:
		if f.Params.StatusCode < 100 || f.Params.StatusCode > 599 {
			return fmt.Errorf("error fault requires statusCode in [100,599], got %d", f.Params.StatusCode)
		}
	default:
		return fmt.Errorf("unknown fault type %q", f.Type)
	}
	return nil
}

// FaultProfile is a named, reusable set of faults attachable to rule buckets.
type FaultProfile struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Enabled     bool    `json:"enabled"`
	Faults      []Fault `json:"faults"`
}

func (p FaultProfile) Validate() error {
	if p.ID == "" {
		return fmt.Errorf("fault profile id is required")
	}
	if len(p.Faults) == 0 {
		return fmt.Errorf("fault profile must have at least one fault")
	}
	for i, f := range p.Faults {
		if err := f.Validate(); err != nil {
			return fmt.Errorf("fault[%d]: %w", i, err)
		}
	}
	return nil
}
```

In `WeightedBucket` add the field:

```go
	FaultProfileID string `json:"fault_profile_id,omitempty"` // optional chaos profile applied to this bucket
```

In `Config` add:

```go
	FaultProfiles []FaultProfile `json:"fault_profiles,omitempty"`
```

- [ ] **Step 4: Run tests**

Run: `go test ./domain/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add domain/model.go domain/model_test.go
git commit -m "feat(domain): FaultProfile entity and bucket fault_profile_id"
```

---

### Task 3: Store capability — `store.FaultStore` + jsonfile implementation

**Files:**
- Modify: `store/store.go`
- Modify: `internal/adapters/out/jsonfile/store.go`
- Test: `internal/adapters/out/jsonfile/store_test.go` (or create alongside existing tests)

`FaultStore` is **optional** (same pattern as `VersionedStore`) so external `DataStore` implementations don't break. The jsonfile store implements it now; remote stores (dynamo/mongo/cosmos) are follow-up tasks in phase 3+ if needed — admin API returns `501` when the store lacks the capability.

- [ ] **Step 1: Write failing test** (jsonfile round trip; follow the existing jsonfile test setup pattern — temp config file, `New(path)`)

```go
func TestFaultProfileCRUD(t *testing.T) {
	s := newTestStore(t) // reuse/adapt the existing helper that creates a store on a temp file
	p := domain.FaultProfile{ID: "fp1", Name: "p", Enabled: true,
		Faults: []domain.Fault{{Type: domain.FaultJitter, Probability: 1, Params: domain.FaultParams{JitterMs: 100}}}}

	if err := s.SaveFaultProfile(p); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFaultProfile("fp1")
	if err != nil || got == nil || got.Name != "p" {
		t.Fatalf("get: %v %v", got, err)
	}
	list, err := s.ListFaultProfiles()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if err := s.DeleteFaultProfile("fp1"); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetFaultProfile("fp1")
	if err != nil || got != nil {
		t.Fatalf("expected (nil,nil) after delete, got %v %v", got, err)
	}
}
```

- [ ] **Step 2: Run, verify failure** — `go test ./internal/adapters/out/jsonfile/ -run TestFaultProfileCRUD -v` → FAIL

- [ ] **Step 3: Add interface** (append to `store/store.go`)

```go
// FaultStore is an optional capability for stores that persist chaos fault
// profiles. Same contract style as DataStore: GetFaultProfile returns
// (nil, nil) when the profile does not exist.
type FaultStore interface {
	ListFaultProfiles() ([]domain.FaultProfile, error)
	GetFaultProfile(id string) (*domain.FaultProfile, error)
	SaveFaultProfile(p domain.FaultProfile) error
	DeleteFaultProfile(id string) error
}
```

- [ ] **Step 4: Implement in jsonfile store**

Follow the existing rule/simulation methods in `internal/adapters/out/jsonfile/store.go` exactly (same mutex, same persist-to-file call). The store already deserializes `domain.Config`, which now carries `FaultProfiles`. Implement the four methods operating on `cfg.FaultProfiles` (find/replace by ID on save; filter on delete; copy on list).

- [ ] **Step 5: Run tests** — `go test ./store/... ./internal/adapters/out/jsonfile/ -v` → PASS

- [ ] **Step 6: Commit**

```bash
git add store/store.go internal/adapters/out/jsonfile/
git commit -m "feat(store): optional FaultStore capability, jsonfile implementation"
```

---

### Task 4: Chaos package — kill switch

**Files:**
- Create: `internal/chaos/killswitch.go`
- Test: `internal/chaos/killswitch_test.go`

- [ ] **Step 1: Write failing test**

```go
package chaos_test

import (
	"testing"

	"github.com/mockwave/mockwave/internal/chaos"
)

func TestKillSwitch(t *testing.T) {
	ks := chaos.NewKillSwitch()
	if ks.Halted() {
		t.Fatal("new switch must start non-halted")
	}
	ks.Halt()
	if !ks.Halted() {
		t.Fatal("expected halted")
	}
	ks.Resume()
	if ks.Halted() {
		t.Fatal("expected resumed")
	}
}
```

- [ ] **Step 2: Run, verify failure** — `go test ./internal/chaos/ -v` → FAIL

- [ ] **Step 3: Implement**

```go
// Package chaos holds runtime chaos-engineering primitives: the global fault
// kill switch and the fault-injection pipeline stage.
package chaos

import "sync/atomic"

// KillSwitch globally disables fault injection. Zero value is usable but use
// NewKillSwitch for symmetry. Not persisted: a restart always resumes chaos.
type KillSwitch struct {
	halted atomic.Bool
}

func NewKillSwitch() *KillSwitch { return &KillSwitch{} }

func (k *KillSwitch) Halt()        { k.halted.Store(true) }
func (k *KillSwitch) Resume()      { k.halted.Store(false) }
func (k *KillSwitch) Halted() bool { return k.halted.Load() }
```

- [ ] **Step 4: Run tests** — PASS
- [ ] **Step 5: Commit** — `git commit -m "feat(chaos): global kill switch"`

---

### Task 5: FaultStage — jitter + error

**Files:**
- Create: `internal/chaos/stage.go`
- Modify: `internal/domain/pipeline/context.go`
- Test: `internal/chaos/stage_test.go`

Semantics (from spec): faults rolled independently in declaration order; first **terminal** fault (`error`) wins and short-circuits simulate/forward; `jitter` is a modifier and combines. Kill switch or `enabled:false` → no-op.

- [ ] **Step 1: Extend PipelineContext** (`internal/domain/pipeline/context.go`)

```go
	// FaultShortCircuit is set by the fault stage when a terminal fault fired:
	// later stages (simulation, script, forward) must skip processing.
	FaultShortCircuit bool
	// FaultDelayMs is extra latency injected by a jitter fault; protocol
	// adapters apply it together with Response.DelayMs.
	FaultDelayMs int
```

(append fields to `PipelineContext`)

- [ ] **Step 2: Write failing tests**

```go
package chaos_test

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/chaos"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

func profile(faults ...domain.Fault) map[string]domain.FaultProfile {
	return map[string]domain.FaultProfile{"p": {ID: "p", Enabled: true, Faults: faults}}
}

func pctxWithProfile() *pipeline.PipelineContext {
	return &pipeline.PipelineContext{Matched: &domain.Rule{ID: "r"}, FaultProfileID: "p"}
}

func TestErrorFaultShortCircuits(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultError, Probability: 1,
		Params: domain.FaultParams{StatusCode: 503, Body: `{"error":"boom"}`, Headers: map[string]string{"X-Fault": "1"}},
	}), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	if err := stage.Execute(context.Background(), pctx); err != nil {
		t.Fatal(err)
	}
	if !pctx.FaultShortCircuit || pctx.Response == nil || pctx.Response.Status != 503 {
		t.Fatalf("expected 503 short-circuit, got %+v", pctx.Response)
	}
}

func TestJitterFaultAddsDelay(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultJitter, Probability: 1,
		Params: domain.FaultParams{BaseDelayMs: 100, JitterMs: 50},
	}), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	if err := stage.Execute(context.Background(), pctx); err != nil {
		t.Fatal(err)
	}
	if pctx.FaultDelayMs < 100 || pctx.FaultDelayMs >= 150 {
		t.Fatalf("delay %d outside [100,150)", pctx.FaultDelayMs)
	}
	if pctx.FaultShortCircuit {
		t.Fatal("jitter must not short-circuit")
	}
}

func TestZeroProbabilityNeverFires(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultError, Probability: 0, Params: domain.FaultParams{StatusCode: 503},
	}), chaos.NewKillSwitch())
	for i := 0; i < 100; i++ {
		pctx := pctxWithProfile()
		_ = stage.Execute(context.Background(), pctx)
		if pctx.FaultShortCircuit {
			t.Fatal("probability 0 fired")
		}
	}
}

func TestKillSwitchDisablesFaults(t *testing.T) {
	ks := chaos.NewKillSwitch()
	ks.Halt()
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503},
	}), ks)
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if pctx.FaultShortCircuit {
		t.Fatal("halted switch must disable faults")
	}
}

func TestDisabledProfileIsNoop(t *testing.T) {
	profiles := profile(domain.Fault{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}})
	p := profiles["p"]
	p.Enabled = false
	profiles["p"] = p
	stage := chaos.NewFaultStage(profiles, chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if pctx.FaultShortCircuit {
		t.Fatal("disabled profile fired")
	}
}

func TestNoProfileIsNoop(t *testing.T) {
	stage := chaos.NewFaultStage(nil, chaos.NewKillSwitch())
	pctx := &pipeline.PipelineContext{Matched: &domain.Rule{ID: "r"}}
	if err := stage.Execute(context.Background(), pctx); err != nil {
		t.Fatal(err)
	}
}
```

Note: tests reference `pctx.FaultProfileID` — the router (Task 6) will set it; add the field in step 1 alongside the others:

```go
	// FaultProfileID is the chaos profile selected by the router for this
	// request's bucket ("" when the bucket has none).
	FaultProfileID string
```

- [ ] **Step 3: Run, verify failure** — `go test ./internal/chaos/ -v` → FAIL

- [ ] **Step 4: Implement** (`internal/chaos/stage.go`)

```go
package chaos

import (
	"context"
	"math/rand"
	"sync"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

// FaultStage rolls fault probabilities for the bucket-selected profile and
// either annotates the context (jitter) or short-circuits it (error).
// Runs after the percentile router, before the simulation stage.
type FaultStage struct {
	profiles map[string]domain.FaultProfile
	ks       *KillSwitch
	mu       sync.Mutex
	rng      *rand.Rand
}

func NewFaultStage(profiles map[string]domain.FaultProfile, ks *KillSwitch) *FaultStage {
	return &FaultStage{
		profiles: profiles,
		ks:       ks,
		rng:      rand.New(rand.NewSource(rand.Int63())),
	}
}

func (s *FaultStage) Name() string { return "fault" }

func (s *FaultStage) Execute(_ context.Context, pctx *pipeline.PipelineContext) error {
	if pctx.FaultProfileID == "" || s.ks.Halted() {
		return nil
	}
	p, ok := s.profiles[pctx.FaultProfileID]
	if !ok || !p.Enabled {
		return nil
	}
	for _, f := range p.Faults {
		if !s.roll(f.Probability) {
			continue
		}
		switch f.Type {
		case domain.FaultJitter:
			d := f.Params.BaseDelayMs
			if f.Params.JitterMs > 0 {
				d += s.intn(f.Params.JitterMs)
			}
			pctx.FaultDelayMs += d
		case domain.FaultError:
			pctx.Response = &pipeline.MockResponse{
				Status:  f.Params.StatusCode,
				Headers: f.Params.Headers,
				Body:    rawBody(f.Params.Body),
			}
			pctx.FaultShortCircuit = true
			pctx.ShouldForward = false
			pctx.SimulationID = ""
			return nil // first terminal fault wins
		}
	}
	return nil
}

func (s *FaultStage) roll(p float64) bool {
	if p <= 0 {
		return false
	}
	if p >= 1 {
		return true
	}
	s.mu.Lock()
	v := s.rng.Float64()
	s.mu.Unlock()
	return v < p
}

func (s *FaultStage) intn(n int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rng.Intn(n)
}

// rawBody keeps the configured string as-is; if it parses as JSON the adapter
// still serializes it correctly because MockResponse.Body is interface{}.
func rawBody(b string) interface{} {
	if b == "" {
		return nil
	}
	return jsonOrString(b)
}
```

Add the small helper in the same file:

```go
import "encoding/json"

func jsonOrString(s string) interface{} {
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		return v
	}
	return s
}
```

(merge the import into the import block)

- [ ] **Step 5: Run tests** — `go test ./internal/chaos/ -v` → PASS
- [ ] **Step 6: Commit** — `git commit -m "feat(chaos): fault stage with jitter and error faults"`

---

### Task 6: Wire into router, pipeline, simulation/script/forward skip, and adapter delay

**Files:**
- Modify: `internal/domain/routing/percentile.go` (set `pctx.FaultProfileID`)
- Modify: `server/server.go` (build FaultStage in `rebuild`, hold KillSwitch)
- Modify: `internal/domain/simulation/loader.go`, `internal/domain/pipeline/script_stage.go`, `internal/adapters/in/httprest/forward.go` (early-return when `pctx.FaultShortCircuit`)
- Modify: `internal/adapters/in/httprest/handler.go` (apply `FaultDelayMs`)
- Test: `internal/domain/routing/percentile_test.go`, `server/server_test.go`

- [ ] **Step 1: Router test** — in `percentile_test.go`, a rule with a single bucket carrying `FaultProfileID: "p"`; after `Execute`, assert `pctx.FaultProfileID == "p"`. Run → FAIL.

- [ ] **Step 2: Set profile in router** — in `routing/percentile.go` `Execute`, after the `switch`, add:

```go
	pctx.FaultProfileID = bucket.FaultProfileID
```

(set it before the switch so both actions get it — place right after `selectBucket` returns.)

- [ ] **Step 3: Skip-on-short-circuit guards** — at the top of each `Execute` in the simulation stage, script stage, and forward stage:

```go
	if pctx.FaultShortCircuit {
		return nil
	}
```

- [ ] **Step 4: Apply fault delay in the HTTP adapter** — in `internal/adapters/in/httprest/handler.go`, change the delay block:

```go
	if d := resp.DelayMs + pctx.FaultDelayMs; d > 0 {
		time.Sleep(time.Duration(d) * time.Millisecond)
	}
```

Apply the same pattern in the graphql and soap handlers if they sleep on `DelayMs` (check `internal/adapters/in/graphql/handler.go`, `internal/adapters/in/soap/handler.go`; mirror whatever they do for `DelayMs`).

- [ ] **Step 5: Server wiring** — in `server/server.go`:

Add to `Server` struct: `killSwitch *chaos.KillSwitch`. Initialize in `New` before `rebuild()`: `s.killSwitch = chaos.NewKillSwitch()`. Add accessor:

```go
// KillSwitch returns the global chaos kill switch for this server.
func (s *Server) KillSwitch() *chaos.KillSwitch { return s.killSwitch }
```

In `rebuild()`, load profiles (store may not implement FaultStore):

```go
	profMap := map[string]domain.FaultProfile{}
	if fs, ok := s.cfg.Store.(store.FaultStore); ok {
		profiles, err := fs.ListFaultProfiles()
		if err != nil {
			return fmt.Errorf("server: load fault profiles: %w", err)
		}
		for _, p := range profiles {
			profMap[p.ID] = p
		}
	}
	faultStage := chaos.NewFaultStage(profMap, s.killSwitch)
	p := pipeline.New(matchStage, routeStage, faultStage, simStage, scriptStage, fwdStage)
```

Import `"github.com/mockwave/mockwave/internal/chaos"`.

- [ ] **Step 6: Server-level test** (`server/server_test.go`, follow existing test setup with an in-memory/jsonfile store): rule with one bucket `FaultProfileID: "p"`, profile `p` with `error` probability 1 / status 503. Execute a request through `s.HTTPHandler()` with `httptest`; assert 503. Then `s.KillSwitch().Halt()`, re-request, assert the normal simulation response.

- [ ] **Step 7: Run full suite** — `go test ./...` → PASS
- [ ] **Step 8: Commit** — `git commit -m "feat(chaos): wire fault stage into pipeline, router and adapters"`

---

### Task 7: Rule validation — reject unknown fault profile references

**Files:**
- Modify: `internal/adapters/cfg/restapi/server.go` (rule save path)
- Test: `internal/adapters/cfg/restapi/server_test.go`

- [ ] **Step 1: Test** — POST `/api/rules` with a bucket `fault_profile_id: "ghost"` against a store with no such profile → expect `422`. Follow the existing rule-POST test pattern in `server_test.go`. Run → FAIL.

- [ ] **Step 2: Implement** — in the rule create/update handler, after `rule.Validate()`, when the store implements `store.FaultStore`, check every bucket's `FaultProfileID` via `GetFaultProfile`; unknown → `422` with `{"error": "unknown fault profile \"ghost\""}`. When the store lacks `FaultStore` and a bucket references a profile → also `422` ("store does not support fault profiles").

- [ ] **Step 3: Run tests, commit** — `git commit -m "feat(restapi): validate bucket fault profile references"`

---

### Task 8: Admin API — /api/faults CRUD + /api/chaos

**Files:**
- Modify: `internal/adapters/cfg/restapi/server.go`
- Test: `internal/adapters/cfg/restapi/server_test.go`

Endpoints (mirror the rules/simulations handler style — same JSON error shape, same method switching):

```
GET    /api/faults            → 200 [FaultProfile]            (501 if store lacks FaultStore)
POST   /api/faults            → 201 on create (validate; 409 if ID exists)
GET    /api/faults/{id}       → 200 | 404
PUT    /api/faults/{id}       → 200 on update (validate; 404 if missing)
DELETE /api/faults/{id}       → 204 | 404 | 409 when referenced by any rule bucket
POST   /api/chaos/halt        → 204
POST   /api/chaos/resume      → 204
GET    /api/chaos/status      → 200 {"halted": bool}
```

- [ ] **Step 1: Write failing tests** — table-driven, one per endpoint behavior, including: CRUD round trip, 501 with a plain DataStore (no FaultStore), delete-conflict 409 when a rule bucket references the profile, halt/status/resume cycle. Follow the `httptest.NewServer(NewMux(...))` pattern already in `server_test.go`.

- [ ] **Step 2: Implement** — `NewMux` gains the kill switch. Signature change: add a `*chaos.KillSwitch` parameter (or a `MuxOption` — follow how `ImportExport` is passed; prefer the existing `MuxOption` mechanism: `WithKillSwitch(ks)`). Register:

```go
	mux.HandleFunc("/api/faults", api.faults)
	mux.HandleFunc("/api/faults/", api.faultByID)
	mux.HandleFunc("/api/chaos/halt", api.chaosHalt)
	mux.HandleFunc("/api/chaos/resume", api.chaosResume)
	mux.HandleFunc("/api/chaos/status", api.chaosStatus)
```

Handlers cast `api.store` to `store.FaultStore`; missing capability → `501 {"error":"store does not support fault profiles"}`. After every successful write call the same reload hook used by rule writes (`onReload`) so the pipeline rebuilds with fresh profiles.

- [ ] **Step 3: Update server admin wiring** — `server/admin.go` (or wherever `NewMux` is called) passes the kill switch.

- [ ] **Step 4: Update `openapi.yaml`** in `internal/adapters/cfg/restapi/` with the new paths/schemas (FaultProfile, ChaosStatus).

- [ ] **Step 5: Run** — `go test ./internal/adapters/cfg/restapi/ ./server/ -v` → PASS
- [ ] **Step 6: Commit** — `git commit -m "feat(restapi): fault profile CRUD and chaos kill-switch endpoints"`

---

### Task 9: CLI — `mockwave fault` and `mockwave chaos`

**Files:**
- Create: `cmd/mockwave/chaos.go`
- Modify: `cmd/mockwave/main.go` (register commands)
- Test: `cmd/mockwave/chaos_test.go`

Commands talk to the admin API over HTTP; flag `--admin-url` (default `http://localhost:9090`).

```
mockwave fault list
mockwave fault get <id>
mockwave fault create -f profile.json
mockwave fault delete <id>
mockwave chaos halt | resume | status
```

- [ ] **Step 1: Write failing test** — spin `httptest.NewServer` with a fake mux recording requests; run command functions against its URL; assert method/path/body. Test at the function level (e.g. `runFaultList(baseURL, out io.Writer) error`) so cobra plumbing stays thin.

- [ ] **Step 2: Implement** (`cmd/mockwave/chaos.go`)

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

func adminClient(base, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}

func checkStatus(resp *http.Response, want int) error {
	if resp.StatusCode != want {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("admin API returned %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}
	return nil
}

func faultCmd() *cobra.Command {
	var adminURL string
	cmd := &cobra.Command{Use: "fault", Short: "Manage chaos fault profiles"}
	cmd.PersistentFlags().StringVar(&adminURL, "admin-url", "http://localhost:9090", "mockwave admin API base URL")

	list := &cobra.Command{Use: "list", Short: "List fault profiles", RunE: func(cmd *cobra.Command, _ []string) error {
		return runFaultList(adminURL, cmd.OutOrStdout())
	}}
	get := &cobra.Command{Use: "get <id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runFaultGet(adminURL, args[0], cmd.OutOrStdout())
	}}
	var file string
	create := &cobra.Command{Use: "create", Short: "Create a fault profile from a JSON file", RunE: func(cmd *cobra.Command, _ []string) error {
		return runFaultCreate(adminURL, file, cmd.OutOrStdout())
	}}
	create.Flags().StringVarP(&file, "file", "f", "", "path to fault profile JSON")
	_ = create.MarkFlagRequired("file")
	del := &cobra.Command{Use: "delete <id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return runFaultDelete(adminURL, args[0])
	}}
	cmd.AddCommand(list, get, create, del)
	return cmd
}

func runFaultList(base string, out io.Writer) error {
	resp, err := adminClient(base, http.MethodGet, "/api/faults", nil)
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

func runFaultGet(base, id string, out io.Writer) error {
	resp, err := adminClient(base, http.MethodGet, "/api/faults/"+id, nil)
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

func runFaultCreate(base, file string, out io.Writer) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if !json.Valid(data) {
		return fmt.Errorf("%s is not valid JSON", file)
	}
	resp, err := adminClient(base, http.MethodPost, "/api/faults", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusCreated); err != nil {
		return err
	}
	fmt.Fprintln(out, "created")
	return nil
}

func runFaultDelete(base, id string) error {
	resp, err := adminClient(base, http.MethodDelete, "/api/faults/"+id, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp, http.StatusNoContent)
}

func chaosCmd() *cobra.Command {
	var adminURL string
	cmd := &cobra.Command{Use: "chaos", Short: "Control the global chaos kill switch"}
	cmd.PersistentFlags().StringVar(&adminURL, "admin-url", "http://localhost:9090", "mockwave admin API base URL")

	mk := func(use, path string) *cobra.Command {
		return &cobra.Command{Use: use, RunE: func(*cobra.Command, []string) error {
			resp, err := adminClient(adminURL, http.MethodPost, path, nil)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			return checkStatus(resp, http.StatusNoContent)
		}}
	}
	status := &cobra.Command{Use: "status", RunE: func(cmd *cobra.Command, _ []string) error {
		resp, err := adminClient(adminURL, http.MethodGet, "/api/chaos/status", nil)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if err := checkStatus(resp, http.StatusOK); err != nil {
			return err
		}
		_, err = io.Copy(cmd.OutOrStdout(), resp.Body)
		return err
	}}
	cmd.AddCommand(mk("halt", "/api/chaos/halt"), mk("resume", "/api/chaos/resume"), status)
	return cmd
}
```

Register in `main.go`:

```go
	root.AddCommand(startCmd(), validateCmd(), versionCmd(), mcpCmd(), faultCmd(), chaosCmd())
```

- [ ] **Step 3: Run** — `go test ./cmd/mockwave/ -v` → PASS
- [ ] **Step 4: Commit** — `git commit -m "feat(cli): fault and chaos commands against admin API"`

---

### Task 10: Admin UI — Chaos tab

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html`
- Test: `internal/adapters/cfg/restapi/ui_test.go` (only if it asserts served content; otherwise manual verification)

The UI is a single vanilla-JS file. Follow its existing tab/section pattern (study how the Rules and Simulations tabs are built before editing).

- [ ] **Step 1: Add "Chaos" tab** alongside existing tabs, with three blocks:
  1. **Kill switch bar**: status fetched from `GET /api/chaos/status`; button toggles `POST /api/chaos/halt` / `/api/chaos/resume`; red "CHAOS HALTED" badge when halted.
  2. **Fault profile list**: table from `GET /api/faults` (id, name, enabled, fault count), edit/delete buttons. Hide tab content with a notice when the API returns `501`.
  3. **Profile editor**: form with id, name, description, enabled checkbox, and a dynamic fault list — each row: type select (`jitter`/`error`), probability number input (0–1 step 0.05), and type-specific params (jitter: baseDelayMs, jitterMs; error: statusCode, body textarea, headers key/value rows). Submit `POST`/`PUT /api/faults[/id]`.

- [ ] **Step 2: Bucket editor integration** — in the existing rule form's bucket rows, add a "Fault profile" `<select>` populated from `GET /api/faults` (empty option = none), writing `fault_profile_id` on the bucket object.

- [ ] **Step 3: Manual verification** — `make build && ./mockwave start --store=json --config <sample>` (see memory: embedded UI needs rebuild). Create a profile via UI, attach to a bucket, hit the mock endpoint with `curl`, observe injected 503s; halt via UI, observe normal responses.

- [ ] **Step 4: Commit** — `git commit -m "feat(admin): chaos tab with fault profiles and kill switch"`

---

### Task 11: Integration test + import/export coverage + docs update

**Files:**
- Create: `tests/integration/chaos_test.go`
- Modify: `internal/adapters/cfg/restapi/transfer.go` + `transfer_test.go` (export/import fault profiles)
- Modify: `README.md`, `docs/extending.md`

- [ ] **Step 1: Integration test** (follow `tests/integration/http_mock_test.go` setup): full server with jsonfile store; rule 100% simulate + fault profile `error` p=1 → client sees 503; `POST /api/chaos/halt` → client sees 200; `resume` → 503 again. Second test: jitter p=1 baseDelayMs=200 → response latency ≥ 200ms.

- [ ] **Step 2: Import/export** — `domain.Config` already carries `fault_profiles`; extend the export collector in `transfer.go` to include profiles referenced by exported rules' buckets, and the import path to upsert them (conflict reporting by ID, same two-phase shape as rules). Add tests mirroring the existing transfer tests.

- [ ] **Step 3: Docs** — extend the README "Chaos Testing" section (from Task 1) with fault profiles, kill switch, CLI examples. Add `store.FaultStore` to `docs/extending.md` next to `VersionedStore`.

- [ ] **Step 4: Full suite + vet** — `go test ./... && go vet ./...` → PASS
- [ ] **Step 5: Commit** — `git commit -m "feat(chaos): integration tests, import/export support, docs"`

---

## Out of scope (next plans)

- Phase 3: connection-level faults (`hang`, `reset`, `halfResponse`, `slowBody`) — adapter/`net.Conn` work.
- Phase 4: `retryStorm` (stateful counters).
- Phase 5: Scenarios (runner, overlay, API/CLI/UI).
- Remote-store (dynamo/mongo/cosmos) `FaultStore` implementations.
- gRPC fault semantics (roadmap in spec).
