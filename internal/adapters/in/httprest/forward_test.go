package httprest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/adapters/in/httprest"
	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForwardStage_SkipsWhenNotForward(t *testing.T) {
	stage := httprest.NewForwardStage(nil)
	pctx := &pipeline.PipelineContext{ShouldForward: false}
	assert.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Nil(t, pctx.Response)
}

func TestForwardStage_NoMatchedRule(t *testing.T) {
	stage := httprest.NewForwardStage(nil)
	pctx := &pipeline.PipelineContext{ShouldForward: true, Matched: nil}
	assert.Error(t, stage.Execute(context.Background(), pctx))
}

func TestForwardStage_NoForwardURL(t *testing.T) {
	stage := httprest.NewForwardStage(nil)
	pctx := &pipeline.PipelineContext{
		ShouldForward: true,
		Matched:       &domain.Rule{ID: "r1", ForwardURL: ""},
	}
	assert.Error(t, stage.Execute(context.Background(), pctx))
}

func TestForwardStage_Name(t *testing.T) {
	stage := httprest.NewForwardStage(nil)
	assert.Equal(t, "forward", stage.Name())
}

func TestForwardStage_ForwardsRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"proxied":true}`))
	}))
	defer upstream.Close()

	stage := httprest.NewForwardStage(nil)
	pctx := &pipeline.PipelineContext{
		ShouldForward: true,
		Matched:       &domain.Rule{ID: "r1", ForwardURL: upstream.URL},
		Request:       pipeline.NormalizedRequest{Method: "GET", Path: "/test"},
	}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	require.NotNil(t, pctx.Response)
	assert.Equal(t, 200, pctx.Response.Status)
}

func TestForwardStage_ForwardsWithQuery(t *testing.T) {
	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`"ok"`))
	}))
	defer upstream.Close()

	stage := httprest.NewForwardStage(nil)
	pctx := &pipeline.PipelineContext{
		ShouldForward: true,
		Matched:       &domain.Rule{ID: "r1", ForwardURL: upstream.URL},
		Request: pipeline.NormalizedRequest{
			Method: "GET",
			Path:   "/test",
			Query:  map[string]string{"foo": "bar"},
		},
	}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Contains(t, gotQuery, "foo=bar")
}

func TestForwardStage_DelayDominatesWhenLongerThanUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`"ok"`))
	}))
	defer upstream.Close()

	stage := httprest.NewForwardStage(nil)
	pctx := &pipeline.PipelineContext{
		ShouldForward:  true,
		ForwardDelayMs: 200,
		Matched:        &domain.Rule{ID: "r1", ForwardURL: upstream.URL},
		Request:        pipeline.NormalizedRequest{Method: "GET", Path: "/test"},
	}
	start := time.Now()
	require.NoError(t, stage.Execute(context.Background(), pctx))
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond, "should wait at least the delay")
	assert.Less(t, elapsed, 1*time.Second, "delay should be concurrent, not additive to a long sleep")
	require.NotNil(t, pctx.Response)
	assert.Equal(t, 200, pctx.Response.Status)
}

func TestForwardStage_UpstreamDominatesWhenSlowerThanDelay(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`"ok"`))
	}))
	defer upstream.Close()

	stage := httprest.NewForwardStage(nil)
	pctx := &pipeline.PipelineContext{
		ShouldForward:  true,
		ForwardDelayMs: 50,
		Matched:        &domain.Rule{ID: "r1", ForwardURL: upstream.URL},
		Request:        pipeline.NormalizedRequest{Method: "GET", Path: "/test"},
	}
	start := time.Now()
	require.NoError(t, stage.Execute(context.Background(), pctx))
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond, "should wait for the slower upstream")
	require.NotNil(t, pctx.Response)
}

func TestForwardStage_ZeroDelayNoExtraWait(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`"ok"`))
	}))
	defer upstream.Close()

	stage := httprest.NewForwardStage(nil)
	pctx := &pipeline.PipelineContext{
		ShouldForward:  true,
		ForwardDelayMs: 0,
		Matched:        &domain.Rule{ID: "r1", ForwardURL: upstream.URL},
		Request:        pipeline.NormalizedRequest{Method: "GET", Path: "/test"},
	}
	start := time.Now()
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Less(t, time.Since(start), 100*time.Millisecond)
}
