# Forward Rule Timeout Simulation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-forward-bucket timeout/delay simulation, where the delay runs concurrently with the upstream proxy call so effective latency is `max(delay, upstream)`.

**Architecture:** Add a `DelayMs` field to `WeightedBucket` (domain model). The percentile router copies the selected forward bucket's delay into a new `ForwardDelayMs` pipeline-context field. The forward stage starts a timer concurrently with the upstream HTTP request and waits for both before returning. UI/OpenAPI expose the field per forward bucket.

**Tech Stack:** Go 1.26 (net/http, testify), vanilla JS frontend embedded in Go binary.

**Working directory:** `/Users/dub/projects/mockwave` (the mockwave repo, an additional working directory). All paths below are relative to it.

---

### Task 1: Add `DelayMs` to the domain model

**Files:**
- Modify: `domain/model.go` (`WeightedBucket` struct ~line 21, `WeightedBucket.Validate` ~line 27)
- Test: `domain/model_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Add to `domain/model_test.go` (if the file doesn't exist, create it with the package clause `package domain` and `import "testing"`):

```go
func TestWeightedBucket_NegativeDelayRejected(t *testing.T) {
	b := WeightedBucket{Weight: 100, Action: ActionForward, DelayMs: -1}
	if err := b.Validate(); err == nil {
		t.Fatal("expected error for negative delay_ms, got nil")
	}
}

func TestWeightedBucket_ForwardWithDelayValid(t *testing.T) {
	b := WeightedBucket{Weight: 100, Action: ActionForward, DelayMs: 2000}
	if err := b.Validate(); err != nil {
		t.Fatalf("expected forward bucket with delay to be valid, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/dub/projects/mockwave && go test ./domain/ -run TestWeightedBucket -v`
Expected: FAIL — compile error `b.DelayMs undefined` (field not yet added).

- [ ] **Step 3: Add the field and validation**

In `domain/model.go`, change the `WeightedBucket` struct to:

```go
// WeightedBucket is one branch in a rule's traffic split.
type WeightedBucket struct {
	Weight       int    `json:"weight"`              // relative weight, must be > 0
	Action       string `json:"action"`              // "simulate" | "forward"
	SimulationID string `json:"simulation_id"`       // required when Action = "simulate"
	DelayMs      int    `json:"delay_ms,omitempty"`  // forward bucket: min response time (ms), concurrent w/ upstream
}
```

In `WeightedBucket.Validate`, add this check after the existing action validation (before `return nil`):

```go
	if b.DelayMs < 0 {
		return fmt.Errorf("bucket delay_ms must be >= 0, got %d", b.DelayMs)
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/dub/projects/mockwave && go test ./domain/ -run TestWeightedBucket -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/dub/projects/mockwave
git add domain/model.go domain/model_test.go
git commit -m "feat(model): add DelayMs to WeightedBucket for forward timeout sim"
```

---

### Task 2: Carry the forward delay through the pipeline context + router

**Files:**
- Modify: `internal/domain/pipeline/context.go` (`PipelineContext` struct ~line 22)
- Modify: `internal/domain/routing/percentile.go` (`Execute` switch ~line 32)
- Test: `internal/domain/routing/percentile_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Add to `internal/domain/routing/percentile_test.go` (create with `package routing` if absent):

```go
package routing

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

func TestRouter_SetsForwardDelayMs(t *testing.T) {
	stage := NewPercentileRouterStage()
	pctx := &pipeline.PipelineContext{
		Matched: &domain.Rule{
			ID: "r1",
			Buckets: []domain.WeightedBucket{
				{Weight: 100, Action: domain.ActionForward, DelayMs: 1500},
			},
		},
	}
	if err := stage.Execute(context.Background(), pctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pctx.ShouldForward {
		t.Fatal("expected ShouldForward = true")
	}
	if pctx.ForwardDelayMs != 1500 {
		t.Fatalf("expected ForwardDelayMs = 1500, got %d", pctx.ForwardDelayMs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/dub/projects/mockwave && go test ./internal/domain/routing/ -run TestRouter_SetsForwardDelayMs -v`
Expected: FAIL — compile error `pctx.ForwardDelayMs undefined`.

- [ ] **Step 3: Add the context field**

In `internal/domain/pipeline/context.go`, add a field to `PipelineContext`:

```go
type PipelineContext struct {
	Request        NormalizedRequest
	Response       *MockResponse
	Matched        *domain.Rule
	SimulationID   string
	ShouldForward  bool
	ForwardDelayMs int // delay (ms) for the selected forward bucket, applied concurrently in the forward stage
}
```

- [ ] **Step 4: Set it in the router**

In `internal/domain/routing/percentile.go`, change the `ActionForward` case in `Execute`:

```go
	case domain.ActionForward:
		pctx.ShouldForward = true
		pctx.ForwardDelayMs = bucket.DelayMs
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd /Users/dub/projects/mockwave && go test ./internal/domain/routing/ -run TestRouter_SetsForwardDelayMs -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
cd /Users/dub/projects/mockwave
git add internal/domain/pipeline/context.go internal/domain/routing/percentile.go internal/domain/routing/percentile_test.go
git commit -m "feat(routing): carry forward bucket delay into pipeline context"
```

---

### Task 3: Apply the delay concurrently in the forward stage

**Files:**
- Modify: `internal/adapters/in/httprest/forward.go`
- Test: `internal/adapters/in/httprest/forward_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/adapters/in/httprest/forward_test.go` (the existing imports already include `context`, `net/http`, `net/http/httptest`, `testing`, the httprest/domain/pipeline packages, and testify; add `"time"` to the import block):

```go
func TestForwardStage_DelayDominatesWhenLongerThanUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`"ok"`))
	}))
	defer upstream.Close()

	stage := httprest.NewForwardStage(nil)
	pctx := &pipeline.PipelineContext{
		ShouldForward:  true,
		ForwardDelayMs: 200,
		Matched:        &domain.Rule{ID: "r1", ForwardURL: upstream.URL},
		Request:        pipeline.NormalizedRequest{Method: "GET", Path: "/test"},
	}
	start := time.Now()
	require.NoError(t, stage.Execute(context.Background(), pctx))
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond, "should wait at least the delay")
	assert.Less(t, elapsed, 1*time.Second, "delay should be concurrent, not additive to a long sleep")
	require.NotNil(t, pctx.Response)
	assert.Equal(t, 200, pctx.Response.Status)
}

