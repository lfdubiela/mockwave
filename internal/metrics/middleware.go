package metrics

import (
	"context"
	"time"

	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/internal/unmatched"
	"github.com/mockwave/mockwave/observability"
)

// Executor is implemented by server.pipelineProxy and any wrapping middleware.
// It matches the interface defined in each protocol handler package.
type Executor interface {
	Execute(ctx context.Context, pctx *pipeline.PipelineContext) error
}

// Middleware wraps an Executor to record metrics, capture unmatched requests,
// and emit observability signals (traces + external metrics).
type Middleware struct {
	next      Executor
	collector *Collector
	buffer    *unmatched.Buffer
	tracer    observability.Tracer
	recorder  observability.MetricsRecorder
}

// NewMiddleware creates a Middleware. tracer and recorder must not be nil;
// pass observability.NoopTracer{} and observability.NoopMetrics{} to disable.
func NewMiddleware(
	next Executor,
	col *Collector,
	buf *unmatched.Buffer,
	tracer observability.Tracer,
	recorder observability.MetricsRecorder,
) *Middleware {
	return &Middleware{
		next:      next,
		collector: col,
		buffer:    buf,
		tracer:    tracer,
		recorder:  recorder,
	}
}

// Execute runs the wrapped pipeline and records the outcome.
func (m *Middleware) Execute(ctx context.Context, pctx *pipeline.PipelineContext) error {
	// Ensure the context has request-scoped fields even when the calling adapter
	// (gRPC, GraphQL, SOAP) hasn't stamped them. StampRequest is idempotent —
	// HTTP requests already stamped are passed through unchanged.
	ctx = observability.StampRequest(ctx, pctx.Request.Method, pctx.Request.Path, pctx.Request.Protocol)

	ctx, span := m.tracer.Start(ctx, "pipeline.Execute",
		observability.A("method", pctx.Request.Method),
		observability.A("path", pctx.Request.Path),
		observability.A("protocol", pctx.Request.Protocol),
	)
	defer span.End()

	start := time.Now()
	err := m.next.Execute(ctx, pctx)
	if err != nil {
		span.SetError(err)
	}
	latencyMs := float64(time.Since(start).Milliseconds())

	if pctx.Matched != nil {
		m.collector.RecordHit(pctx.Matched.ID, pctx.Matched.Name, latencyMs)
		m.recorder.RecordRequest(ctx, observability.RequestAttrs{
			Protocol:  pctx.Request.Protocol,
			Method:    pctx.Request.Method,
			Path:      pctx.Request.Path,
			RuleID:    pctx.Matched.ID,
			RuleName:  pctx.Matched.Name,
			LatencyMs: latencyMs,
		})
	} else {
		m.collector.RecordMiss()
		m.recorder.RecordUnmatched(ctx, pctx.Request.Method, pctx.Request.Path, pctx.Request.Protocol)
		m.buffer.Add(unmatched.Request{
			At:       time.Now(),
			Protocol: pctx.Request.Protocol,
			Method:   pctx.Request.Method,
			Path:     pctx.Request.Path,
			Headers:  pctx.Request.Headers,
			Body:     string(pctx.Request.Body),
		})
	}
	return err
}
