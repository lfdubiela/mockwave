package metrics_test

import (
	"context"
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
