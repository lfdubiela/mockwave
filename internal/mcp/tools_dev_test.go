package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	mockwavemcp "github.com/mockwave/mockwave/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdminServerWithDev serves the script-eval and metrics-history endpoints.
func fakeAdminServerWithDev() *httptest.Server {
	mux := http.NewServeMux()

	// POST /api/script/eval
	mux.HandleFunc("/api/script/eval", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"result":      42,
			"error":       nil,
			"duration_ms": 3,
		})
	})

	// GET /api/metrics/history
	mux.HandleFunc("/api/metrics/history", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"rules": []any{},
		})
	})

	return httptest.NewServer(mux)
}

// invokeDev is a helper for dev tools.
func invokeDev(t *testing.T, toolName string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	srv := fakeAdminServerWithDev()
	defer srv.Close()
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool(toolName)
	require.NotNil(t, tool, "tool %s not found", toolName)
	res, err := tool.Handler(context.Background(), makeReq(toolName, args))
	require.NoError(t, err)
	return res
}

// --- client tests ---

func TestClient_EvalScript(t *testing.T) {
	srv := fakeAdminServerWithDev()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	result, err := c.EvalScript(map[string]any{"script": "42"})
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestClient_EvalScript_Error(t *testing.T) {
	c := mockwavemcp.NewClient("http://127.0.0.1:1")
	_, err := c.EvalScript(map[string]any{"script": "42"})
	assert.Error(t, err)
}

func TestClient_MetricsHistory(t *testing.T) {
	srv := fakeAdminServerWithDev()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	result, err := c.MetricsHistory()
	require.NoError(t, err)
	require.NotNil(t, result)
	_, ok := result["rules"]
	assert.True(t, ok, "expected 'rules' key in metrics history response")
}

func TestClient_MetricsHistory_Error(t *testing.T) {
	c := mockwavemcp.NewClient("http://127.0.0.1:1")
	_, err := c.MetricsHistory()
	assert.Error(t, err)
}

// --- eval_script tool ---

func TestHandler_EvalScript_Success(t *testing.T) {
	res := invokeDev(t, "eval_script", map[string]any{
		"script": "42",
	})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_EvalScript_MissingScript(t *testing.T) {
	res := invokeDev(t, "eval_script", nil)
	assert.True(t, res.IsError, "expected error when script param is missing")
}

func TestHandler_EvalScript_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("eval_script")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("eval_script", map[string]any{
		"script": "42",
	}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- get_metrics_history tool ---

func TestHandler_GetMetricsHistory_Success(t *testing.T) {
	res := invokeDev(t, "get_metrics_history", nil)
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_GetMetricsHistory_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("get_metrics_history")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("get_metrics_history", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}
