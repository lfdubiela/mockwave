package httprest_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mockwave/mockwave/internal/adapters/in/httprest"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubPipeline struct {
	fn func(ctx context.Context, pctx *pipeline.PipelineContext) error
}

func (s *stubPipeline) Execute(ctx context.Context, pctx *pipeline.PipelineContext) error {
	return s.fn(ctx, pctx)
}

func TestHandler_NormalizesRequest(t *testing.T) {
	var captured *pipeline.PipelineContext
	pipe := &stubPipeline{fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
		captured = pctx
		pctx.Response = &pipeline.MockResponse{Status: 200, Body: map[string]interface{}{"ok": true}}
		return nil
	}}
	h := httprest.NewHandler(pipe)
	req := httptest.NewRequest(http.MethodGet, "/users/42?page=1", nil)
	req.Header.Set("x-cid", "999")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, captured)
	assert.Equal(t, "http", captured.Request.Protocol)
	assert.Equal(t, "GET", captured.Request.Method)
	assert.Equal(t, "/users/42", captured.Request.Path)
	assert.Equal(t, "999", captured.Request.Headers["x-cid"])
	assert.Equal(t, "1", captured.Request.Query["page"])
	assert.Equal(t, []string{"users", "42"}, captured.Request.PathSegs)
}

func TestHandler_Returns404OnPipelineError(t *testing.T) {
	pipe := &stubPipeline{fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
		return fmt.Errorf("no rule matched: GET /missing")
	}}
	h := httprest.NewHandler(pipe)
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandler_SetsResponseHeaders(t *testing.T) {
	pipe := &stubPipeline{fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
		pctx.Response = &pipeline.MockResponse{Status: 201, Headers: map[string]string{"x-mock": "true"}}
		return nil
	}}
	h := httprest.NewHandler(pipe)
	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, 201, w.Code)
	assert.Equal(t, "true", w.Header().Get("x-mock"))
}

func TestHandler_NilResponseReturns500(t *testing.T) {
	pipe := &stubPipeline{fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
		// don't set pctx.Response
		return nil
	}}
	h := httprest.NewHandler(pipe)
	req := httptest.NewRequest(http.MethodGet, "/foo", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
