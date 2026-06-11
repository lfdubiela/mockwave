package soap_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	soapadapter "github.com/mockwave/mockwave/internal/adapters/in/soap"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

type mockExec struct {
	fn func(ctx context.Context, pctx *pipeline.PipelineContext) error
}

func (m *mockExec) Execute(ctx context.Context, pctx *pipeline.PipelineContext) error {
	return m.fn(ctx, pctx)
}

const sampleEnvelope = `<?xml version="1.0" encoding="UTF-8"?><soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><GetUserResponse><id>42</id></GetUserResponse></soap:Body></soap:Envelope>`

func TestSOAPHandler_SimulationWithEnvelope(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			assert.Equal(t, "soap", pctx.Request.Protocol)
			assert.Equal(t, "http://example.com/GetUser", pctx.Request.Path)
			pctx.Response = &pipeline.MockResponse{
				Status:  200,
				Headers: map[string]string{"content-type": "text/xml; charset=utf-8"},
				Body:    sampleEnvelope,
			}
			return nil
		},
	}
	h := soapadapter.NewHandler(exec)
	req := httptest.NewRequest(http.MethodPost, "/service", strings.NewReader(`<soap:Envelope/>`))
	req.Header.Set("SOAPAction", `"http://example.com/GetUser"`)
	req.Header.Set("Content-Type", "text/xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/xml")
	assert.Contains(t, w.Body.String(), "GetUserResponse")
}

func TestSOAPHandler_UnquotedSOAPAction(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			assert.Equal(t, "GetUser", pctx.Request.Path)
			pctx.Response = &pipeline.MockResponse{
				Status: 200,
				Body:   sampleEnvelope,
			}
			return nil
		},
	}
	h := soapadapter.NewHandler(exec)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`<soap:Envelope/>`))
	req.Header.Set("SOAPAction", "GetUser")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestSOAPHandler_NoMatch(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			return fmt.Errorf("no rule matched")
		},
	}
	h := soapadapter.NewHandler(exec)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`<soap:Envelope/>`))
	req.Header.Set("SOAPAction", "UnknownAction")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/xml")
	assert.Contains(t, w.Body.String(), "soap:Fault")
}

func TestSOAPHandler_EmptySoapEnvelope(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			pctx.Response = &pipeline.MockResponse{Status: 200, Body: nil}
			return nil
		},
	}
	h := soapadapter.NewHandler(exec)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`<soap:Envelope/>`))
	req.Header.Set("SOAPAction", "SomeAction")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "soap:Fault")
}

func TestSOAPHandler_FaultShortCircuitJSONBody(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			pctx.FaultShortCircuit = true
			pctx.Response = &pipeline.MockResponse{
				Status: 503,
				Body:   map[string]interface{}{"error": "injected fault"},
			}
			return nil
		},
	}
	h := soapadapter.NewHandler(exec)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`<soap:Envelope/>`))
	req.Header.Set("SOAPAction", "GetUser")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
	assert.Contains(t, w.Body.String(), "injected fault")
}

func TestSOAPHandler_FaultShortCircuitStringBody(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			pctx.FaultShortCircuit = true
			pctx.Response = &pipeline.MockResponse{
				Status:  500,
				Headers: map[string]string{"Content-Type": "text/xml"},
				Body:    "<fault/>",
			}
			return nil
		},
	}
	h := soapadapter.NewHandler(exec)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`<soap:Envelope/>`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/xml")
	assert.Equal(t, "<fault/>", w.Body.String())
}

func TestSOAPHandler_FaultDelayApplied(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			pctx.FaultDelayMs = 30
			pctx.Response = &pipeline.MockResponse{Status: 200, DelayMs: 20, Body: sampleEnvelope}
			return nil
		},
	}
	h := soapadapter.NewHandler(exec)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`<soap:Envelope/>`))
	w := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(w, req)
	assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
	assert.Equal(t, http.StatusOK, w.Code)
}
