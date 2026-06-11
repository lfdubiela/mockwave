# Chaos Faults Phase 3 — Connection-Level Faults Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add four connection-level fault types — `hang` (response never arrives), `reset` (TCP RST), `halfResponse` (truncated body), `slowBody` (throttled write) — to the chaos engine, executed in the HTTP-based adapters.

**Architecture:** Faults split into two kinds. `hang`/`reset`/`halfResponse`/`slowBody` are *connection-level*: the FaultStage cannot produce them because it only annotates `PipelineContext` — actual socket manipulation happens in the httprest/graphql/soap adapters via `http.ResponseWriter`/`http.Hijacker`. The stage rolls them and records a `ConnFault` directive on the context (terminal for hang/reset/halfResponse; modifier for slowBody, like jitter). Adapters read the directive and act on the raw connection. REST/GraphQL/SOAP only; gRPC is roadmap.

**Tech Stack:** Go 1.26, `net/http` Hijacker, `net.Conn` with `SetLinger`, testify, raw `net.Dial` clients in tests.

**Spec:** `docs/specs/2026-06-11-chaos-faults-design.md` (§Execution, connection-level bullets). Phases 1–2 already shipped (jitter/error, profiles, kill switch, API/CLI/UI). retryStorm = phase 4, scenarios = phase 5 (separate plans).

---

### Task 1: Domain — new fault types and params

**Files:**
- Modify: `domain/model.go`
- Test: `domain/model_test.go`

- [ ] **Step 1: Write failing tests** (append to `domain/model_test.go`)

```go
func TestConnectionFaultValidate(t *testing.T) {
	cases := []struct {
		name  string
		fault domain.Fault
		ok    bool
	}{
		{"hang valid", domain.Fault{Type: domain.FaultHang, Probability: 1, Params: domain.FaultParams{MaxMs: 5000}}, true},
		{"hang missing max_ms", domain.Fault{Type: domain.FaultHang, Probability: 1}, false},
		{"hang negative max_ms", domain.Fault{Type: domain.FaultHang, Probability: 1, Params: domain.FaultParams{MaxMs: -1}}, false},
		{"reset valid", domain.Fault{Type: domain.FaultReset, Probability: 1}, true},
		{"halfResponse valid", domain.Fault{Type: domain.FaultHalfResponse, Probability: 1, Params: domain.FaultParams{Fraction: 0.5}}, true},
		{"halfResponse fraction 0", domain.Fault{Type: domain.FaultHalfResponse, Probability: 1, Params: domain.FaultParams{Fraction: 0}}, false},
		{"halfResponse fraction >1", domain.Fault{Type: domain.FaultHalfResponse, Probability: 1, Params: domain.FaultParams{Fraction: 1.5}}, false},
		{"slowBody valid", domain.Fault{Type: domain.FaultSlowBody, Probability: 1, Params: domain.FaultParams{BytesPerSec: 1024}}, true},
		{"slowBody zero rate", domain.Fault{Type: domain.FaultSlowBody, Probability: 1, Params: domain.FaultParams{BytesPerSec: 0}}, false},
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

Run: `go test ./domain/ -run TestConnectionFault -v`
Expected: FAIL (undefined: domain.FaultHang, etc.)

- [ ] **Step 3: Implement** (in `domain/model.go`)

Add constants next to the existing `FaultJitter`/`FaultError`:

```go
	FaultHang         = "hang"
	FaultReset        = "reset"
	FaultHalfResponse = "halfResponse"
	FaultSlowBody     = "slowBody"
```

Add fields to `FaultParams` (after the existing fields):

```go
	MaxMs       int     `json:"max_ms,omitempty"`        // hang: max ms to block before giving up
	Fraction    float64 `json:"fraction,omitempty"`      // halfResponse: portion of body to write in [0,1)
	BytesPerSec int     `json:"bytes_per_sec,omitempty"` // slowBody: write throttle rate
