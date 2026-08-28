package metrics

import (
	"context"
	"time"

	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/internal/matched"
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
	next       Executor
	collector  *Collector
	buffer     *unmatched.Buffer
	tracer     observability.Tracer
	recorder   observability.MetricsRecorder
	matchedBuf *matched.Buffer // may be nil → capture disabled
	matchedTTL int             // seconds
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

// SetMatchedCapture enables matched-request capture into buf with the given
// global TTL in seconds. Pass a nil buf to leave capture disabled.
func (m *Middleware) SetMatchedCapture(buf *matched.Buffer, ttlSeconds int) {
	m.matchedBuf = buf
	m.matchedTTL = ttlSeconds
}

// captureMatched records a matched request into the buffer (best-effort).
func (m *Middleware) captureMatched(pctx *pipeline.PipelineContext) {
	if m.matchedBuf == nil || pctx.Matched == nil {
		return
	}
	now := time.Now()
	r := matched.Request{
		ID:       matched.NewID(),
		RuleID:   pctx.Matched.ID,
		At:       now,
		Protocol: pctx.Request.Protocol,
		Method:   pctx.Request.Method,
		Path:     pctx.Request.Path,
		Headers:  pctx.Request.Headers,
		Query:    pctx.Request.Query,
	}
	if m.matchedTTL > 0 {
		r.TTL = now.Add(time.Duration(m.matchedTTL) * time.Second).Unix()
	}
	var reqBody []byte
	if len(pctx.Request.Body) > 0 {
		r.RequestBodyID = matched.NewID()
		reqBody = pctx.Request.Body
	}
	var respBody interface{}
	if pctx.Response != nil {
		r.ResponseStatus = pctx.Response.Status
		r.ResponseHeaders = pctx.Response.Headers
		if pctx.Response.Body != nil {
			r.ResponseBodyID = matched.NewID()
			respBody = pctx.Response.Body
		}
	}
	m.matchedBuf.Add(r, reqBody, respBody)
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
	// Use microsecond precision so sub-millisecond handlers don't floor to 0ms.
	latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

	if pctx.Matched != nil {
		m.collector.RecordHit(pctx.Matched.ID, pctx.Matched.Name, latencyMs)
		m.recorder.RecordRequest(ctx, observability.RequestAttrs{
			Protocol:       pctx.Request.Protocol,
			Method:         pctx.Request.Method,
			Path:           pctx.Request.Path,
			RuleID:         pctx.Matched.ID,
			RuleName:       pctx.Matched.Name,
			LatencyMs:      latencyMs,
			FaultProfileID: pctx.FaultProfileID,
			FaultType:      pctx.FaultType,
		})
		m.captureMatched(pctx)
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
