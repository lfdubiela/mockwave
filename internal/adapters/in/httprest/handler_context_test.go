package httprest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mockwave/mockwave/internal/adapters/in/httprest"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/observability"
	"github.com/stretchr/testify/assert"
)

// contextCapturingExecutor captures the context passed to Execute.
type contextCapturingExecutor struct {
	capturedCtx context.Context
}

func (e *contextCapturingExecutor) Execute(ctx context.Context, pctx *pipeline.PipelineContext) error {
	e.capturedCtx = ctx
	pctx.Response = &pipeline.MockResponse{Status: 200}
	return nil
}

func TestHandler_StampsRequestContext(t *testing.T) {
	exec := &contextCapturingExecutor{}
	h := httprest.NewHandler(exec)

	req := httptest.NewRequest(http.MethodPost, "/orders/99", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	ri := observability.FromContext(exec.capturedCtx)
	assert.Equal(t, "POST", ri.Method)
	assert.Equal(t, "/orders/99", ri.Path)
	assert.Equal(t, "http", ri.Protocol)
	assert.NotEmpty(t, ri.RequestID)
}
