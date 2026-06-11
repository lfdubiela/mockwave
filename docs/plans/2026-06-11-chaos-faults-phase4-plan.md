# Chaos Faults Phase 4 — Retry Storm Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the `retryStorm` fault — fail the first N requests per key, then let requests through — to validate client backoff and idempotency logic.

**Architecture:** `retryStorm` is a stateful terminal fault. The FaultStage owns an in-memory counter keyed by a request attribute (`path` or a named header), with a sliding TTL window. The first `fail_first` requests for a key within the window short-circuit with the configured status; the (N+1)th onward pass through. Counters are per-process and reset on restart (documented limitation). Reuses the phase-2 `error`-style short-circuit machinery.

**Tech Stack:** Go 1.26, `sync.Mutex`-guarded map with timestamps, testify.

**Spec:** `docs/specs/2026-06-11-chaos-faults-design.md` (§Execution, retryStorm bullet; params `fail_first`, `status_code`, `key_by`, `window_sec`). Depends on phases 1–2 (shipped). Independent of phase 3.

---

### Task 1: Domain — retryStorm fault type and params

**Files:**
- Modify: `domain/model.go`
- Test: `domain/model_test.go`

- [ ] **Step 1: Write failing tests** (append to `domain/model_test.go`)

```go
func TestRetryStormValidate(t *testing.T) {
	cases := []struct {
		name  string
		fault domain.Fault
		ok    bool
	}{
		{"valid path key", domain.Fault{Type: domain.FaultRetryStorm, Probability: 1,
			Params: domain.FaultParams{FailFirst: 3, StatusCode: 503, KeyBy: "path", WindowSec: 60}}, true},
		{"valid header key", domain.Fault{Type: domain.FaultRetryStorm, Probability: 1,
			Params: domain.FaultParams{FailFirst: 1, StatusCode: 500, KeyBy: "header:X-Request-Id", WindowSec: 30}}, true},
		{"fail_first zero", domain.Fault{Type: domain.FaultRetryStorm, Probability: 1,
			Params: domain.FaultParams{FailFirst: 0, StatusCode: 503, KeyBy: "path", WindowSec: 60}}, false},
		{"bad status", domain.Fault{Type: domain.FaultRetryStorm, Probability: 1,
			Params: domain.FaultParams{FailFirst: 3, StatusCode: 0, KeyBy: "path", WindowSec: 60}}, false},
		{"bad key_by", domain.Fault{Type: domain.FaultRetryStorm, Probability: 1,
			Params: domain.FaultParams{FailFirst: 3, StatusCode: 503, KeyBy: "ip", WindowSec: 60}}, false},
		{"header without name", domain.Fault{Type: domain.FaultRetryStorm, Probability: 1,
			Params: domain.FaultParams{FailFirst: 3, StatusCode: 503, KeyBy: "header:", WindowSec: 60}}, false},
		{"window zero", domain.Fault{Type: domain.FaultRetryStorm, Probability: 1,
			Params: domain.FaultParams{FailFirst: 3, StatusCode: 503, KeyBy: "path", WindowSec: 0}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fault.Validate()
			if tc.ok && err != nil {
				t.Fatalf("expected valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./domain/ -run TestRetryStorm -v`
Expected: FAIL (undefined: domain.FaultRetryStorm)

- [ ] **Step 3: Implement** (in `domain/model.go`)

Add constant:

```go
	FaultRetryStorm = "retryStorm"
```

Add fields to `FaultParams`:

```go
	FailFirst int    `json:"fail_first,omitempty"` // retryStorm: number of initial requests per key to fail
	KeyBy     string `json:"key_by,omitempty"`     // retryStorm: "path" or "header:<Name>"
	WindowSec int    `json:"window_sec,omitempty"` // retryStorm: sliding window TTL in seconds
```

Add a `switch` case in `Fault.Validate`:

```go
	case FaultRetryStorm:
		if f.Params.FailFirst <= 0 {
			return fmt.Errorf("retryStorm fault requires fail_first > 0, got %d", f.Params.FailFirst)
		}
		if f.Params.StatusCode < 100 || f.Params.StatusCode > 599 {
			return fmt.Errorf("retryStorm fault requires status_code in [100,599], got %d", f.Params.StatusCode)
		}
		if f.Params.WindowSec <= 0 {
			return fmt.Errorf("retryStorm fault requires window_sec > 0, got %d", f.Params.WindowSec)
		}
		switch {
		case f.Params.KeyBy == "path":
		case strings.HasPrefix(f.Params.KeyBy, "header:") && len(f.Params.KeyBy) > len("header:"):
		default:
			return fmt.Errorf("retryStorm key_by must be \"path\" or \"header:<Name>\", got %q", f.Params.KeyBy)
		}
```

Add `"strings"` to the `domain/model.go` import block if not already present.

- [ ] **Step 4: Run tests**

Run: `go test ./domain/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add domain/model.go domain/model_test.go
git commit -m "feat(domain): retryStorm fault type and params"
```

---

### Task 2: retryStorm counter — standalone, testable unit

**Files:**
- Create: `internal/chaos/retrystorm.go`
- Test: `internal/chaos/retrystorm_test.go`

