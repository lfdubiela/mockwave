# Extending Mockwave

Mockwave exposes two public extension points so you can plug in your own backends and observability tooling when embedding the server as a Go library:

| Extension point | Package | What it controls |
|----------------|---------|-----------------|
| `store.DataStore` | `github.com/mockwave/mockwave/store` | Where rules and simulations are stored |
| `observability.Logger` | `github.com/mockwave/mockwave/observability` | Structured logging |
| `observability.Tracer` | `github.com/mockwave/mockwave/observability` | Distributed tracing |
| `observability.MetricsRecorder` | `github.com/mockwave/mockwave/observability` | Request metrics |

All four are Go interfaces. Implement any or all of them and pass them to `server.New` — Mockwave uses Noop defaults for anything you leave nil.

---

## Import

```bash
go get github.com/mockwave/mockwave
```

```go
import (
    "github.com/mockwave/mockwave/domain"
    "github.com/mockwave/mockwave/observability"
    "github.com/mockwave/mockwave/server"
    "github.com/mockwave/mockwave/store"
)
```

---

## Custom Store Backend

### Interface

```go
// store/store.go
type DataStore interface {
    GetRules() ([]domain.Rule, error)
    GetSimulation(id string) (*domain.Simulation, error)  // return (nil, nil) when not found
    ListSimulations() ([]domain.Simulation, error)
    SaveRule(r domain.Rule) error
    SaveSimulation(sim domain.Simulation) error
    DeleteRule(id string) error
    DeleteSimulation(id string) error
}
```

`GetSimulation` must return `(nil, nil)` — not an error — when the simulation does not exist. Returning an error for a missing ID is a contract violation.

### Optional capabilities

Beyond `DataStore`, a store can opt into extra behavior by implementing
additional interfaces. Mockwave type-asserts for them at runtime; stores that
don't implement them simply don't get the feature.

```go
// store/store.go

// VersionedStore enables the periodic version-poll reloader. Mockwave reads
// ConfigVersion cheaply each tick and reloads the pipeline only when the
// value changed. Return a marker that increases on every rule/simulation
// write; 0 when no marker exists yet.
type VersionedStore interface {
    ConfigVersion() (int64, error)
}

// FaultStore persists chaos fault profiles (see "Chaos Testing" in the
// README). Same contract style as DataStore: GetFaultProfile returns
// (nil, nil) when the profile does not exist. Stores without this capability
// make the /api/faults endpoints return 501, and rules referencing
// fault_profile_id are rejected by the admin API.
type FaultStore interface {
    ListFaultProfiles() ([]domain.FaultProfile, error)
    GetFaultProfile(id string) (*domain.FaultProfile, error)
    SaveFaultProfile(p domain.FaultProfile) error
    DeleteFaultProfile(id string) error
}

// ScenarioStore persists chaos scenarios (see "Scenarios" under "Chaos Testing"
// in the README). Same contract style as FaultStore: GetScenario returns
// (nil, nil) when the scenario does not exist. Stores without this capability
// make the /api/scenarios endpoints (and start/stop) return 501, and import
// payloads carrying scenarios are rejected with 422. Note that live scenario
// run state is held in-process, not in the store — only the scenario
// definitions are persisted here.
type ScenarioStore interface {
    ListScenarios() ([]domain.Scenario, error)
    GetScenario(id string) (*domain.Scenario, error)
    SaveScenario(s domain.Scenario) error
    DeleteScenario(id string) error
}
```

All built-in backends implement `FaultStore` and `ScenarioStore`: `json` (file), `dynamodb`, and `mongo` (with Cosmos inheriting the MongoDB implementation). All also implement `VersionedStore` except `json`.

### Example — Redis store

