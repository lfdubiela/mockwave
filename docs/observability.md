# Observability

Mockwave ships a small `observability` package at `github.com/mockwave/mockwave/observability`.

It defines three interfaces — **Logger**, **Tracer**, **MetricsRecorder** — plus context helpers that carry request-scoped fields (request ID, method, path, protocol) through the call stack.

Every interface method accepts `context.Context` as its first parameter. Implementations pull request fields out of the context automatically, so callers never build log lines by hand.

---

## Import

```bash
go get github.com/mockwave/mockwave
```

```go
import "github.com/mockwave/mockwave/observability"
```

---

## Interfaces

### Logger

```go
type Logger interface {
    Debug(ctx context.Context, msg string, fields ...Field)
    Info(ctx context.Context, msg string, fields ...Field)
    Warn(ctx context.Context, msg string, fields ...Field)
    Error(ctx context.Context, msg string, err error, fields ...Field)
}

type Field struct {
    Key   string
    Value any
}

func F(key string, value any) Field // convenience constructor
```

### Tracer

```go
type Tracer interface {
    Start(ctx context.Context, spanName string, attrs ...Attr) (context.Context, Span)
}

type Span interface {
    End()
    SetError(err error)
    SetAttr(key string, value any)
}

type Attr struct{ Key string; Value any }

func A(key string, value any) Attr // convenience constructor
```

### MetricsRecorder

```go
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

---

## Context helpers

At every request entry point, Mockwave stamps four fields into the context:

```go
// Stamp (called automatically by HTTP adapter and metrics middleware)
ctx = observability.StampRequest(ctx, method, path, protocol)

// Read anywhere downstream
ri := observability.FromContext(ctx)
// ri.RequestID  — random 16-char hex, unique per request
// ri.Method     — e.g. "GET"
// ri.Path       — e.g. "/orders/42"
// ri.Protocol   — "http" | "grpc" | "graphql" | "soap"
// ri.TraceID    — set by Tracer implementation, empty by default
```

`StampRequest` is idempotent: calling it again on an already-stamped context is a no-op.

---

## Defaults

Two ready-made implementations are included.

### SlogLogger — structured JSON to stdout

```go
// Default: JSON to stdout, DEBUG level
logger := observability.NewSlogLogger()

// Custom handler (e.g. to write to a file or capture in tests)
logger := observability.NewSlogLoggerWith(mySlogHandler)
```

Output automatically includes `request_id`, `method`, `path`, `protocol`, and `trace_id` whenever they are present in the context:

```json
{"time":"2026-06-01T12:00:00Z","level":"INFO","msg":"rule matched",
 "request_id":"a3f2b1c0e4d56789","method":"POST","path":"/orders","protocol":"http",
 "rule_id":"r1","latency_ms":3}
```

### Noop implementations — silence everything

```go
observability.NoopLogger{}
observability.NoopTracer{}
observability.NoopSpan{}
observability.NoopMetrics{}
```

These satisfy all interfaces and discard all inputs. Use them as defaults when you don't need instrumentation.

---

## Custom implementations

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
    e := l.log.Error().Err(err)
    l.event(ctx, e, msg, fields)
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

// Compile-time check
var _ observability.Logger = ZerologLogger{}
```

### Custom Tracer — OpenTelemetry

```go
import (
    "context"
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    oteltrace "go.opentelemetry.io/otel/trace"
    "github.com/mockwave/mockwave/observability"
)

type OTelTracer struct {
    tracer oteltrace.Tracer
}

func NewOTelTracer(name string) OTelTracer {
    return OTelTracer{tracer: otel.Tracer(name)}
}

func (t OTelTracer) Start(ctx context.Context, spanName string, attrs ...observability.Attr) (context.Context, observability.Span) {
    otelAttrs := make([]attribute.KeyValue, len(attrs))
    for i, a := range attrs {
        otelAttrs[i] = attribute.String(a.Key, fmt.Sprintf("%v", a.Value))
    }
    ctx, span := t.tracer.Start(ctx, spanName, oteltrace.WithAttributes(otelAttrs...))
    // Stamp the trace ID into context so SlogLogger picks it up
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

var _ observability.Tracer = OTelTracer{}
```

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

func (r *PrometheusRecorder) RecordRequest(ctx context.Context, attrs observability.RequestAttrs) {
    r.requestDuration.
        WithLabelValues(attrs.Protocol, attrs.Method, attrs.RuleID).
        Observe(attrs.LatencyMs)
}

func (r *PrometheusRecorder) RecordUnmatched(ctx context.Context, method, path, protocol string) {
    r.unmatchedTotal.Inc()
}

var _ observability.MetricsRecorder = (*PrometheusRecorder)(nil)
```

---

## Wiring custom implementations

When using Mockwave as a Go library, pass your implementations through `server.Config`:

```go
import (
    "github.com/mockwave/mockwave/server"
    "github.com/mockwave/mockwave/observability"
)

myLogger   := ZerologLogger{log: zerolog.New(os.Stdout)}
myTracer   := NewOTelTracer("mockwave")
myRecorder := NewPrometheusRecorder()

srv, _ := server.New(server.Config{
    Store:   myStore,
    Logger:  myLogger,
    Tracer:  myTracer,
    Metrics: myRecorder,
})

proxy := srv.NewProxy()

// Use proxy as the executor for MockHandler / GRPCServer
http.ListenAndServe(":8080", srv.MockHandler([]string{"http"}, proxy))
```

The `Tracer` and `MetricsRecorder` are applied automatically to every request that flows through the pipeline. Any nil field defaults to the matching Noop implementation.

> **Note:** The `Logger` interface is wired for use in your own code.
> Passing a Logger into Mockwave's internal request pipeline is planned for a future release.

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
