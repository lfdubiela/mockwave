package metrics_test

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/internal/metrics"
	"github.com/mockwave/mockwave/internal/unmatched"
	"github.com/mockwave/mockwave/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiddleware_CapturesMatched(t *testing.T) {
	buf := matched.NewBuffer(10)
	exec := &stubExecutor{matchedRule: &domain.Rule{ID: "r1", Name: "R"}}
	mw := metrics.NewMiddleware(exec, metrics.NewCollector(), unmatched.NewBuffer(10), observability.NoopTracer{}, observability.NoopMetrics{})
	mw.SetMatchedCapture(buf, 3600)

	pctx := &pipeline.PipelineContext{
		Request: pipeline.NormalizedRequest{
			Method:   "POST",
			Path:     "/users",
			Protocol: "http",
			Headers:  map[string]string{"x-cid": "abc"},
			Body:     []byte(`{"name":"x"}`),
		},
		Response: &pipeline.MockResponse{Status: 201, Headers: map[string]string{"content-type": "application/json"}, Body: map[string]any{"id": 1}},
	}
	require.NoError(t, mw.Execute(context.Background(), pctx))

	page := buf.List("r1", matched.Query{})
	require.Len(t, page.Items, 1)
	got := page.Items[0]
	assert.Equal(t, "r1", got.RuleID)
	assert.Equal(t, "POST", got.Method)
	assert.Equal(t, "/users", got.Path)
	assert.Equal(t, 201, got.ResponseStatus)
	assert.Equal(t, "abc", got.Headers["x-cid"])
	assert.NotZero(t, got.TTL)

	full, ok := buf.Get("r1", got.ID)
	require.True(t, ok)
	assert.JSONEq(t, `{"name":"x"}`, string(full.RequestBody))
	assert.Equal(t, map[string]any{"id": 1}, full.ResponseBody)
}

func TestMiddleware_NoCaptureWhenDisabled(t *testing.T) {
	exec := &stubExecutor{matchedRule: &domain.Rule{ID: "r1"}}
	mw := metrics.NewMiddleware(exec, metrics.NewCollector(), unmatched.NewBuffer(10), observability.NoopTracer{}, observability.NoopMetrics{})
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Method: "GET", Path: "/x", Protocol: "http"}}
	require.NoError(t, mw.Execute(context.Background(), pctx))
}