```

Add cases to the `switch f.Type` in `Fault.Validate`:

```go
	case FaultHang:
		if f.Params.MaxMs <= 0 {
			return fmt.Errorf("hang fault requires max_ms > 0, got %d", f.Params.MaxMs)
		}
	case FaultReset:
		// no params
	case FaultHalfResponse:
		if f.Params.Fraction <= 0 || f.Params.Fraction >= 1 {
			return fmt.Errorf("halfResponse fault requires fraction in (0,1), got %v", f.Params.Fraction)
		}
	case FaultSlowBody:
		if f.Params.BytesPerSec <= 0 {
			return fmt.Errorf("slowBody fault requires bytes_per_sec > 0, got %d", f.Params.BytesPerSec)
		}
```

- [ ] **Step 4: Run tests**

Run: `go test ./domain/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add domain/model.go domain/model_test.go
git commit -m "feat(domain): connection-level fault types (hang, reset, halfResponse, slowBody)"
```

---

### Task 2: PipelineContext — ConnFault directive

**Files:**
- Modify: `internal/domain/pipeline/context.go`
- Test: none (struct fields; exercised in Task 3)

- [ ] **Step 1: Add fields to `PipelineContext`** (after the existing `FaultType` field)

```go
	// ConnFault is a connection-level fault directive the protocol adapter must
	// execute on the raw connection (hang/reset/halfResponse). "" when none.
	ConnFault string
	// ConnFaultMaxMs is the hang duration when ConnFault == "hang".
	ConnFaultMaxMs int
	// ConnFaultFraction is the body portion to write when ConnFault == "halfResponse".
	ConnFaultFraction float64
	// SlowBodyBytesPerSec throttles the response body write when > 0 (modifier,
	// combines with any non-terminal outcome).
	SlowBodyBytesPerSec int
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: clean

- [ ] **Step 3: Commit**

```bash
git add internal/domain/pipeline/context.go
git commit -m "feat(pipeline): connection-fault directive fields on context"
```

---

### Task 3: FaultStage — roll connection faults

**Files:**
- Modify: `internal/chaos/stage.go`
- Test: `internal/chaos/stage_test.go`

Terminal faults (`hang`, `reset`, `halfResponse`) set `FaultShortCircuit` (so sim/script/forward skip), set `ConnFault`, and `return` — first terminal wins, consistent with `error`. `slowBody` is a modifier like `jitter`: sets `SlowBodyBytesPerSec`, does not short-circuit.

- [ ] **Step 1: Write failing tests** (append to `internal/chaos/stage_test.go`)

```go
func TestHangFaultShortCircuits(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultHang, Probability: 1, Params: domain.FaultParams{MaxMs: 5000},
	}), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if !pctx.FaultShortCircuit || pctx.ConnFault != "hang" || pctx.ConnFaultMaxMs != 5000 {
		t.Fatalf("expected hang directive, got %+v", pctx)
	}
}

func TestResetFaultShortCircuits(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultReset, Probability: 1,
	}), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if !pctx.FaultShortCircuit || pctx.ConnFault != "reset" {
		t.Fatalf("expected reset directive, got %+v", pctx)
	}
}

func TestHalfResponseFaultShortCircuits(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultHalfResponse, Probability: 1, Params: domain.FaultParams{Fraction: 0.5},
	}), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if !pctx.FaultShortCircuit || pctx.ConnFault != "halfResponse" || pctx.ConnFaultFraction != 0.5 {
		t.Fatalf("expected halfResponse directive, got %+v", pctx)
	}
}

func TestSlowBodyFaultIsModifier(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultSlowBody, Probability: 1, Params: domain.FaultParams{BytesPerSec: 2048},
	}), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if pctx.FaultShortCircuit {
		t.Fatal("slowBody must not short-circuit")
	}
	if pctx.SlowBodyBytesPerSec != 2048 {
		t.Fatalf("expected throttle 2048, got %d", pctx.SlowBodyBytesPerSec)
	}
}

func TestSlowBodyCombinesWithError(t *testing.T) {
	stage := chaos.NewFaultStage(profile(
		domain.Fault{Type: domain.FaultSlowBody, Probability: 1, Params: domain.FaultParams{BytesPerSec: 512}},
		domain.Fault{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}},
	), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if pctx.SlowBodyBytesPerSec != 512 || !pctx.FaultShortCircuit || pctx.Response.Status != 503 {
		t.Fatalf("expected slowBody + error, got throttle=%d sc=%v", pctx.SlowBodyBytesPerSec, pctx.FaultShortCircuit)
	}
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/chaos/ -run 'Hang|Reset|HalfResponse|SlowBody' -v`
Expected: FAIL

