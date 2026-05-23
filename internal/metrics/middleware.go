package metrics

import (
	"context"
	"time"

	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/internal/unmatched"
)

// Executor is implemented by server.pipelineProxy and any wrapping middleware.
// It matches the interface defined in each protocol handler package.
type Executor interface {
	Execute(ctx context.Context, pctx *pipeline.PipelineContext) error
}

// Middleware wraps an Executor to record metrics and capture unmatched requests.
type Middleware struct {
	next      Executor
	collector *Collector
	buffer    *unmatched.Buffer
}

// NewMiddleware creates a Middleware that records into col and buf.
func NewMiddleware(next Executor, col *Collector, buf *unmatched.Buffer) *Middleware {
	return &Middleware{next: next, collector: col, buffer: buf}
}

// Execute runs the wrapped pipeline and records the outcome.
func (m *Middleware) Execute(ctx context.Context, pctx *pipeline.PipelineContext) error {
	start := time.Now()
	err := m.next.Execute(ctx, pctx)
	latencyMs := float64(time.Since(start).Milliseconds())

	if pctx.Matched != nil {
		m.collector.RecordHit(pctx.Matched.ID, pctx.Matched.Name, latencyMs)
	} else {
		m.collector.RecordMiss()
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
