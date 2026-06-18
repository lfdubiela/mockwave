package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mockwave/mockwave/domain"
	mockwavemcp "github.com/mockwave/mockwave/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdminServerWithScenarios starts an httptest server that serves
// /api/scenarios (collection) and /api/scenarios/{id} (item), plus
// /api/scenarios/{id}/start and /api/scenarios/{id}/stop (no-body POSTs).
func fakeAdminServerWithScenarios() *httptest.Server {
	mux := http.NewServeMux()

	sampleScenario := domain.Scenario{
		ID:      "sc1",
		Name:    "Flaky DB Chaos",
		RuleIDs: []string{"rule1"},
		Phases: []domain.ScenarioPhase{
			{DurationSec: 30, FaultProfileID: "fp1"},
			{DurationSec: 10},
		},
	}

	// --- scenarios collection ---
	mux.HandleFunc("/api/scenarios", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			ss := []domain.Scenario{sampleScenario}
			json.NewEncoder(w).Encode(ss)
		case http.MethodPost:
			var s domain.Scenario
			json.NewDecoder(r.Body).Decode(&s)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(s)
		default:
			http.Error(w, "method not allowed", 405)
		}
	})

	// --- scenarios item + action endpoints ---
	mux.HandleFunc("/api/scenarios/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/scenarios/")
		w.Header().Set("Content-Type", "application/json")

		// action: POST /api/scenarios/{id}/start or /stop
		if strings.HasSuffix(path, "/start") {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", 405)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.HasSuffix(path, "/stop") {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", 405)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// item CRUD
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(sampleScenario)
		case http.MethodPut:
			var s domain.Scenario
			json.NewDecoder(r.Body).Decode(&s)
			json.NewEncoder(w).Encode(s)
		case http.MethodDelete:
			w.WriteHeader(204)
		default:
			http.Error(w, "method not allowed", 405)
		}
	})

	return httptest.NewServer(mux)
}

// invokeScenario is a convenience helper that creates a server backed by
// fakeAdminServerWithScenarios and invokes the named tool.
func invokeScenario(t *testing.T, toolName string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	srv := fakeAdminServerWithScenarios()
	defer srv.Close()
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool(toolName)
	require.NotNil(t, tool, "tool %s not found", toolName)
	res, err := tool.Handler(context.Background(), makeReq(toolName, args))
	require.NoError(t, err)
	return res
}

// --- client tests ---

func TestClient_ListScenarios(t *testing.T) {
	srv := fakeAdminServerWithScenarios()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	ss, err := c.ListScenarios()
	require.NoError(t, err)
	require.Len(t, ss, 1)
	assert.Equal(t, "sc1", ss[0].ID)
}

func TestClient_GetScenario(t *testing.T) {
	srv := fakeAdminServerWithScenarios()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	s, err := c.GetScenario("sc1")
	require.NoError(t, err)
	require.NotNil(t, s)
	assert.Equal(t, "sc1", s.ID)
}

func TestClient_CreateScenario(t *testing.T) {
	srv := fakeAdminServerWithScenarios()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	in := domain.Scenario{
		ID:      "sc2",
		Name:    "Slow Network",
		RuleIDs: []string{"rule2"},
		Phases:  []domain.ScenarioPhase{{DurationSec: 60, FaultProfileID: "fp2"}},
	}
	out, err := c.CreateScenario(in)
	require.NoError(t, err)
	assert.Equal(t, "sc2", out.ID)
}

func TestClient_UpdateScenario(t *testing.T) {
	srv := fakeAdminServerWithScenarios()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	in := domain.Scenario{
		ID:      "sc1",
		Name:    "Updated",
		RuleIDs: []string{"rule1"},
		Phases:  []domain.ScenarioPhase{{DurationSec: 10}},
	}
	out, err := c.UpdateScenario("sc1", in)
	require.NoError(t, err)
	assert.Equal(t, "Updated", out.Name)
}

func TestClient_DeleteScenario(t *testing.T) {
	srv := fakeAdminServerWithScenarios()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.DeleteScenario("sc1")
	require.NoError(t, err)
}

func TestClient_StartScenario(t *testing.T) {
	srv := fakeAdminServerWithScenarios()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.StartScenario("sc1")
	require.NoError(t, err)
}