```go
import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/redis/go-redis/v9"
    "github.com/mockwave/mockwave/domain"
    "github.com/mockwave/mockwave/store"
)

type RedisStore struct {
    rdb *redis.Client
}

func NewRedisStore(addr string) *RedisStore {
    return &RedisStore{rdb: redis.NewClient(&redis.Options{Addr: addr})}
}

// Compile-time interface check.
var _ store.DataStore = (*RedisStore)(nil)

func (s *RedisStore) GetRules() ([]domain.Rule, error) {
    data, err := s.rdb.Get(context.Background(), "rules").Bytes()
    if err == redis.Nil {
        return nil, nil
    }
    if err != nil {
        return nil, fmt.Errorf("redis get rules: %w", err)
    }
    var rules []domain.Rule
    return rules, json.Unmarshal(data, &rules)
}

func (s *RedisStore) GetSimulation(id string) (*domain.Simulation, error) {
    data, err := s.rdb.Get(context.Background(), "sim:"+id).Bytes()
    if err == redis.Nil {
        return nil, nil // not found — not an error
    }
    if err != nil {
        return nil, fmt.Errorf("redis get simulation %s: %w", id, err)
    }
    var sim domain.Simulation
    return &sim, json.Unmarshal(data, &sim)
}

func (s *RedisStore) ListSimulations() ([]domain.Simulation, error) {
    keys, err := s.rdb.Keys(context.Background(), "sim:*").Result()
    if err != nil {
        return nil, fmt.Errorf("redis list simulations: %w", err)
    }
    sims := make([]domain.Simulation, 0, len(keys))
    for _, k := range keys {
        data, err := s.rdb.Get(context.Background(), k).Bytes()
        if err != nil {
            continue
        }
        var sim domain.Simulation
        if json.Unmarshal(data, &sim) == nil {
            sims = append(sims, sim)
        }
    }
    return sims, nil
}

func (s *RedisStore) SaveRule(r domain.Rule) error {
    rules, _ := s.GetRules()
    for i, existing := range rules {
        if existing.ID == r.ID {
            rules[i] = r
            return s.saveRules(rules)
        }
    }
    return s.saveRules(append(rules, r))
}

func (s *RedisStore) SaveSimulation(sim domain.Simulation) error {
    data, err := json.Marshal(sim)
    if err != nil {
        return err
    }
    return s.rdb.Set(context.Background(), "sim:"+sim.ID, data, 0).Err()
}

func (s *RedisStore) DeleteRule(id string) error {
    rules, err := s.GetRules()
    if err != nil {
        return err
    }
    filtered := rules[:0]
    for _, r := range rules {
        if r.ID != id {
            filtered = append(filtered, r)
        }
    }
    return s.saveRules(filtered)
}

func (s *RedisStore) DeleteSimulation(id string) error {
    return s.rdb.Del(context.Background(), "sim:"+id).Err()
}

func (s *RedisStore) saveRules(rules []domain.Rule) error {
    data, err := json.Marshal(rules)
    if err != nil {
        return err
    }
    return s.rdb.Set(context.Background(), "rules", data, 0).Err()
}
```

### Wiring

Pass your store explicitly:

```go
myStore := NewRedisStore("localhost:6379")

srv, err := server.New(server.Config{
    Store: myStore,
})
```

Or omit `Store` entirely and configure via environment variables. When `Config.Store` is nil, `server.New` reads `MOCKWAVE_STORE` (default `json`) and constructs the appropriate backend:

| Variable | Default | Backend |
|----------|---------|---------|
| `MOCKWAVE_STORE` | `json` | all — selects backend |
| `MOCKWAVE_CONFIG` | — | `json` — path to config file (required) |
| `MOCKWAVE_DYNAMO_RULES_TABLE` | `mockwave-rules` | `dynamodb` |
| `MOCKWAVE_DYNAMO_SIMS_TABLE` | `mockwave-simulations` | `dynamodb` |
| `MOCKWAVE_DYNAMO_REGION` | `us-east-1` | `dynamodb` |
| `MOCKWAVE_DYNAMO_ENDPOINT` | `""` | `dynamodb` — empty = AWS default |
| `MOCKWAVE_MONGO_URI` | `mongodb://localhost:27017` | `mongo` |
| `MOCKWAVE_MONGO_DB` | `mockwave` | `mongo` |
| `MOCKWAVE_COSMOS_URI` | — | `cosmos` — required |
| `MOCKWAVE_COSMOS_DB` | `mockwave` | `cosmos` |

