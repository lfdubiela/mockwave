# Admin API Public Exposure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `server.New()` auto-start the admin HTTP server when `AdminPort > 0`, so library embedders get a working admin dashboard without any extra wiring — and simplify the CLI to use the same library path.

**Architecture:** `Server` gains four private fields (`collector`, `buffer`, `broker`, `brokerCancel`) always initialized in `New()`. `NewProxy()` wraps the pipeline with `metrics.Middleware` so mock traffic feeds the dashboard automatically. When `AdminPort > 0`, `New()` binds the admin listener via `startAdmin()` in a new `server/admin.go`. A new `Shutdown(ctx)` drains the admin listener and cancels the broker. The CLI is simplified to remove its manual admin wiring — it just passes `AdminPort: adminPort` and calls `srv.NewProxy()`.

**Tech Stack:** Go 1.26, `internal/metrics`, `internal/unmatched`, `internal/adapters/cfg/restapi`, `net/http`, testify.

---

## File Structure

| File | Change | Responsibility |
|------|--------|---------------|
| `server/server.go` | **Modify** | Add 4 private fields, initialize in `New()`, update `NewProxy()`, add `Shutdown()` |
| `server/admin.go` | **Create** | `startAdmin()` — binds listener, builds `restapi.NewMux`, stores `*http.Server` |
| `server/server_test.go` | **Modify** | Add admin start/shutdown tests |
| `cmd/mockwave/main.go` | **Modify** | Remove manual admin wiring; use library auto-start + `NewProxy()` |
| `docs/extending.md` | **Modify** | Document `AdminPort` auto-start + `Shutdown()` |

---

### Task 1: Wire internal subsystems into Server and update NewProxy()

**Files:**
- Modify: `server/server.go`
- Test: `server/server_test.go`

**Context:**

Current `server.go` struct:

```go
type Server struct {
    cfg      Config
    mu       sync.RWMutex
    pipeline *pipeline.Pipeline
    engine   *scripting.Engine
}
```

Current `NewProxy()`:
```go
func (s *Server) NewProxy() Executor {
    return &pipelineProxy{server: s}
}
```

New internal package paths needed:
- `"github.com/mockwave/mockwave/internal/metrics"` — `metrics.NewCollector()`, `metrics.NewBroker(col)`, `metrics.NewMiddleware(next, col, buf, tracer, recorder)`
- `"github.com/mockwave/mockwave/internal/unmatched"` — `unmatched.NewBuffer(100)`

`metrics.NewMiddleware` full signature:
```go
func NewMiddleware(
    next Executor,
    col *Collector,
    buf *unmatched.Buffer,
    tracer observability.Tracer,
    recorder observability.MetricsRecorder,
) *Middleware
```

- [ ] **Step 1: Write the failing test**

Add to `server/server_test.go` (before the last `}`):

```go
func TestServer_NewProxy_Execute_NoError(t *testing.T) {
	srv, err := server.New(server.Config{Store: newStubStore()})
	require.NoError(t, err)
	proxy := srv.NewProxy()
	require.NotNil(t, proxy)
	// Verify compile-time satisfaction of Executor interface.
	var _ server.Executor = proxy
}
```

- [ ] **Step 2: Run test — expect PASS (interface check only)**

```bash
go test ./server/... -run "TestServer_NewProxy_Execute_NoError" -v
```

Expected: PASS (test compiles and passes; verifies interface satisfaction).

- [ ] **Step 3: Update Server struct in `server/server.go`**

Add two new imports inside the existing import block:

```go
"github.com/mockwave/mockwave/internal/metrics"
"github.com/mockwave/mockwave/internal/unmatched"
```

Replace the `Server` struct definition:

```go
// Server holds the active pipeline and serves mock traffic across protocols.
type Server struct {
	cfg          Config
	mu           sync.RWMutex
	pipeline     *pipeline.Pipeline
	engine       *scripting.Engine
	collector    *metrics.Collector
	buffer       *unmatched.Buffer
	broker       *metrics.Broker
	brokerCancel context.CancelFunc
	adminSrv     *http.Server
}
```

- [ ] **Step 4: Initialize subsystems in `New()` in `server/server.go`**

Find and replace this block at the end of `New()`:

