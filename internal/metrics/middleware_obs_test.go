package metrics_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/internal/metrics"
	"github.com/mockwave/mockwave/internal/unmatched"
	"github.com/mockwave/mockwave/observability"
	"github.com/stretchr/testify/assert"
)

// recordingMetrics captures RecordRequest and RecordUnmatched calls.
type recordingMetrics struct {
	requests  []observability.RequestAttrs
	unmatched [][]string // [method, path, protocol]
}

func (r *recordingMetrics) RecordRequest(_ context.Context, attrs observability.RequestAttrs) {
	r.requests = append(r.requests, attrs)
}
func (r *recordingMetrics) RecordUnmatched(_ context.Context, method, path, protocol string) {
	r.unmatched = append(r.unmatched, []string{method, path, protocol})
}

func TestMiddleware_CallsRecorderOnHit(t *testing.T) {
	col := metrics.NewCollector()
	buf := unmatched.NewBuffer(10)
	rec := &recordingMetrics{}
	tracer := observability.NoopTracer{}

	exec := &stubExecutor{matchedRule: &domain.Rule{ID: "r1", Name: "My Rule"}}
	mw := metrics.NewMiddleware(exec, col, buf, tracer, rec)

	pctx := &pipeline.PipelineContext{
		Request: pipeline.NormalizedRequest{Method: "GET", Path: "/ping", Protocol: "http"},
	}
	err := mw.Execute(context.Background(), pctx)
	assert.NoError(t, err)

	assert.Len(t, rec.requests, 1)
	assert.Equal(t, "r1", rec.requests[0].RuleID)
	assert.Equal(t, "My Rule", rec.requests[0].RuleName)
	assert.Equal(t, "GET", rec.requests[0].Method)
	assert.Equal(t, "/ping", rec.requests[0].Path)
	assert.Equal(t, "http", rec.requests[0].Protocol)
}

func TestMiddleware_CallsUnmatchedOnMiss(t *testing.T) {
	col := metrics.NewCollector()
	buf := unmatched.NewBuffer(10)
	rec := &recordingMetrics{}
	tracer := observability.NoopTracer{}

	exec := &stubExecutor{matchedRule: nil}
	mw := metrics.NewMiddleware(exec, col, buf, tracer, rec)

	pctx := &pipeline.PipelineContext{
		Request: pipeline.NormalizedRequest{Method: "POST", Path: "/missing", Protocol: "http"},
	}
	_ = mw.Execute(context.Background(), pctx)

	assert.Len(t, rec.unmatched, 1)
	assert.Equal(t, []string{"POST", "/missing", "http"}, rec.unmatched[0])
	assert.Empty(t, rec.requests)
}

// spySpan records whether SetError was called.
type spySpan struct {
	errSet error
}

func (s *spySpan) End()                    {}
func (s *spySpan) SetError(err error)      { s.errSet = err }
func (s *spySpan) SetAttr(_ string, _ any) {}

// spyTracer returns a spySpan so tests can inspect it.
type spyTracer struct {
	span *spySpan
}

func (t *spyTracer) Start(ctx context.Context, _ string, _ ...observability.Attr) (context.Context, observability.Span) {
	t.span = &spySpan{}
	return ctx, t.span
}

// errExecutor always returns an error from Execute.
type errExecutor struct{}

func (e *errExecutor) Execute(_ context.Context, pctx *pipeline.PipelineContext) error {
	return fmt.Errorf("pipeline boom")
}

func TestMiddleware_SetsSpanErrorOnPipelineError(t *testing.T) {
	col := metrics.NewCollector()
	buf := unmatched.NewBuffer(10)
	rec := &recordingMetrics{}
	tracer := &spyTracer{}

	mw := metrics.NewMiddleware(&errExecutor{}, col, buf, tracer, rec)

	pctx := &pipeline.PipelineContext{
		Request: pipeline.NormalizedRequest{Method: "GET", Path: "/boom", Protocol: "http"},
	}
	err := mw.Execute(context.Background(), pctx)

	assert.Error(t, err)
	assert.NotNil(t, tracer.span)
	assert.Equal(t, "pipeline boom", tracer.span.errSet.Error())
}

func TestMiddleware_StampsContextIfNotAlreadyStamped(t *testing.T) {
	col := metrics.NewCollector()
	buf := unmatched.NewBuffer(10)
	rec := &recordingMetrics{}
	tracer := observability.NoopTracer{}

	// Use executorFunc with no match (miss path doesn't matter, we care about ctx)
	var capturedCtx context.Context
	captureExec := executorFunc(func(ctx context.Context, pctx *pipeline.PipelineContext) error {
		capturedCtx = ctx
		pctx.Response = &pipeline.MockResponse{Status: 200}
		return nil
	})

	mw := metrics.NewMiddleware(captureExec, col, buf, tracer, rec)
	pctx := &pipeline.PipelineContext{
		Request: pipeline.NormalizedRequest{Method: "GET", Path: "/grpc-like", Protocol: "grpc"},
	}
	// Pass a bare context (no stamp — simulates gRPC/SOAP adapter)
	_ = mw.Execute(context.Background(), pctx)

	ri := observability.FromContext(capturedCtx)
	assert.Equal(t, "GET", ri.Method)
	assert.Equal(t, "/grpc-like", ri.Path)
	assert.Equal(t, "grpc", ri.Protocol)
	assert.NotEmpty(t, ri.RequestID)
}

// executorFunc allows using a closure as an Executor in tests.
type executorFunc func(ctx context.Context, pctx *pipeline.PipelineContext) error

func (f executorFunc) Execute(ctx context.Context, pctx *pipeline.PipelineContext) error {
	return f(ctx, pctx)
}