```go
// No Store in Config — server reads MOCKWAVE_STORE from environment.
// myLogger, myTracer, myRecorder are your own observability implementations
// (see "Custom Observability" section below).
srv, err := server.New(server.Config{
    Logger:  myLogger,
    Tracer:  myTracer,
    Metrics: myRecorder,
})
```

---

## Custom Observability

Mockwave's `observability` package defines three interfaces. Every method accepts `context.Context` as its first parameter. Implementations can pull request-scoped fields (request ID, method, path, protocol, trace ID) from the context using `observability.FromContext`.

### Interfaces

```go
type Logger interface {
    Debug(ctx context.Context, msg string, fields ...Field)
    Info(ctx context.Context, msg string, fields ...Field)
    Warn(ctx context.Context, msg string, fields ...Field)
    Error(ctx context.Context, msg string, err error, fields ...Field)
}

type Tracer interface {
    Start(ctx context.Context, spanName string, attrs ...Attr) (context.Context, Span)
}

type Span interface {
    End()
    SetError(err error)
    SetAttr(key string, value any)
}

type MetricsRecorder interface {
    RecordRequest(ctx context.Context, attrs RequestAttrs)
    RecordUnmatched(ctx context.Context, method, path, protocol string)
}

type RequestAttrs struct {
    Protocol  string
    Method    string
    Path      string
    RuleID    string
    RuleName  string
    LatencyMs float64
}
```

Convenience constructors: `observability.F(key, value)` builds a `Field`; `observability.A(key, value)` builds an `Attr`.

### Context helpers

Mockwave stamps request fields into the context automatically. Read them anywhere:

```go
ri := observability.FromContext(ctx)
// ri.RequestID  — random 16-char hex, unique per request
// ri.Method     — e.g. "GET"
// ri.Path       — e.g. "/orders/42"
// ri.Protocol   — "http" | "grpc" | "graphql" | "soap"
// ri.TraceID    — set by your Tracer implementation, empty otherwise
```

Use `observability.StampRequest` at your own service boundaries:

```go
ctx = observability.StampRequest(r.Context(), r.Method, r.URL.Path, "http")
```

`StampRequest` is idempotent — safe to call multiple times on the same context.

---

### Custom Logger — zerolog

```go
import (
    "context"

    "github.com/rs/zerolog"
    "github.com/mockwave/mockwave/observability"
)

type ZerologLogger struct {
    log zerolog.Logger
}

// Compile-time interface check.
var _ observability.Logger = ZerologLogger{}

func (l ZerologLogger) Debug(ctx context.Context, msg string, fields ...observability.Field) {
    l.event(ctx, l.log.Debug(), msg, fields)
}
func (l ZerologLogger) Info(ctx context.Context, msg string, fields ...observability.Field) {
    l.event(ctx, l.log.Info(), msg, fields)
}
func (l ZerologLogger) Warn(ctx context.Context, msg string, fields ...observability.Field) {
    l.event(ctx, l.log.Warn(), msg, fields)
}
func (l ZerologLogger) Error(ctx context.Context, msg string, err error, fields ...observability.Field) {
    l.event(ctx, l.log.Error().Err(err), msg, fields)
}

func (l ZerologLogger) event(ctx context.Context, e *zerolog.Event, msg string, fields []observability.Field) {
    ri := observability.FromContext(ctx)
    if ri.RequestID != "" { e = e.Str("request_id", ri.RequestID) }
    if ri.Method != ""    { e = e.Str("method", ri.Method) }
    if ri.Path != ""      { e = e.Str("path", ri.Path) }
    if ri.Protocol != ""  { e = e.Str("protocol", ri.Protocol) }
    if ri.TraceID != ""   { e = e.Str("trace_id", ri.TraceID) }
    for _, f := range fields {
        e = e.Interface(f.Key, f.Value)
    }
    e.Msg(msg)
}
```

---

### Custom Tracer — OpenTelemetry