```go
	s := &Server{cfg: cfg, engine: scripting.NewEngine()}
	if err := s.rebuild(); err != nil {
		return nil, err
	}
	return s, nil
```

Replace with:

```go
	col := metrics.NewCollector()
	buf := unmatched.NewBuffer(100)
	broker := metrics.NewBroker(col)
	brokerCtx, brokerCancel := context.WithCancel(context.Background())
	go broker.Start(brokerCtx)

	s := &Server{
		cfg:          cfg,
		engine:       scripting.NewEngine(),
		collector:    col,
		buffer:       buf,
		broker:       broker,
		brokerCancel: brokerCancel,
	}
	if err := s.rebuild(); err != nil {
		brokerCancel()
		return nil, err
	}
	return s, nil
```

- [ ] **Step 5: Update `NewProxy()` in `server/server.go`**

Replace:

```go
// NewProxy returns an Executor backed by this server's active pipeline.
// Wrap it with middleware before passing to MockHandler or GRPCServer.
func (s *Server) NewProxy() Executor {
	return &pipelineProxy{server: s}
}
```

With:

```go
// NewProxy returns an Executor backed by this server's active pipeline,
// pre-wrapped with metrics middleware. Every request through MockHandler
// or GRPCServer automatically feeds the admin dashboard.
func (s *Server) NewProxy() Executor {
	return metrics.NewMiddleware(
		&pipelineProxy{server: s},
		s.collector,
		s.buffer,
		s.cfg.Tracer,
		s.cfg.Metrics,
	)
}
```

- [ ] **Step 6: Run full server test suite**

```bash
go test ./server/... -v
```

Expected: all tests pass.

- [ ] **Step 7: Verify full build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add server/server.go server/server_test.go
git commit -m "feat: wire metrics subsystems into Server and wrap NewProxy with middleware"
```

---

### Task 2: Create `server/admin.go`, add `Shutdown()`, auto-start in `New()`

**Files:**
- Create: `server/admin.go`
- Modify: `server/server.go`
- Test: `server/server_test.go`

**Context:**

`restapi.NewMux` signature (from `internal/adapters/cfg/restapi/server.go`):
```go
func NewMux(
    store store.DataStore,
    onReload OnReload,
    collector *metrics.Collector,
    buffer *unmatched.Buffer,
    broker *metrics.Broker,
    engine *scripting.Engine,
) *http.ServeMux
```

`freePort` helper for tests: listen on `:0`, capture OS-assigned port, close immediately. Tiny race window is acceptable in tests.

- [ ] **Step 1: Write the failing tests**

Add to `server/server_test.go`. Add to the import block: `"context"`, `"fmt"`, `"net"`, `"time"` (if not already present).

```go
// freePort returns a TCP port that is free at the moment of the call.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

func TestServer_AdminStartsWhenPortSet(t *testing.T) {
	port := freePort(t)
	srv, err := server.New(server.Config{Store: newStubStore(), AdminPort: port})
	require.NoError(t, err)
	require.NotNil(t, srv)

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/health", port))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assert.NoError(t, srv.Shutdown(ctx))
}

func TestServer_AdminPortAlreadyInUse_ReturnsError(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	_, err = server.New(server.Config{Store: newStubStore(), AdminPort: port})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin listen")
}

func TestServer_ShutdownStopsAdmin(t *testing.T) {
	port := freePort(t)
	srv, err := server.New(server.Config{Store: newStubStore(), AdminPort: port})
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))

	_, err = http.Get(fmt.Sprintf("http://localhost:%d/api/health", port))
	assert.Error(t, err)
}