func TestForwardStage_UpstreamDominatesWhenSlowerThanDelay(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`"ok"`))
	}))
	defer upstream.Close()

	stage := httprest.NewForwardStage(nil)
	pctx := &pipeline.PipelineContext{
		ShouldForward:  true,
		ForwardDelayMs: 50,
		Matched:        &domain.Rule{ID: "r1", ForwardURL: upstream.URL},
		Request:        pipeline.NormalizedRequest{Method: "GET", Path: "/test"},
	}
	start := time.Now()
	require.NoError(t, stage.Execute(context.Background(), pctx))
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond, "should wait for the slower upstream")
	require.NotNil(t, pctx.Response)
}

func TestForwardStage_ZeroDelayNoExtraWait(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`"ok"`))
	}))
	defer upstream.Close()

	stage := httprest.NewForwardStage(nil)
	pctx := &pipeline.PipelineContext{
		ShouldForward:  true,
		ForwardDelayMs: 0,
		Matched:        &domain.Rule{ID: "r1", ForwardURL: upstream.URL},
		Request:        pipeline.NormalizedRequest{Method: "GET", Path: "/test"},
	}
	start := time.Now()
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Less(t, time.Since(start), 100*time.Millisecond)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/dub/projects/mockwave && go test ./internal/adapters/in/httprest/ -run TestForwardStage_Delay -v`
Expected: FAIL — `TestForwardStage_DelayDominatesWhenLongerThanUpstream` fails because elapsed is far below 200ms (no delay applied yet). (`time` import unused will also error until the implementation uses it — that's fine, fix in Step 3.)

- [ ] **Step 3: Add the concurrent delay**

In `internal/adapters/in/httprest/forward.go`, add `"time"` to the import block. Then, inside `Execute`, start the timer immediately after the `ForwardURL` guard (before building/sending the request) and drain it right before the final `return nil`.

Insert after the `if pctx.Matched == nil || pctx.Matched.ForwardURL == "" { ... }` block (after line 33):

```go
	// Start the delay timer NOW so it runs concurrently with the upstream call.
	// Net effect on return is max(delay, upstreamLatency).
	var delay <-chan time.Time
	if pctx.ForwardDelayMs > 0 {
		delay = time.After(time.Duration(pctx.ForwardDelayMs) * time.Millisecond)
	}
```

Then change the end of the function — after `pctx.Response = &pipeline.MockResponse{...}` and before `return nil`:

```go
	pctx.Response = &pipeline.MockResponse{
		Status:  resp.StatusCode,
		Headers: headers,
		Body:    parsedBody,
	}
	if delay != nil {
		<-delay
	}
	return nil
```

Do NOT set `DelayMs` on the `MockResponse` — the HTTP handler applies that sequentially for mocks and would double-count.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/dub/projects/mockwave && go test ./internal/adapters/in/httprest/ -v`
Expected: PASS (all forward stage tests, including the pre-existing ones).

- [ ] **Step 5: Commit**

```bash
cd /Users/dub/projects/mockwave
git add internal/adapters/in/httprest/forward.go internal/adapters/in/httprest/forward_test.go
git commit -m "feat(forward): apply per-bucket delay concurrent with upstream call"
```

---

### Task 4: Document `delay_ms` in the OpenAPI schema

**Files:**
- Modify: `internal/adapters/cfg/restapi/openapi.yaml` (`WeightedBucket` schema ~line 269)

- [ ] **Step 1: Add the property**

In `internal/adapters/cfg/restapi/openapi.yaml`, add `delay_ms` to the `WeightedBucket` properties, after `simulation_id`:

```yaml
        simulation_id:
          type: string
        delay_ms:
          type: integer
          minimum: 0
          description: >-
            Forward buckets only. Minimum response time in milliseconds. The
            delay runs concurrently with the upstream proxy call, so the
            effective latency is max(delay_ms, upstream latency). Ignored for
            simulate buckets (mock delay lives on the simulation response).
```

- [ ] **Step 2: Verify YAML is well-formed**

Run: `cd /Users/dub/projects/mockwave && python3 -c "import yaml,sys; yaml.safe_load(open('internal/adapters/cfg/restapi/openapi.yaml'))" && echo OK`
Expected: `OK` (no traceback).

- [ ] **Step 3: Commit**

```bash
cd /Users/dub/projects/mockwave
git add internal/adapters/cfg/restapi/openapi.yaml
git commit -m "docs(openapi): document delay_ms on WeightedBucket"
```

---

### Task 5: Expose the delay field in the UI for forward buckets

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html` (`defaultBucket` ~line 366, `renderBuckets` forward branch ~line 454, `saveRule` forward payload ~line 805, `editRule` bucket reconstruction ~line 741)

This task is UI-only (no Go test). Verify manually in Step 6.

- [ ] **Step 1: Add `fwdDelayMs` to the default bucket**

In `defaultBucket()` (~line 366), add the field:

```javascript
  function defaultBucket() {
    return {
      weight: 100, action: 'simulate',
      simName: '', simStatus: 200,
      simBody: '{\n  "message": "OK"\n}',
      simContentType: 'application/json',
      simDelayMs: 0,
      simScript: '',
      bodyType: 'json',
      fwdDelayMs: 0,
      existingSimID: null
    };
  }
```

- [ ] **Step 2: Add the delay input to the forward branch of `renderBuckets`**

In `renderBuckets` (~line 454), replace the forward-branch `else` block (the one that currently renders only the "Request will be proxied…" note) with a version that adds a Delay input:

```javascript
          ${b.action === 'simulate' ? `
          <div class="form-group">
            <label>Simulation Label (optional)</label>
            <input value="${escapeAttr(b.simName)}" placeholder="e.g. 200 OK happy path" oninput="_buckets[${i}].simName=this.value">
          </div>` : `
          <div class="form-group" style="flex:0 0 5.5rem">
            <label>Delay (ms)</label>
            <input type="number" value="${b.fwdDelayMs}" min="0" oninput="_buckets[${i}].fwdDelayMs=parseInt(this.value)||0">
          </div>
          <div class="form-group" style="flex:1;align-self:flex-end">
            <span style="color:var(--muted);font-size:0.75rem">Proxied to the Forward URL below. Delay runs concurrently (max of delay and upstream).</span>
          </div>`}
```

- [ ] **Step 3: Include `delay_ms` in the forward bucket payload in `saveRule`**

In `saveRule` (~line 805), change the forward bucket push:

```javascript
        if (b.action === 'forward') {
          resolvedBuckets.push({ weight: b.weight, action: 'forward', delay_ms: b.fwdDelayMs || 0 });
          continue;
        }
```

- [ ] **Step 4: Populate `fwdDelayMs` when editing an existing rule**

In `editRule` (~line 742), where each bucket is reconstructed, set `fwdDelayMs` from the raw bucket:

```javascript
      const bucket = { ...defaultBucket(), weight: b.weight || 100, action: b.action || 'simulate', fwdDelayMs: b.delay_ms || 0 };
```

- [ ] **Step 5: Commit**

```bash
cd /Users/dub/projects/mockwave
git add internal/adapters/cfg/restapi/static/index.html
git commit -m "feat(ui): expose delay field for forward buckets"
```

- [ ] **Step 6: Manual verification**

Build and run the server, then verify end-to-end:

```bash
cd /Users/dub/projects/mockwave
go build ./... && echo BUILD_OK
go test ./... && echo TESTS_OK
```

Then run the app (see the project's run instructions / `server` package main), open the config UI, and:
1. Add a rule with a forward bucket; confirm a "Delay (ms)" input appears.
2. Set delay = 2000, point Forward URL at any fast endpoint, Save.
3. Edit the rule again; confirm the delay value round-trips (shows 2000).
4. Hit the mocked route; confirm the response takes ~2s.

Expected: `BUILD_OK`, `TESTS_OK`, and the route honors the delay.

---

## Self-Review

- **Spec coverage:** model field (Task 1) ✓, validation `>= 0` (Task 1) ✓, pipeline context field (Task 2) ✓, router copies delay (Task 2) ✓, concurrent `max` in forward stage without setting `Response.DelayMs` (Task 3) ✓, OpenAPI `delay_ms` (Task 4) ✓, UI field + default + save + load (Task 5) ✓, forward tests for delay>upstream / upstream>delay / zero (Task 3) ✓. Out-of-scope items (504/abort, rule-level delay) intentionally excluded.
- **Type consistency:** Go field `DelayMs` / JSON `delay_ms` consistent across model, context (`ForwardDelayMs`), router, OpenAPI. JS state key `fwdDelayMs` consistent across `defaultBucket`, `renderBuckets`, `saveRule`, `editRule`; wire field `delay_ms` matches Go JSON tag.
- **Placeholder scan:** none.
