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

// fakeAdminServerWithTransfer serves the export and import endpoints.
func fakeAdminServerWithTransfer() *httptest.Server {
	mux := http.NewServeMux()

	sampleExport := map[string]any{
		"rules":          []any{},
		"simulations":    []any{},
		"fault_profiles": []any{},
		"scenarios":      []any{},
		"event_rules":    []any{},
	}

	// GET /api/export
	mux.HandleFunc("/api/export", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sampleExport)
	})

	// POST /api/import
	mux.HandleFunc("/api/import", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"imported":           []string{},
			"skipped":            []string{},
			"overridden":         []string{},
			"scenarios_imported": []string{},
		})
	})

	// POST /api/import/preview
	mux.HandleFunc("/api/import/preview", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"importable":              0,
			"conflicts":               []any{},
			"fault_profile_conflicts": []any{},
			"scenario_conflicts":      []any{},
		})
	})

	return httptest.NewServer(mux)
}

// invokeTransfer is a convenience helper for the transfer tools.
func invokeTransfer(t *testing.T, toolName string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	srv := fakeAdminServerWithTransfer()
	defer srv.Close()
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool(toolName)
	require.NotNil(t, tool, "tool %s not found", toolName)
	res, err := tool.Handler(context.Background(), makeReq(toolName, args))
	require.NoError(t, err)
	return res
}

// --- client tests ---

func TestClient_ExportConfig(t *testing.T) {
	srv := fakeAdminServerWithTransfer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	cfg, err := c.ExportConfig()
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

func TestClient_ImportConfig(t *testing.T) {
	srv := fakeAdminServerWithTransfer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	result, err := c.ImportConfig(map[string]any{"rules": []any{}}, false)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestClient_ImportConfigPreview(t *testing.T) {
	srv := fakeAdminServerWithTransfer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	result, err := c.ImportConfig(map[string]any{"rules": []any{}}, true)
	require.NoError(t, err)
	require.NotNil(t, result)
}

// --- export_config tool ---

func TestHandler_ExportConfig_Success(t *testing.T) {
	res := invokeTransfer(t, "export_config", nil)
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_ExportConfig_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("export_config")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("export_config", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- import_config tool (no preview) ---

func TestHandler_ImportConfig_Success(t *testing.T) {
	res := invokeTransfer(t, "import_config", map[string]any{
		"config": map[string]any{
			"rules":       []any{},
			"simulations": []any{},
		},
	})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_ImportConfig_MissingConfig(t *testing.T) {
	res := invokeTransfer(t, "import_config", nil)
	assert.True(t, res.IsError)
}

func TestHandler_ImportConfig_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("import_config")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("import_config", map[string]any{
		"config": map[string]any{"rules": []any{}},
	}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- import_config tool with preview=true routes to /api/import/preview ---

func TestHandler_ImportConfig_Preview_RoutesToPreviewEndpoint(t *testing.T) {
	// This server only serves /api/import/preview (not /api/import),
	// so the call must succeed only when the tool routes to the preview path.
	previewHit := false
	importHit := false

	mux := http.NewServeMux()
	mux.HandleFunc("/api/import/preview", func(w http.ResponseWriter, r *http.Request) {
		previewHit = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"importable":              0,
			"conflicts":               []any{},
			"fault_profile_conflicts": []any{},
			"scenario_conflicts":      []any{},
		})
	})
	mux.HandleFunc("/api/import", func(w http.ResponseWriter, r *http.Request) {
		importHit = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool("import_config")
	require.NotNil(t, tool)

	res, err := tool.Handler(context.Background(), makeReq("import_config", map[string]any{
		"config":  map[string]any{"rules": []any{}},
		"preview": true,
	}))
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success: %v", res.Content)
	assert.True(t, previewHit, "expected /api/import/preview to be called")
	assert.False(t, importHit, "expected /api/import NOT to be called for preview")
}

func TestHandler_ImportConfig_NoPreview_RoutesToImportEndpoint(t *testing.T) {
	previewHit := false
	importHit := false

	mux := http.NewServeMux()
	mux.HandleFunc("/api/import/preview", func(w http.ResponseWriter, r *http.Request) {
		previewHit = true
		http.Error(w, "should not be called", 500)
	})
	mux.HandleFunc("/api/import", func(w http.ResponseWriter, r *http.Request) {
		importHit = true
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"imported":           []string{},
			"skipped":            []string{},
			"overridden":         []string{},
			"scenarios_imported": []string{},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool("import_config")
	require.NotNil(t, tool)

	res, err := tool.Handler(context.Background(), makeReq("import_config", map[string]any{
		"config": map[string]any{"rules": []any{}},
		// preview omitted → defaults to false
	}))
	require.NoError(t, err)
	assert.False(t, res.IsError, "expected success: %v", res.Content)
	assert.False(t, previewHit, "expected /api/import/preview NOT to be called")
	assert.True(t, importHit, "expected /api/import to be called")
}

func TestHandler_ImportConfig_Preview_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("import_config")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("import_config", map[string]any{
		"config":  map[string]any{"rules": []any{}},
		"preview": true,
	}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}