func TestServer_ShutdownNoopWhenAdminPortZero(t *testing.T) {
	srv, err := server.New(server.Config{Store: newStubStore()})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	assert.NoError(t, srv.Shutdown(ctx))
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./server/... -run "TestServer_Admin|TestServer_Shutdown" -v
```

Expected: FAIL — `AdminPort` ignored; `Shutdown` undefined.

- [ ] **Step 3: Create `server/admin.go`**

```go
package server

import (
	"context"
	"fmt"
	"net"
	"net/http"

	restapi "github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
)

// startAdmin binds an HTTP listener on cfg.AdminPort, builds the admin mux,
// and serves in a goroutine. Stores the *http.Server in s.adminSrv for Shutdown.
// Returns an error if the port cannot be bound.
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

// Shutdown gracefully stops the admin HTTP server and cancels background goroutines.
// Safe to call when AdminPort was 0 (no-op).
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

- [ ] **Step 4: Wire `startAdmin()` into `New()` in `server/server.go`**

Find:

```go
	if err := s.rebuild(); err != nil {
		brokerCancel()
		return nil, err
	}
	return s, nil
```

Replace with:

```go
	if err := s.rebuild(); err != nil {
		brokerCancel()
		return nil, err
	}
	if cfg.AdminPort > 0 {
		if err := s.startAdmin(); err != nil {
			brokerCancel()
			return nil, err
		}
	}
	return s, nil
```

- [ ] **Step 5: Run the new tests — expect all 4 to pass**

```bash
go test ./server/... -run "TestServer_Admin|TestServer_Shutdown" -v
```

Expected: all 4 PASS.

- [ ] **Step 6: Run full server test suite**

```bash
go test ./server/... -v
```

Expected: all tests pass.

- [ ] **Step 7: Commit**

```bash
git add server/admin.go server/server.go server/server_test.go
git commit -m "feat: auto-start admin server when AdminPort > 0; add Shutdown()"
```

---

### Task 3: Simplify CLI to use library auto-start

**Files:**
- Modify: `cmd/mockwave/main.go`

**Context:**

The CLI currently creates `col`, `buf`, `broker`, `evalEngine` manually, wraps proxy with `metrics.NewMiddleware`, and starts admin with `http.ListenAndServe`. All of this is now handled by the library. After this task:

- `srv.NewProxy()` returns a metrics-wrapped executor — use directly, no manual wrapping
- Admin starts automatically when `AdminPort: adminPort` is passed to `server.Config`
- `srv.Shutdown(ctx)` is called on process exit

These imports are no longer needed and must be removed from `cmd/mockwave/main.go`:
- `"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"`
- `"github.com/mockwave/mockwave/internal/metrics"`
- `"github.com/mockwave/mockwave/internal/scripting"`
- `"github.com/mockwave/mockwave/internal/unmatched"`

The `"context"` import stays (used for gRPC and Shutdown).

- [ ] **Step 1: Rewrite the `RunE` body in `startCmd()` in `cmd/mockwave/main.go`**

Replace the entire `RunE` function body (lines 54–113) with:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    s, err := buildStore(storeType, configFile, opts)
    if err != nil {
        return fmt.Errorf("init store: %w", err)
    }
    srv, err := server.New(server.Config{
        MockPort:  mockPort,
        AdminPort: adminPort,
        Store:     s,
    })
    if err != nil {
        return err
    }

    ctx, stop := context.WithCancel(context.Background())
    defer stop()
    defer srv.Shutdown(ctx) //nolint:errcheck

    proxy := srv.NewProxy() // metrics-wrapped; feeds admin dashboard automatically
    protocols := splitProtocols(protocolsStr)

    if containsProtocol(protocols, "grpc") {
        var registry *grpcadapter.FileRegistry
        if grpcProto != "" {
            registry, err = grpcadapter.LoadDescriptor(grpcProto)
            if err != nil {
                return fmt.Errorf("load grpc proto descriptor: %w", err)
            }
        }
        grpcSrv := srv.GRPCServer(registry, proxy)
        lis, err := net.Listen("tcp", fmt.Sprintf(":%d", grpcPort))
        if err != nil {
            return fmt.Errorf("grpc listen: %w", err)
        }
        go func() {
            log.Printf("gRPC server listening on :%d", grpcPort)
            if err := grpcSrv.Serve(lis); err != nil {
                log.Fatalf("grpc server: %v", err)
            }
        }()
    }

    log.Printf("mock server listening on :%d (protocols: %s, store: %s)", mockPort, protocolsStr, storeType)
    log.Printf("admin API listening on :%d", adminPort)
    return http.ListenAndServe(fmt.Sprintf(":%d", mockPort), srv.MockHandler(protocols, proxy))
},
```

- [ ] **Step 2: Update imports in `cmd/mockwave/main.go`**

Remove these four imports:
```go
"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
"github.com/mockwave/mockwave/internal/metrics"
"github.com/mockwave/mockwave/internal/scripting"
"github.com/mockwave/mockwave/internal/unmatched"
```

The remaining import block should be:
```go
import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net"
    "net/http"
    "os"
    "strings"

    grpcadapter "github.com/mockwave/mockwave/internal/adapters/in/grpc"
    cosmos "github.com/mockwave/mockwave/internal/adapters/out/cosmos"
    dynamostore "github.com/mockwave/mockwave/internal/adapters/out/dynamodb"
    "github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
    mongodb "github.com/mockwave/mockwave/internal/adapters/out/mongodb"
    "github.com/mockwave/mockwave/server"
    "github.com/mockwave/mockwave/store"
    "github.com/spf13/cobra"
)
```

- [ ] **Step 3: Build to verify no unused imports and no compilation errors**

```bash
go build ./cmd/mockwave/...
```

Expected: compiles cleanly, no unused import errors.

- [ ] **Step 4: Run full test suite**

```bash
go test ./...
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/mockwave/main.go
git commit -m "refactor: simplify CLI — remove manual admin wiring, use server.New auto-start"
```

---

### Task 4: Update `docs/extending.md`

**Files:**
- Modify: `docs/extending.md`

**Goal:** Document that `AdminPort > 0` auto-starts admin, show `Shutdown()` in the wiring example.

- [ ] **Step 1: Find the `## Wiring everything together` section in `docs/extending.md`**

