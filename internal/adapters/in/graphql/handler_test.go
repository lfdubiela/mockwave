package graphql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	graphqladapter "github.com/mockwave/mockwave/internal/adapters/in/graphql"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

type mockExec struct {
	fn func(ctx context.Context, pctx *pipeline.PipelineContext) error
}

func (m *mockExec) Execute(ctx context.Context, pctx *pipeline.PipelineContext) error {
	return m.fn(ctx, pctx)
}

func TestGraphQLHandler_QuerySimulation(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			assert.Equal(t, "graphql", pctx.Request.Protocol)
			assert.Equal(t, "query", pctx.Request.Method)
			assert.Equal(t, "GetUser", pctx.Request.Path)
			pctx.Response = &pipeline.MockResponse{
				Status: 200,
				Body:   map[string]interface{}{"id": "42", "name": "mock"},
			}
			return nil
		},
	}
	h := graphqladapter.NewHandler(exec)
	body := `{"query":"query GetUser { user { id } }","operationName":"GetUser"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "data")
}

func TestGraphQLHandler_MutationSimulation(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			assert.Equal(t, "mutation", pctx.Request.Method)
			assert.Equal(t, "CreateUser", pctx.Request.Path)
			pctx.Response = &pipeline.MockResponse{Status: 200, Body: map[string]interface{}{"created": true}}
			return nil
		},
	}
	h := graphqladapter.NewHandler(exec)
	body := `{"query":"mutation CreateUser($name: String!) { createUser(name: $name) { id } }"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGraphQLHandler_AnonymousQuery(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			assert.Equal(t, "query", pctx.Request.Method)
			assert.Equal(t, "", pctx.Request.Path)
			pctx.Response = &pipeline.MockResponse{Status: 200, Body: nil}
			return nil
		},
	}
	h := graphqladapter.NewHandler(exec)
	body := `{"query":"{ users { id } }"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGraphQLHandler_InvalidJSON(t *testing.T) {
	exec := &mockExec{fn: func(_ context.Context, pctx *pipeline.PipelineContext) error { return nil }}
	h := graphqladapter.NewHandler(exec)
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "errors")
}

func TestGraphQLHandler_NoMatch(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			return fmt.Errorf("no rule matched")
		},
	}
	h := graphqladapter.NewHandler(exec)
	body := `{"query":"query GetUser { user { id } }"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "errors")
}

func TestExtractOperationType(t *testing.T) {
	cases := []struct {
		query    string
		expected string
	}{
		{"query GetUser { user { id } }", "query"},
		{"mutation CreateUser($n: String!) { createUser { id } }", "mutation"},
		{"subscription OnUpdate { update { id } }", "subscription"},
		{"{ users { id } }", "query"},
		{"  QUERY GetUser { }", "query"},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			assert.Equal(t, c.expected, graphqladapter.ExtractOperationType(c.query))
		})
	}
}

func TestExtractOperationName(t *testing.T) {
	cases := []struct {
		query    string
		expected string
	}{
		{"query GetUser { user { id } }", "GetUser"},
		{"mutation CreateUser($n: String!) { createUser { id } }", "CreateUser"},
		{"query { users { id } }", ""},
		{"{ users { id } }", ""},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			assert.Equal(t, c.expected, graphqladapter.ExtractOperationName(c.query))
		})
	}
}

func TestGraphQLHandler_FaultDelayApplied(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			pctx.FaultDelayMs = 30
			pctx.Response = &pipeline.MockResponse{Status: 200, DelayMs: 20, Body: map[string]interface{}{"ok": true}}
			return nil
		},
	}
	h := graphqladapter.NewHandler(exec)
	body := `{"query":"{ users { id } }"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	w := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(w, req)
	assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGraphQLHandler_FaultShortCircuitJSONBody(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			pctx.FaultShortCircuit = true
			pctx.Response = &pipeline.MockResponse{
				Status:  503,
				Headers: map[string]string{"Retry-After": "5"},
				Body:    map[string]interface{}{"error": "injected fault"},
			}
			return nil
		},
	}
	h := graphqladapter.NewHandler(exec)
	body := `{"query":"query GetUser { user { id } }"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "5", w.Header().Get("Retry-After"))
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "injected fault", resp["error"])
	assert.NotContains(t, resp, "data")
}

func TestGraphQLHandler_FaultShortCircuitStringBody(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			pctx.FaultShortCircuit = true
			pctx.Response = &pipeline.MockResponse{
				Status:  500,
				Headers: map[string]string{"Content-Type": "text/plain"},
				Body:    "boom",
			}
			return nil
		},
	}
	h := graphqladapter.NewHandler(exec)
	body := `{"query":"query GetUser { user { id } }"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")
	assert.Equal(t, "boom", w.Body.String())
}

func TestGraphQL_ResetFault(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			// Mirror the real FaultStage: a reset conn fault leaves Response nil.
			pctx.ConnFault = "reset"
			return nil
		},
	}
	h := graphqladapter.NewHandler(exec)
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/graphql", "application/json", strings.NewReader(`{"query":"{x}"}`))
	if err == nil {
		// Relaxed assertion: a reset must not yield a normal full 200 JSON response,
		// and must not produce the 500 "no response" error from the nil-Response guard.
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusInternalServerError {
			t.Fatal("reset must not hit the nil-Response guard (500)")
		}
		if _, rerr := io.ReadAll(resp.Body); rerr == nil && resp.StatusCode == http.StatusOK {
			t.Fatal("expected connection error from reset, got full 200 response")
		}
	}
}

func TestGraphQL_HangFault(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			// Real FaultStage leaves Response nil for a hang conn fault.
			pctx.ConnFault = "hang"
			pctx.ConnFaultMaxMs = 200
			return nil
		},
	}
	h := graphqladapter.NewHandler(exec)
	srv := httptest.NewServer(h)
	defer srv.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	start := time.Now()
	resp, err := client.Post(srv.URL+"/graphql", "application/json", strings.NewReader(`{"query":"{x}"}`))
	elapsed := time.Since(start)
	if err == nil {
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusInternalServerError {
			t.Fatal("hang must not hit the nil-Response guard (500)")
		}
	}
	assert.GreaterOrEqual(t, elapsed, 180*time.Millisecond, "hang must block ~max_ms before closing")
}

func TestGraphQL_SlowBodyThrottled(t *testing.T) {
	exec := &mockExec{
		fn: func(_ context.Context, pctx *pipeline.PipelineContext) error {
			pctx.SlowBodyBytesPerSec = 256
			pctx.Response = &pipeline.MockResponse{Status: 200, Body: map[string]interface{}{"users": []int{1, 2, 3}}}
			return nil
		},
	}
	h := graphqladapter.NewHandler(exec)
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":"{ users { id } }"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp, "data")
}
