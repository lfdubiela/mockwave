# Admin API Public Exposure — Design Spec

**Date:** 2026-06-03

## Goal

Expose the admin HTTP server as part of the public `server` package so applications embedding the Mockwave library can start it. Currently `AdminPort int` in `server.Config` is populated but never acted on — admin API is only wired in the standalone CLI via internal packages inaccessible to embedders.

---

## Background

`server.Config` already has `AdminPort int`. `server.New()` ignores it. The admin API (`internal/adapters/cfg/restapi`) depends on four internal types:

- `internal/metrics.Collector` — per-request counters and histograms
- `internal/metrics.Broker` — SSE fanout for live metric streams
- `internal/unmatched.Buffer` — ring buffer of unmatched requests
- `internal/scripting.Engine` — JS eval (already owned by Server)

The CLI constructs these manually and wires them itself. Library embedders have no path to do the same.

---

## Design Decisions

| Question | Decision |
|----------|----------|
| Auto-start vs explicit `AdminHandler()` | Auto-start when `AdminPort > 0` (option A) |
| `NewProxy()` metrics wrapping | Always wrap — embedder gets dashboard data for free |
| `Shutdown()` method | Yes — `Shutdown(ctx context.Context) error` |
| CLI changes | None — CLI passes `AdminPort: 0`, keeps its manual admin wiring |

---

## Architecture

### New Server fields (private)

```go
type Server struct {
    cfg          Config
    mu           sync.RWMutex
    pipeline     *pipeline.Pipeline
    engine       *scripting.Engine
    collector    *metrics.Collector   // NEW
    buffer       *unmatched.Buffer    // NEW
    broker       *metrics.Broker      // NEW
    brokerCancel context.CancelFunc   // NEW — cancels broker goroutine
    adminSrv     *http.Server         // NEW — nil when AdminPort == 0
}
```

`collector`, `buffer`, and `broker` are **always** initialized in `New()` — they are needed by `NewProxy()` regardless of whether the admin server starts. `adminSrv` is only populated when `AdminPort > 0`.

`Broker.Start(ctx)` is controlled via a derived context stored as `brokerCancel`. `Shutdown()` calls it to stop the broker goroutine.

### `New()` changes

1. Initialize subsystems: `collector = metrics.NewCollector()`, `buffer = unmatched.NewBuffer(100)`, `broker = metrics.NewBroker(collector)`
2. Start broker with cancellable context: `brokerCtx, brokerCancel := context.WithCancel(context.Background()); go s.broker.Start(brokerCtx); s.brokerCancel = brokerCancel`
3. If `cfg.AdminPort > 0`: call `s.startAdmin()` — bind listener, store `*http.Server`
4. `startAdmin()` failure returns error from `New()`

### `NewProxy()` change

Returns a metrics-wrapped executor instead of a raw pipeline proxy:

```go
func (s *Server) NewProxy() Executor {
    return metrics.NewMiddleware(&pipelineProxy{server: s}, s.collector, s.buffer, s.cfg.Tracer, s.cfg.Metrics)
}
```

Interface unchanged — still satisfies `Executor`. Backward compatible. CLI wraps the returned proxy again with its own collector (harmless — each collector feeds its own admin instance, no double display).

### `Shutdown(ctx context.Context) error`

```go
func (s *Server) Shutdown(ctx context.Context) error {
    if s.brokerCancel != nil {
        s.brokerCancel()
    }
    if s.adminSrv != nil {
        return s.adminSrv.Shutdown(ctx)
    }
    return nil
}
```

Caller invokes on process exit. No-op when `AdminPort == 0`.

---

## Files

| File | Change | Responsibility |
|------|--------|---------------|
| `server/admin.go` | **Create** | `startAdmin()` — builds `restapi.NewMux`, binds listener, returns `*http.Server` |
| `server/server.go` | **Modify** | Add 4 private fields, wire subsystems in `New()`, update `NewProxy()`, add `Shutdown()` |
| `server/server_test.go` | **Modify** | Add admin start/shutdown tests, update `NewProxy` test |

`cmd/mockwave/main.go` — **no change**. Passes `AdminPort: 0` implicitly (field zero value); its manual admin wiring is unaffected.

---

## `server/admin.go`

```go
package server

import (
    "context"
    "fmt"
    "net"
    "net/http"

    "github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
    "github.com/mockwave/mockwave/internal/metrics"
    "github.com/mockwave/mockwave/internal/unmatched"
)

// startAdmin binds an HTTP listener on cfg.AdminPort, builds the admin mux,
// and starts serving in a goroutine. Returns the http.Server for Shutdown.
func (s *Server) startAdmin() error {
    mux := restapi.NewMux(
        s.cfg.Store,
        func() { _ = s.Rebuild() },
        s.collector,
        s.buffer,
        s.broker,
        s.engine,
    )
    ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.cfg.AdminPort))
    if err != nil {
        return fmt.Errorf("server: admin listen :%d: %w", s.cfg.AdminPort, err)
    }
    srv := &http.Server{Handler: mux}
    s.adminSrv = srv
    go srv.Serve(ln) //nolint:errcheck
    return nil
}
```

Using `net.Listen` + `Serve` (instead of `ListenAndServe`) lets tests use port `:0` to get a random free port and inspect the bound address.

---

## Error handling

| Condition | Behaviour |
|-----------|-----------|
| `AdminPort > 0` and port already in use | `New()` returns error — `"server: admin listen :N: ..."` |
| `AdminPort == 0` | No listener started, `adminSrv` stays nil |
| `Shutdown()` called when `AdminPort == 0` | No-op, returns nil |
| `Shutdown()` called with expired context | Returns `context.DeadlineExceeded` from `http.Server.Shutdown` |

---

## Testing

```go
// Verify admin starts and serves /api/health when AdminPort > 0
func TestServer_AdminStartsWhenPortSet(t *testing.T) {
    srv, err := server.New(server.Config{Store: &stubStore{}, AdminPort: 0})
    // Use port 0 trick: find free port, pass it, verify response
}

// Verify Shutdown stops the admin listener
func TestServer_ShutdownStopsAdmin(t *testing.T) { ... }

// Verify NewProxy wraps with metrics (does not panic on nil fields)
func TestServer_NewProxy_IsNotNil(t *testing.T) { ... }
```

Tests bind on a random free port (`:0` via `net.Listen`, capture `Addr()`). No hardcoded ports.

---

## What does NOT change

- Public API surface (except adding `Shutdown`) — `MockHandler`, `GRPCServer`, `HTTPHandler`, `Logger`, `Tracer`, `MetricsRecorder` unchanged
- `server.Config` struct — `AdminPort` field already exists
- `cmd/mockwave/main.go` — CLI passes zero-value `AdminPort`, admin wiring unchanged
- Noop fallback pattern for `Logger`/`Tracer`/`Metrics` — unchanged

---

## Non-goals

- `AdminHandler() http.Handler` — not added (option B rejected)
- TLS for admin listener — caller's concern, out of scope
- Admin path prefix configuration — out of scope
- Exposing `Collector`, `Buffer`, or `Broker` as public types — not needed