The current `func main()` code block reads:

```go
func main() {
    myStore    := NewRedisStore("localhost:6379")
    myLogger   := ZerologLogger{log: zerolog.New(os.Stdout)}
    myTracer   := NewOTelTracer("mockwave")
    myRecorder := NewPrometheusRecorder()

    srv, err := server.New(server.Config{
        MockPort:  8080,
        AdminPort: 9090,
        Store:     myStore,
        Logger:    myLogger,
        Tracer:    myTracer,
        Metrics:   myRecorder,
    })
    if err != nil {
        log.Fatal(err)
    }

    proxy := srv.NewProxy()
    http.ListenAndServe(":8080", srv.MockHandler([]string{"http"}, proxy))
}
```

And the paragraph immediately after:

```
`Tracer` and `MetricsRecorder` are applied automatically to every request. `Logger` is available for your own code via the context helpers; integration with Mockwave's internal request pipeline is planned for a future release.
```

- [ ] **Step 2: Replace the code block and paragraph**

Replace the `func main()` block and the paragraph below it with:

````markdown
```go
import (
    "context"
    "log"
    "net/http"
    "os"
    "os/signal"

    "github.com/mockwave/mockwave/server"
)

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    myStore    := NewRedisStore("localhost:6379")
    myLogger   := ZerologLogger{log: zerolog.New(os.Stdout)}
    myTracer   := NewOTelTracer("mockwave")
    myRecorder := NewPrometheusRecorder()

    srv, err := server.New(server.Config{
        MockPort:  8080,
        AdminPort: 9090,   // admin API + dashboard start automatically on :9090
        Store:     myStore,
        Logger:    myLogger,
        Tracer:    myTracer,
        Metrics:   myRecorder,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer srv.Shutdown(ctx)

    proxy := srv.NewProxy()  // pre-wrapped with metrics; feeds the admin dashboard
    http.ListenAndServe(":8080", srv.MockHandler([]string{"http"}, proxy))
}
```

When `AdminPort > 0`, `server.New` binds the admin HTTP server on that port automatically — no extra wiring required. The admin dashboard is available at `http://localhost:9090`.

`NewProxy()` includes metrics middleware automatically: every request through `MockHandler` feeds `/api/metrics` and the dashboard's live stream.

Call `srv.Shutdown(ctx)` on exit to drain in-flight admin requests and stop background goroutines. When `AdminPort` is 0, `Shutdown` is a no-op.
````

- [ ] **Step 3: Verify the section**

```bash
grep -A 50 "## Wiring everything together" docs/extending.md
```

Expected: updated `main()` with `signal.NotifyContext`, `defer srv.Shutdown(ctx)`, `proxy := srv.NewProxy()`, and explanatory paragraph.

- [ ] **Step 4: Run full test suite**

```bash
go test ./...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add docs/extending.md
git commit -m "docs: document AdminPort auto-start and Shutdown in extending guide"
```