A self-contained counter so the time-window logic is tested in isolation with an injectable clock (no real sleeps).

- [ ] **Step 1: Write failing tests**

```go
package chaos

import (
	"testing"
	"time"
)

func TestRetryCounter_FailsFirstN(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newRetryCounter(func() time.Time { return now })
	// fail_first=3, window=60s
	for i := 0; i < 3; i++ {
		if !c.shouldFail("k", 3, 60) {
			t.Fatalf("request %d should fail", i)
		}
	}
	if c.shouldFail("k", 3, 60) {
		t.Fatal("4th request should pass")
	}
}

func TestRetryCounter_WindowExpiryResets(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	c := newRetryCounter(clock)
	for i := 0; i < 3; i++ {
		c.shouldFail("k", 3, 60)
	}
	// advance past the window → counter resets, fails again
	now = now.Add(61 * time.Second)
	if !c.shouldFail("k", 3, 60) {
		t.Fatal("after window expiry the count should reset and fail again")
	}
}

func TestRetryCounter_KeysIndependent(t *testing.T) {
	now := time.Unix(1000, 0)
	c := newRetryCounter(func() time.Time { return now })
	c.shouldFail("a", 1, 60) // a exhausted
	if !c.shouldFail("b", 1, 60) {
		t.Fatal("key b must have its own counter")
	}
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/chaos/ -run TestRetryCounter -v`
Expected: FAIL (undefined: newRetryCounter)

- [ ] **Step 3: Implement** (`internal/chaos/retrystorm.go`)

```go
package chaos

import (
	"sync"
	"time"
)

// retryCounter tracks how many requests each key has seen inside its current
// window. The window is sliding: the first request for a key stamps the window
// start; once windowSec elapses, the next request resets the count.
type retryCounter struct {
	mu    sync.Mutex
	now   func() time.Time
	state map[string]*retryEntry
}

type retryEntry struct {
	count       int
	windowStart time.Time
}

func newRetryCounter(now func() time.Time) *retryCounter {
	return &retryCounter{now: now, state: map[string]*retryEntry{}}
}

// shouldFail reports whether this request for key should be failed, given the
// fault's failFirst threshold and windowSec TTL. It also advances the counter.
func (c *retryCounter) shouldFail(key string, failFirst, windowSec int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.now()
	e, ok := c.state[key]
	if !ok || t.Sub(e.windowStart) >= time.Duration(windowSec)*time.Second {
		e = &retryEntry{windowStart: t}
		c.state[key] = e
	}
	e.count++
	return e.count <= failFirst
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/chaos/ -run TestRetryCounter -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/chaos/retrystorm.go internal/chaos/retrystorm_test.go
git commit -m "feat(chaos): retry-storm counter with sliding window"
```

---

### Task 3: FaultStage — wire retryStorm

**Files:**
- Modify: `internal/chaos/stage.go`
- Test: `internal/chaos/stage_test.go`

The stage needs the request's path and headers to compute the key. These live on `pctx.Request` (`NormalizedRequest` has `Path` and `Headers map[string]string`, headers lower-cased). The stage gains a `retryCounter` field.

- [ ] **Step 1: Write failing tests** (append to `internal/chaos/stage_test.go`)

```go
func TestRetryStormFailsFirstThenPasses(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultRetryStorm, Probability: 1,
		Params: domain.FaultParams{FailFirst: 2, StatusCode: 503, KeyBy: "path", WindowSec: 60},
	}), chaos.NewKillSwitch())

	mk := func() *pipeline.PipelineContext {
		return &pipeline.PipelineContext{
			Matched:        &domain.Rule{ID: "r"},
			FaultProfileID: "p",
			Request:        pipeline.NormalizedRequest{Path: "/orders"},
		}
	}
	// first two fail
	for i := 0; i < 2; i++ {
		pctx := mk()
		_ = stage.Execute(context.Background(), pctx)
		if !pctx.FaultShortCircuit || pctx.Response.Status != 503 {
			t.Fatalf("request %d should fail with 503", i)
		}
	}
	// third passes
	pctx := mk()
	_ = stage.Execute(context.Background(), pctx)
	if pctx.FaultShortCircuit {
		t.Fatal("third request should pass through")
	}
}

func TestRetryStormKeyByHeader(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultRetryStorm, Probability: 1,
		Params: domain.FaultParams{FailFirst: 1, StatusCode: 500, KeyBy: "header:x-request-id", WindowSec: 60},
	}), chaos.NewKillSwitch())

	mk := func(id string) *pipeline.PipelineContext {
		return &pipeline.PipelineContext{
			Matched:        &domain.Rule{ID: "r"},
			FaultProfileID: "p",
			Request:        pipeline.NormalizedRequest{Path: "/x", Headers: map[string]string{"x-request-id": id}},
		}
	}
	p1 := mk("req-1")
	_ = stage.Execute(context.Background(), p1)
	if !p1.FaultShortCircuit {
		t.Fatal("first req-1 should fail")
	}
	// different header value → independent counter, fails on its own first hit
	p2 := mk("req-2")
	_ = stage.Execute(context.Background(), p2)
	if !p2.FaultShortCircuit {
		t.Fatal("first req-2 should fail (independent key)")
	}
	// req-1 again → already exhausted → passes
	p1b := mk("req-1")
	_ = stage.Execute(context.Background(), p1b)
	if p1b.FaultShortCircuit {
		t.Fatal("second req-1 should pass")
	}
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/chaos/ -run TestRetryStorm -v`
Expected: FAIL

