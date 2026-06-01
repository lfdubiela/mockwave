package metrics_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/internal/metrics"
	"github.com/mockwave/mockwave/internal/unmatched"
	"github.com/mockwave/mockwave/observability"
	"github.com/stretchr/testify/assert"
)

type stubExecutor struct {
	matchedRule *domain.Rule
	returnErr   error
}

func (s *stubExecutor) Execute(_ context.Context, pctx *pipeline.PipelineContext) error {
	pctx.Matched = s.matchedRule
	return s.returnErr
}

func TestMiddleware_RecordsHit(t *testing.T) {
	col := metrics.NewCollector()
	buf := unmatched.NewBuffer(10)
	stub := &stubExecutor{matchedRule: &domain.Rule{ID: "r1", Name: "Rule One"}}
	mw := metrics.NewMiddleware(stub, col, buf, observability.NoopTracer{}, observability.NoopMetrics{})

	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Method: "GET", Path: "/foo"}}
	_ = mw.Execute(context.Background(), pctx)

	snap := col.Snapshot()
	assert.Equal(t, int64(1), snap.TotalRequests)
	assert.Equal(t, int64(0), snap.Misses)
	assert.Len(t, snap.Rules, 1)
	assert.Equal(t, "r1", snap.Rules[0].RuleID)
	assert.Empty(t, buf.List())
}

func TestMiddleware_RecordsMiss(t *testing.T) {
	col := metrics.NewCollector()
	buf := unmatched.NewBuffer(10)
	stub := &stubExecutor{returnErr: errors.New("no rule matched")}
	mw := metrics.NewMiddleware(stub, col, buf, observability.NoopTracer{}, observability.NoopMetrics{})

	pctx := &pipeline.PipelineContext{
		Request: pipeline.NormalizedRequest{Protocol: "http", Method: "POST", Path: "/missing"},
	}
	_ = mw.Execute(context.Background(), pctx)

	snap := col.Snapshot()
	assert.Equal(t, int64(1), snap.TotalRequests)
	assert.Equal(t, int64(1), snap.Misses)
	items := buf.List()
	assert.Len(t, items, 1)
	assert.Equal(t, "/missing", items[0].Path)
	assert.Equal(t, "POST", items[0].Method)
}