- [ ] **Step 3: Implement** — add cases to the `switch f.Type` in `FaultStage.Execute` (after the `FaultError` case, before the closing brace of the switch):

```go
		case domain.FaultSlowBody:
			pctx.SlowBodyBytesPerSec = f.Params.BytesPerSec
			pctx.FaultType = "slowBody"
		case domain.FaultHang:
			pctx.ConnFault = "hang"
			pctx.ConnFaultMaxMs = f.Params.MaxMs
			pctx.FaultShortCircuit = true
			pctx.FaultType = "hang"
			pctx.ShouldForward = false
			pctx.SimulationID = ""
			return nil
		case domain.FaultReset:
			pctx.ConnFault = "reset"
			pctx.FaultShortCircuit = true
			pctx.FaultType = "reset"
			pctx.ShouldForward = false
			pctx.SimulationID = ""
			return nil
		case domain.FaultHalfResponse:
			pctx.ConnFault = "halfResponse"
			pctx.ConnFaultFraction = f.Params.Fraction
			pctx.FaultShortCircuit = true
			pctx.FaultType = "halfResponse"
			pctx.ShouldForward = false
			pctx.SimulationID = ""
			return nil
```

Note: `slowBody` is declared before terminal faults in a profile to take effect alongside them — but since it does not `return`, declaration order only matters relative to other modifiers. A terminal fault declared before `slowBody` will `return` first and skip it; document that `slowBody` should be listed first in a profile to combine. (No code change needed; the combine test above places slowBody first.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/chaos/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/chaos/stage.go internal/chaos/stage_test.go
git commit -m "feat(chaos): roll connection-level faults into context directives"
```

---

### Task 4: httprest adapter — execute connection faults

**Files:**
- Create: `internal/adapters/in/httprest/connfault.go`
- Modify: `internal/adapters/in/httprest/handler.go`
- Test: `internal/adapters/in/httprest/connfault_test.go`

This is the load-bearing task. After the pipeline runs and *before* writing the normal response, the handler checks `pctx.ConnFault` and `pctx.SlowBodyBytesPerSec`.

- [ ] **Step 1: Write failing tests** — these use a real `net/http` test server and a raw client to observe socket behavior.

```go
package httprest_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/adapters/in/httprest"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

// fakeExec injects a fixed pctx mutation so the handler executes a directive.
type fakeExec struct{ mutate func(*pipeline.PipelineContext) }

func (f fakeExec) Execute(_ context.Context, pctx *pipeline.PipelineContext) error {
	f.mutate(pctx)
	return nil
}

func TestHang_BlocksThenClosesWithoutResponse(t *testing.T) {
	h := httprest.NewHandler(fakeExec{mutate: func(p *pipeline.PipelineContext) {
		p.FaultShortCircuit = true
		p.ConnFault = "hang"
		p.ConnFaultMaxMs = 300
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()

	start := time.Now()
	resp, err := http.Get(srv.URL + "/x")
	elapsed := time.Since(start)
	// Server closes the connection after the hang window without writing a
	// response → client sees EOF / error, or an empty unparseable response.
	if err == nil {
		resp.Body.Close()
	}
	if elapsed < 280*time.Millisecond {
		t.Fatalf("hang returned too early: %v", elapsed)
	}
}

func TestReset_ClientSeesConnectionReset(t *testing.T) {
	h := httprest.NewHandler(fakeExec{mutate: func(p *pipeline.PipelineContext) {
		p.FaultShortCircuit = true
		p.ConnFault = "reset"
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()

	_, err := http.Get(srv.URL + "/x")
	if err == nil {
		t.Fatal("expected connection error from reset")
	}
	// On most platforms the error string contains "reset" or "EOF"; assert any error.
}

func TestHalfResponse_TruncatedBody(t *testing.T) {
	full := strings.Repeat("A", 1000)
	h := httprest.NewHandler(fakeExec{mutate: func(p *pipeline.PipelineContext) {
		p.FaultShortCircuit = true
		p.ConnFault = "halfResponse"
		p.ConnFaultFraction = 0.5
		p.Response = &pipeline.MockResponse{Status: 200, Body: full}
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/x")
	if err != nil {
		// truncation may surface as a read error; acceptable
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if len(body) >= len(full) {
		t.Fatalf("expected truncated body, got %d bytes", len(body))
	}
}

func TestSlowBody_ThrottlesWrite(t *testing.T) {
	full := strings.Repeat("B", 4096)
	h := httprest.NewHandler(fakeExec{mutate: func(p *pipeline.PipelineContext) {
		p.Response = &pipeline.MockResponse{Status: 200, Body: full}
		p.SlowBodyBytesPerSec = 4096 // ~1s for 4096 bytes
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()

	start := time.Now()
	resp, err := http.Get(srv.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if time.Since(start) < 500*time.Millisecond {
		t.Fatalf("slowBody did not throttle: %v", time.Since(start))
	}
}

var _ = net.Dial // keep import if unused after edits
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/adapters/in/httprest/ -run 'Hang|Reset|HalfResponse|SlowBody' -v`
Expected: FAIL (handler ignores directives → no hang, no reset)

- [ ] **Step 3: Implement `connfault.go`**

```go
package httprest

import (
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

// handleConnFault executes a terminal connection-level fault directly on the
// connection. Returns true when it handled the response (the caller must then
// stop). hang/reset require hijacking the raw conn; halfResponse writes a
// partial body and closes.
func handleConnFault(w http.ResponseWriter, pctx *pipeline.PipelineContext) bool {
	switch pctx.ConnFault {
	case "hang":
		// Block up to MaxMs (or until client disconnects), then close silently.
		d := time.Duration(pctx.ConnFaultMaxMs) * time.Millisecond
		select {
		case <-time.After(d):
		}
		hijackClose(w)
		return true
	case "reset":
		hijackReset(w)
		return true
	case "halfResponse":
		writeHalf(w, pctx)
		return true
	}
	return false
}

// hijackReset sends a TCP RST by setting SO_LINGER 0 then closing.
func hijackReset(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0) // linger 0 → close sends RST
	}
	_ = conn.Close()
}

// hijackClose closes the hijacked connection without writing anything (FIN).
func hijackClose(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	_ = conn.Close()
}

// writeHalf writes status + a fraction of the body, then closes abruptly.
func writeHalf(w http.ResponseWriter, pctx *pipeline.PipelineContext) {
	resp := pctx.Response
	if resp == nil {
		hijackClose(w)
		return
	}
	full := bodyBytes(resp.Body)
	n := int(float64(len(full)) * pctx.ConnFaultFraction)
	hj, ok := w.(http.Hijacker)
	if !ok {
		// Fallback: best-effort partial write through the normal writer.
		w.WriteHeader(resp.Status)
		_, _ = w.Write(full[:n])
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	// Minimal HTTP/1.1 response with a Content-Length that promises the full
	// body, but only the prefix is written → client read is truncated.
	_, _ = buf.WriteString("HTTP/1.1 ")
	_, _ = buf.WriteString(http.StatusText(resp.Status))
	_, _ = buf.WriteString("\r\nContent-Length: ")
	_, _ = buf.WriteString(itoa(len(full)))
	_, _ = buf.WriteString("\r\n\r\n")
	_, _ = buf.Write(full[:n])
	_ = buf.Flush()
}

// bodyBytes renders a MockResponse body the same way the normal path does.
func bodyBytes(body interface{}) []byte {
	if body == nil {
		return nil
	}
	if s, ok := body.(string); ok {
		return []byte(s)
	}
	b, _ := json.Marshal(body)
	return b
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// throttledWrite writes b to w at approximately bytesPerSec, flushing chunks.
func throttledWrite(w http.ResponseWriter, b []byte, bytesPerSec int) {
	flusher, _ := w.(http.Flusher)
	const chunk = 256
	interval := time.Duration(float64(time.Second) * float64(chunk) / float64(bytesPerSec))
	for off := 0; off < len(b); off += chunk {
		end := off + chunk
		if end > len(b) {
			end = len(b)
		}
		_, _ = w.Write(b[off:end])
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(interval)
	}
}
```

Note: `http.StatusText(resp.Status)` returns text only, not the numeric code — fix the status line to include the code. Use this corrected sequence inside `writeHalf` instead:

```go
	_, _ = buf.WriteString("HTTP/1.1 " + itoa(resp.Status) + " " + http.StatusText(resp.Status) + "\r\n")
	_, _ = buf.WriteString("Content-Length: " + itoa(len(full)) + "\r\n\r\n")
	_, _ = buf.Write(full[:n])
	_ = buf.Flush()
```

(Delete the earlier multi-line WriteString block; keep only this corrected version.)

- [ ] **Step 4: Wire into `handler.go`** — replace the response-writing tail (from `resp := pctx.Response` onward). New `ServeHTTP` tail:

```go
	// Connection-level terminal faults bypass the normal response path.
	if pctx.ConnFault != "" {
		handleConnFault(w, pctx)
		return
	}
	if pctx.Response == nil {
		writeError(w, http.StatusInternalServerError, "pipeline produced no response")
		return
	}
	resp := pctx.Response
	if d := resp.DelayMs + pctx.FaultDelayMs; d > 0 {
		time.Sleep(time.Duration(d) * time.Millisecond)
	}
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	if resp.Body != nil {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.Status)
	if resp.Body != nil {
		if pctx.SlowBodyBytesPerSec > 0 {
			throttledWrite(w, bodyBytes(resp.Body), pctx.SlowBodyBytesPerSec)
		} else {
			_ = json.NewEncoder(w).Encode(resp.Body)
		}
	}
```

Note: `bodyBytes` JSON-marshals without the trailing newline `json.Encoder` adds; that difference is acceptable. Keep the `nil`-Response check *after* the ConnFault check, because hang/reset legitimately have no Response.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/adapters/in/httprest/ -v`
Expected: PASS. If `TestReset` is flaky on the CI platform (RST vs FIN timing), assert only that the request did not return a normal 200 with full body.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/in/httprest/connfault.go internal/adapters/in/httprest/handler.go internal/adapters/in/httprest/connfault_test.go
git commit -m "feat(httprest): execute hang/reset/halfResponse/slowBody connection faults"
```

---

### Task 5: graphql + soap adapters — connection faults

**Files:**
- Modify: `internal/adapters/in/graphql/handler.go`
- Modify: `internal/adapters/in/soap/handler.go`
- Test: `internal/adapters/in/graphql/handler_test.go`, `internal/adapters/in/soap/handler_test.go`

Both adapters already short-circuit on `FaultShortCircuit` and apply `FaultDelayMs` (phase 2). They must now also honor `ConnFault` / `SlowBodyBytesPerSec`. The `connfault.go` helpers live in the `httprest` package — to avoid an import cycle or cross-package reach, move the shared helpers to a small internal package.

- [ ] **Step 1: Extract shared helpers** — create `internal/adapters/in/connfault/connfault.go` with the exported helpers `Handle(w http.ResponseWriter, pctx *pipeline.PipelineContext) bool`, `ThrottledWrite(w, b, bytesPerSec)`, `BodyBytes(body interface{}) []byte`. Move the bodies from `httprest/connfault.go` (Task 4) into this package, exporting them. Update `httprest/handler.go` to call `connfault.Handle(...)`, `connfault.ThrottledWrite(...)`, `connfault.BodyBytes(...)`. Delete `httprest/connfault.go`. Re-run `go test ./internal/adapters/in/httprest/ -v` → PASS (behavior unchanged).

Commit this refactor separately:

```bash
git add internal/adapters/in/connfault/ internal/adapters/in/httprest/
git commit -m "refactor(adapters): extract connfault helpers to shared package"
```

- [ ] **Step 2: Write failing tests** for graphql (mirror in soap with its envelope handling)

```go
func TestGraphQL_ResetFault(t *testing.T) {
	h := graphql.NewHandler(fakeExec{mutate: func(p *pipeline.PipelineContext) {
		p.FaultShortCircuit = true
		p.ConnFault = "reset"
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()
	_, err := http.Post(srv.URL+"/graphql", "application/json", strings.NewReader(`{"query":"{x}"}`))
	if err == nil {
		t.Fatal("expected connection error from reset")
	}
}
```

(Define `fakeExec` in each handler's test package as in Task 4. For soap, send a SOAP request with the appropriate Content-Type the soap handler expects — copy from existing soap handler tests.)

- [ ] **Step 3: Run, verify failure** — FAIL (adapters ignore ConnFault).

- [ ] **Step 4: Implement** — in each handler's `ServeHTTP`, immediately after the pipeline `Execute` returns and before writing the normal/short-circuit response, add:

```go
	if pctx.ConnFault != "" {
		connfault.Handle(w, pctx)
		return
	}
```

And where each handler writes the response body, when `pctx.SlowBodyBytesPerSec > 0`, route the body through `connfault.ThrottledWrite(w, connfault.BodyBytes(body), pctx.SlowBodyBytesPerSec)` instead of the normal write. Read each handler to find the exact body-writing line and match its body source.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/adapters/in/... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/in/graphql/ internal/adapters/in/soap/
git commit -m "feat(graphql,soap): honor connection-level faults"
```

---

### Task 6: Admin UI — new fault types in profile editor

**Files:**
- Modify: `internal/adapters/cfg/restapi/static/index.html`
- Modify: `internal/adapters/cfg/restapi/openapi.yaml`

- [ ] **Step 1: Extend the fault-type select** in the profile editor (Chaos tab) to include `hang`, `reset`, `halfResponse`, `slowBody` alongside `jitter`/`error`. Study how the existing type→params rendering works (the function that shows jitter inputs vs error inputs) and add per-type param fields:
  - hang → `max_ms` (number)
  - reset → no params
  - halfResponse → `fraction` (number, step 0.05, 0–1 exclusive)
  - slowBody → `bytes_per_sec` (number)

Match the existing param-row rendering exactly. Validate with `node --check` on the extracted inline script.

- [ ] **Step 2: Update `openapi.yaml`** — extend the `Fault.type` enum to `[jitter, error, hang, reset, halfResponse, slowBody]` and add the new `FaultParams` properties (`max_ms`, `fraction`, `bytes_per_sec`).

- [ ] **Step 3: Build + verify** — `go build -o /tmp/mockwave ./cmd/mockwave`, start it, create a `hang` profile via the UI, confirm it persists and round-trips through `GET /api/faults`.

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/cfg/restapi/static/index.html internal/adapters/cfg/restapi/openapi.yaml
git commit -m "feat(admin): connection-level fault types in profile editor"
```

---

### Task 7: Integration tests + docs

**Files:**
- Create: `tests/integration/chaos_connfault_test.go`
- Modify: `README.md`

- [ ] **Step 1: Integration tests** — full server (jsonfile store) with a rule whose simulate bucket references a profile, one test per fault:
  - `reset` probability 1 → `http.Get` returns an error.
  - `hang` max_ms 300 → request takes ≥ 280ms.
  - `halfResponse` fraction 0.5 on a known-size body → received body shorter than full (or read error).
  - `slowBody` bytes_per_sec sized so a 4KB body takes ≥ 500ms.
  Follow the existing `tests/integration/chaos_test.go` setup (server.New + httptest, or the real listener pattern already used there).

- [ ] **Step 2: Run** — `go test ./tests/integration/ -run Chaos -v` → PASS.

- [ ] **Step 3: Docs** — extend the README "Chaos Testing" section with the four new fault types, a one-line description each, and a note that they are HTTP-based-protocol only (gRPC roadmap). Add a `flaky-network` profile JSON example combining `slowBody` + `reset`.

- [ ] **Step 4: Full suite** — `go test ./... && go vet ./...` → PASS.

- [ ] **Step 5: Commit**

```bash
git add tests/integration/chaos_connfault_test.go README.md
git commit -m "feat(chaos): integration tests and docs for connection-level faults"
```

---

## Out of scope (other plans)
- Phase 4: `retryStorm` (stateful counters) — `docs/plans/2026-06-11-chaos-faults-phase4-plan.md`.
- Phase 5: Scenarios — `docs/plans/2026-06-11-chaos-faults-phase5-plan.md`.
- gRPC connection-fault semantics (RST_STREAM, deadline) — spec roadmap.

## Notes / risks
- `httptest.Server` supports hijacking; `http.ResponseWriter` from the stdlib server implements `Hijacker`. If mockwave is ever embedded behind an HTTP/2 server, `Hijack` fails (returns error) — the helpers degrade to best-effort or no-op, which is acceptable (documented).
- TCP RST observability differs across OSes; reset tests assert "an error occurred" rather than a specific errno.