- [ ] **Step 3: Implement**

Add the counter field to `FaultStage` and initialize it in `NewFaultStage`:

```go
type FaultStage struct {
	profiles map[string]domain.FaultProfile
	ks       *KillSwitch
	mu       sync.Mutex
	rng      *rand.Rand
	retry    *retryCounter
}

func NewFaultStage(profiles map[string]domain.FaultProfile, ks *KillSwitch) *FaultStage {
	return &FaultStage{
		profiles: profiles,
		ks:       ks,
		rng:      rand.New(rand.NewSource(rand.Int63())),
		retry:    newRetryCounter(time.Now),
	}
}
```

Add `"time"` and `"strings"` to the `internal/chaos/stage.go` imports.

Add a case in the `switch f.Type` (terminal, mirrors `error`):

```go
		case domain.FaultRetryStorm:
			key := retryKey(pctx, f.Params.KeyBy)
			if !s.retry.shouldFail(key, f.Params.FailFirst, f.Params.WindowSec) {
				continue // window exhausted → let this request pass; keep rolling other faults
			}
			pctx.Response = &pipeline.MockResponse{Status: f.Params.StatusCode}
			pctx.FaultShortCircuit = true
			pctx.FaultType = "retryStorm"
			pctx.ShouldForward = false
			pctx.SimulationID = ""
			return nil
```

Add the key helper at the bottom of the file:

```go
// retryKey derives the retry-storm bucket key from the request per the
// fault's key_by setting. Header names are lower-cased to match the adapter's
// normalized header map.
func retryKey(pctx *pipeline.PipelineContext, keyBy string) string {
	if keyBy == "path" {
		return "path:" + pctx.Request.Path
	}
	if name, ok := strings.CutPrefix(keyBy, "header:"); ok {
		return "hdr:" + name + ":" + pctx.Request.Headers[strings.ToLower(name)]
	}
	return "path:" + pctx.Request.Path // defensive default; validation forbids reaching here
}
```

Note: when retryStorm decides *not* to fail (window exhausted), it `continue`s rather than `return`s, so a later fault in the profile could still fire — matches "pass through normally" since on its own it leaves the context untouched.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/chaos/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/chaos/stage.go internal/chaos/stage_test.go
git commit -m "feat(chaos): retryStorm fault wired into stage"
```

---

### Task 4: Admin UI + openapi + integration + docs

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html`
- Modify: `internal/adapters/cfg/restapi/openapi.yaml`
- Create: `tests/integration/chaos_retrystorm_test.go`
- Modify: `README.md`

- [ ] **Step 1: UI** — add `retryStorm` to the fault-type select with params: `fail_first` (number), `status_code` (number), `key_by` (text, placeholder `path` or `header:X-Request-Id`), `window_sec` (number). Match existing param-row rendering. `node --check` the extracted script.

- [ ] **Step 2: openapi.yaml** — add `retryStorm` to the `Fault.type` enum and `fail_first`/`key_by`/`window_sec` to `FaultParams`.

- [ ] **Step 3: Integration test** (`tests/integration/chaos_retrystorm_test.go`) — full server, rule 100% simulate referencing a retryStorm profile (`fail_first: 2`, `key_by: path`, `status_code: 503`, `window_sec: 60`). Three sequential `GET /same-path`: assert 503, 503, then 200 (the simulation's real response). Follow `tests/integration/chaos_test.go` setup.

- [ ] **Step 4: Run** — `go test ./tests/integration/ -run Chaos -v` → PASS.

- [ ] **Step 5: Docs** — README "Chaos Testing": document retryStorm (what it tests: client backoff/idempotency), the `key_by` options, and the per-process/reset-on-restart caveat. Add a profile example.

- [ ] **Step 6: Full suite** — `go test ./... && go vet ./...` → PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html internal/adapters/cfg/restapi/openapi.yaml tests/integration/chaos_retrystorm_test.go README.md
git commit -m "feat(chaos): retryStorm UI, openapi, integration test, docs"
```

---

## Out of scope (other plans)
- Phase 3: connection-level faults — `docs/plans/2026-06-11-chaos-faults-phase3-plan.md`.
- Phase 5: Scenarios — `docs/plans/2026-06-11-chaos-faults-phase5-plan.md`.

## Notes / risks
- Counters are per-process: behind a load balancer with multiple mockwave instances the failFirst budget multiplies by instance count. Documented; out of scope to make distributed.
- The counter map grows unbounded with distinct keys (e.g. `key_by: path` with high-cardinality paths). Acceptable for a chaos/testing tool; a follow-up could add periodic eviction of expired entries. Flag in docs, do not implement now (YAGNI).