func TestClient_StopScenario(t *testing.T) {
	srv := fakeAdminServerWithScenarios()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.StopScenario("sc1")
	require.NoError(t, err)
}

// --- list_scenarios tool ---

func TestHandler_ListScenarios_Success(t *testing.T) {
	res := invokeScenario(t, "list_scenarios", nil)
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_ListScenarios_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("list_scenarios")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("list_scenarios", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- get_scenario tool ---

func TestHandler_GetScenario_Success(t *testing.T) {
	res := invokeScenario(t, "get_scenario", map[string]any{"id": "sc1"})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_GetScenario_MissingParam(t *testing.T) {
	res := invokeScenario(t, "get_scenario", nil)
	assert.True(t, res.IsError)
}

func TestHandler_GetScenario_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("get_scenario")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("get_scenario", map[string]any{"id": "sc1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- create_scenario tool ---

func TestHandler_CreateScenario_Success(t *testing.T) {
	res := invokeScenario(t, "create_scenario", map[string]any{
		"scenario": map[string]any{
			"id":       "sc2",
			"name":     "Slow Network",
			"rule_ids": []any{"rule2"},
			"phases": []any{
				map[string]any{"duration_sec": 60, "fault_profile_id": "fp2"},
			},
		},
	})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_CreateScenario_MissingParam(t *testing.T) {
	res := invokeScenario(t, "create_scenario", nil)
	assert.True(t, res.IsError)
}

func TestHandler_CreateScenario_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("create_scenario")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("create_scenario", map[string]any{
		"scenario": map[string]any{"id": "sc2"},
	}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- update_scenario tool ---

func TestHandler_UpdateScenario_Success(t *testing.T) {
	res := invokeScenario(t, "update_scenario", map[string]any{
		"id": "sc1",
		"scenario": map[string]any{
			"id":       "sc1",
			"name":     "Updated",
			"rule_ids": []any{"rule1"},
			"phases": []any{
				map[string]any{"duration_sec": 10},
			},
		},
	})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_UpdateScenario_MissingID(t *testing.T) {
	res := invokeScenario(t, "update_scenario", map[string]any{
		"scenario": map[string]any{"id": "sc1"},
	})
	assert.True(t, res.IsError)
}

func TestHandler_UpdateScenario_MissingScenario(t *testing.T) {
	res := invokeScenario(t, "update_scenario", map[string]any{"id": "sc1"})
	assert.True(t, res.IsError)
}

func TestHandler_UpdateScenario_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("update_scenario")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("update_scenario", map[string]any{
		"id":       "sc1",
		"scenario": map[string]any{"id": "sc1"},
	}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- delete_scenario tool ---

func TestHandler_DeleteScenario_Success(t *testing.T) {
	res := invokeScenario(t, "delete_scenario", map[string]any{"id": "sc1"})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "deleted")
}

func TestHandler_DeleteScenario_MissingParam(t *testing.T) {
	res := invokeScenario(t, "delete_scenario", nil)
	assert.True(t, res.IsError)
}

func TestHandler_DeleteScenario_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("delete_scenario")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("delete_scenario", map[string]any{"id": "sc1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- start_scenario tool ---

func TestHandler_StartScenario_Success(t *testing.T) {
	res := invokeScenario(t, "start_scenario", map[string]any{"id": "sc1"})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "started")
}

func TestHandler_StartScenario_MissingParam(t *testing.T) {
	res := invokeScenario(t, "start_scenario", nil)
	assert.True(t, res.IsError)
}

func TestHandler_StartScenario_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("start_scenario")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("start_scenario", map[string]any{"id": "sc1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- stop_scenario tool ---

func TestHandler_StopScenario_Success(t *testing.T) {
	res := invokeScenario(t, "stop_scenario", map[string]any{"id": "sc1"})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "stopped")
}

func TestHandler_StopScenario_MissingParam(t *testing.T) {
	res := invokeScenario(t, "stop_scenario", nil)
	assert.True(t, res.IsError)
}

func TestHandler_StopScenario_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("stop_scenario")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("stop_scenario", map[string]any{"id": "sc1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}