```go
import (
    "context"
    "fmt"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/codes"
    oteltrace "go.opentelemetry.io/otel/trace"
    "github.com/mockwave/mockwave/observability"
)

type OTelTracer struct {
    tracer oteltrace.Tracer
}

func NewOTelTracer(name string) OTelTracer {
    return OTelTracer{tracer: otel.Tracer(name)}
}

// Compile-time interface check.
var _ observability.Tracer = OTelTracer{}

func (t OTelTracer) Start(ctx context.Context, spanName string, attrs ...observability.Attr) (context.Context, observability.Span) {
    otelAttrs := make([]attribute.KeyValue, len(attrs))
    for i, a := range attrs {
        otelAttrs[i] = attribute.String(a.Key, fmt.Sprintf("%v", a.Value))
    }
    ctx, span := t.tracer.Start(ctx, spanName, oteltrace.WithAttributes(otelAttrs...))
    // Stamp the trace ID into context so Logger implementations pick it up.
    ctx = observability.StampTraceID(ctx, span.SpanContext().TraceID().String())
    return ctx, &otelSpan{span: span}
}

type otelSpan struct{ span oteltrace.Span }

func (s *otelSpan) End() { s.span.End() }
func (s *otelSpan) SetError(err error) {
    s.span.RecordError(err)
    s.span.SetStatus(codes.Error, err.Error())
}
func (s *otelSpan) SetAttr(key string, value any) {
    s.span.SetAttributes(attribute.String(key, fmt.Sprintf("%v", value)))
}
```

---

### Custom MetricsRecorder — Prometheus

```go
import (
    "context"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
    "github.com/mockwave/mockwave/observability"
)

type PrometheusRecorder struct {
    requestDuration *prometheus.HistogramVec
    unmatchedTotal  prometheus.Counter
}

func NewPrometheusRecorder() *PrometheusRecorder {
    return &PrometheusRecorder{
        requestDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
            Name:    "mockwave_request_duration_ms",
            Help:    "Request latency by rule",
            Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500},
        }, []string{"protocol", "method", "rule_id"}),
        unmatchedTotal: promauto.NewCounter(prometheus.CounterOpts{
            Name: "mockwave_unmatched_requests_total",
            Help: "Requests that matched no rule",
        }),
    }
}

// Compile-time interface check.
var _ observability.MetricsRecorder = (*PrometheusRecorder)(nil)

func (r *PrometheusRecorder) RecordRequest(ctx context.Context, attrs observability.RequestAttrs) {
    r.requestDuration.
        WithLabelValues(attrs.Protocol, attrs.Method, attrs.RuleID).
        Observe(attrs.LatencyMs)
}

func (r *PrometheusRecorder) RecordUnmatched(ctx context.Context, method, path, protocol string) {
    r.unmatchedTotal.Inc()
}
```

---

## Wiring everything together

Pass all your implementations to `server.New`. Any field left nil defaults to the matching Noop implementation.

```go
import (
    "context"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/mockwave/mockwave/server"
)

func main() {
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

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    proxy := srv.NewProxy()  // pre-wrapped with metrics; feeds the admin dashboard
    go http.ListenAndServe(":8080", srv.MockHandler([]string{"http"}, proxy))

    <-ctx.Done()
    shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    srv.Shutdown(shutCtx)
}
```

When `AdminPort > 0`, `server.New` binds the admin HTTP server on that port automatically — no extra wiring required. The admin dashboard is available at `http://localhost:9090`.

`NewProxy()` includes metrics middleware automatically: every request through `MockHandler` feeds `/api/metrics` and the dashboard's live stream.

Call `srv.Shutdown(ctx)` on exit to drain in-flight admin requests and stop background goroutines. When `AdminPort` is 0, `Shutdown` is a no-op.

---

## Using the Logger in your own code

The Logger and context helpers work standalone — no Mockwave server required:

```go
logger := observability.NewSlogLogger()

// Stamp context at your service boundary
ctx := observability.StampRequest(r.Context(), r.Method, r.URL.Path, "http")

// Use anywhere — context fields added automatically
logger.Info(ctx, "processing order", observability.F("order_id", orderID))
logger.Error(ctx, "payment failed", err, observability.F("amount", total))
```

Output includes `request_id`, `method`, `path`, `protocol`, and `trace_id` whenever they are present in the context:

```json
{"time":"2026-06-01T12:00:00Z","level":"INFO","msg":"processing order",
 "request_id":"a3f2b1c0e4d56789","method":"POST","path":"/orders","protocol":"http",
 "order_id":"ord_99"}
```
