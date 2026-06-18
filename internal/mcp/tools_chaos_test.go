package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mockwave/mockwave/domain"
	mockwavemcp "github.com/mockwave/mockwave/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdminServerWithFaults starts an httptest server that serves
// /api/faults (collection) and /api/faults/{id} (item, real GET endpoint).
func fakeAdminServerWithFaults() *httptest.Server {
	mux := http.NewServeMux()

	sampleProfile := domain.FaultProfile{
		ID:      "fp1",
		Name:    "Flaky DB",
		Enabled: true,
		Faults: []domain.Fault{
			{Type: domain.FaultJitter, Probability: 0.5, Params: domain.FaultParams{BaseDelayMs: 100, JitterMs: 50}},
		},
	}

	// --- faults collection ---
	mux.HandleFunc("/api/faults", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			fps := []domain.FaultProfile{sampleProfile}
			json.NewEncoder(w).Encode(fps)
		case http.MethodPost:
			var fp domain.FaultProfile
			json.NewDecoder(r.Body).Decode(&fp)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(fp)
		}
	})

	// --- faults item (real GET /api/faults/{id} exists) ---
	mux.HandleFunc("/api/faults/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(sampleProfile)
		case http.MethodPut:
			var fp domain.FaultProfile
			json.NewDecoder(r.Body).Decode(&fp)
			json.NewEncoder(w).Encode(fp)
		case http.MethodDelete:
			w.WriteHeader(204)
		default:
			http.Error(w, "method not allowed", 405)
		}
	})

	return httptest.NewServer(mux)
}

// invokeFault is a convenience helper that creates a server backed by
// fakeAdminServerWithFaults and invokes the named tool.
func invokeFault(t *testing.T, toolName string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	srv := fakeAdminServerWithFaults()
	defer srv.Close()
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool(toolName)
	require.NotNil(t, tool, "tool %s not found", toolName)
	res, err := tool.Handler(context.Background(), makeReq(toolName, args))
	require.NoError(t, err)
	return res
}

// --- client tests ---

func TestClient_ListFaultProfiles(t *testing.T) {
	srv := fakeAdminServerWithFaults()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	fps, err := c.ListFaultProfiles()
	require.NoError(t, err)
	require.Len(t, fps, 1)
	assert.Equal(t, "fp1", fps[0].ID)
}

func TestClient_GetFaultProfile(t *testing.T) {
	srv := fakeAdminServerWithFaults()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	fp, err := c.GetFaultProfile("fp1")
	require.NoError(t, err)
	require.NotNil(t, fp)
	assert.Equal(t, "fp1", fp.ID)
}

func TestClient_CreateFaultProfile(t *testing.T) {
	srv := fakeAdminServerWithFaults()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	in := domain.FaultProfile{
		ID:      "fp2",
		Name:    "Slow Network",
		Enabled: true,
		Faults:  []domain.Fault{{Type: domain.FaultSlowBody, Probability: 1.0, Params: domain.FaultParams{BytesPerSec: 512}}},
	}
	out, err := c.CreateFaultProfile(in)
	require.NoError(t, err)
	assert.Equal(t, "fp2", out.ID)
}

func TestClient_UpdateFaultProfile(t *testing.T) {
	srv := fakeAdminServerWithFaults()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	in := domain.FaultProfile{
		ID:      "fp1",
		Name:    "Updated",
		Enabled: false,
		Faults:  []domain.Fault{{Type: domain.FaultReset, Probability: 1.0}},
	}
	out, err := c.UpdateFaultProfile("fp1", in)
	require.NoError(t, err)
	assert.Equal(t, "Updated", out.Name)
}

func TestClient_DeleteFaultProfile(t *testing.T) {
	srv := fakeAdminServerWithFaults()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.DeleteFaultProfile("fp1")
	require.NoError(t, err)
}

// --- list_faults tool ---

func TestHandler_ListFaults_Success(t *testing.T) {
	res := invokeFault(t, "list_faults", nil)
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_ListFaults_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("list_faults")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("list_faults", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- get_fault tool ---

func TestHandler_GetFault_Success(t *testing.T) {
	res := invokeFault(t, "get_fault", map[string]any{"id": "fp1"})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_GetFault_MissingParam(t *testing.T) {
	res := invokeFault(t, "get_fault", nil)
	assert.True(t, res.IsError)
}

func TestHandler_GetFault_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("get_fault")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("get_fault", map[string]any{"id": "fp1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- create_fault tool ---

func TestHandler_CreateFault_Success(t *testing.T) {
	res := invokeFault(t, "create_fault", map[string]any{
		"fault_profile": map[string]any{
			"id":      "fp2",
			"name":    "Slow Network",
			"enabled": true,
			"faults": []any{
				map[string]any{
					"type":        "slowBody",
					"probability": 1.0,
					"params":      map[string]any{"bytes_per_sec": 512},
				},
			},
		},
	})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_CreateFault_MissingParam(t *testing.T) {
	res := invokeFault(t, "create_fault", nil)
	assert.True(t, res.IsError)
}

func TestHandler_CreateFault_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("create_fault")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("create_fault", map[string]any{
		"fault_profile": map[string]any{"id": "fp2"},
	}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- update_fault tool ---

func TestHandler_UpdateFault_Success(t *testing.T) {
	res := invokeFault(t, "update_fault", map[string]any{
		"id": "fp1",
		"fault_profile": map[string]any{
			"id":      "fp1",
			"name":    "Updated Flaky",
			"enabled": false,
			"faults": []any{
				map[string]any{
					"type":        "reset",
					"probability": 1.0,
					"params":      map[string]any{},
				},
			},
		},
	})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_UpdateFault_MissingID(t *testing.T) {
	res := invokeFault(t, "update_fault", map[string]any{
		"fault_profile": map[string]any{"id": "fp1"},
	})
	assert.True(t, res.IsError)
}

func TestHandler_UpdateFault_MissingFaultProfile(t *testing.T) {
	res := invokeFault(t, "update_fault", map[string]any{"id": "fp1"})
	assert.True(t, res.IsError)
}

func TestHandler_UpdateFault_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("update_fault")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("update_fault", map[string]any{
		"id":            "fp1",
		"fault_profile": map[string]any{"id": "fp1"},
	}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- delete_fault tool ---

func TestHandler_DeleteFault_Success(t *testing.T) {
	res := invokeFault(t, "delete_fault", map[string]any{"id": "fp1"})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "deleted")
}

func TestHandler_DeleteFault_MissingParam(t *testing.T) {
	res := invokeFault(t, "delete_fault", nil)
	assert.True(t, res.IsError)
}

func TestHandler_DeleteFault_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("delete_fault")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("delete_fault", map[string]any{"id": "fp1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}
