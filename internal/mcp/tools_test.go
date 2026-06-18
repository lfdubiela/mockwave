package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	mockwavemcp "github.com/mockwave/mockwave/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeReq builds a CallToolRequest with the given arguments map.
func makeReq(name string, args map[string]any) mcpsdk.CallToolRequest {
	req := mcpsdk.CallToolRequest{}
	req.Params.Name = name
	if args != nil {
		req.Params.Arguments = args
	}
	return req
}

// invoke calls the named tool handler on a server pointed at adminURL.
func invoke(t *testing.T, adminURL, toolName string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	s := mockwavemcp.NewServer(adminURL, "test")
	tool := s.GetTool(toolName)
	require.NotNil(t, tool, "tool %s not found", toolName)
	res, err := tool.Handler(context.Background(), makeReq(toolName, args))
	require.NoError(t, err)
	return res
}

// --- list_rules ---

func TestHandler_ListRules_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "list_rules", nil)
	assert.False(t, res.IsError, "expected success, got error content: %v", res.Content)
}

func TestHandler_ListRules_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("list_rules")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("list_rules", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- get_rule ---

func TestHandler_GetRule_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "get_rule", map[string]any{"id": "r1"})
	assert.False(t, res.IsError)
}

func TestHandler_GetRule_MissingParam(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "get_rule", nil)
	assert.True(t, res.IsError)
}

func TestHandler_GetRule_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("get_rule")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("get_rule", map[string]any{"id": "r1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- create_rule ---

func TestHandler_CreateRule_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "create_rule", map[string]any{
		"rule": map[string]any{"id": "r2", "name": "R2", "match": map[string]any{"path": "/bar"}},
	})
	assert.False(t, res.IsError)
}

func TestHandler_CreateRule_MissingParam(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "create_rule", nil)
	assert.True(t, res.IsError)
}

func TestHandler_CreateRule_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("create_rule")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("create_rule", map[string]any{
		"rule": map[string]any{"id": "r2"},
	}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- update_rule ---

func TestHandler_UpdateRule_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "update_rule", map[string]any{
		"id":   "r1",
		"rule": map[string]any{"id": "r1", "name": "R1-updated"},
	})
	assert.False(t, res.IsError)
}

func TestHandler_UpdateRule_MissingID(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "update_rule", map[string]any{
		"rule": map[string]any{"id": "r1"},
	})
	assert.True(t, res.IsError)
}

func TestHandler_UpdateRule_MissingRule(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "update_rule", map[string]any{"id": "r1"})
	assert.True(t, res.IsError)
}

func TestHandler_UpdateRule_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("update_rule")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("update_rule", map[string]any{
		"id":   "r1",
		"rule": map[string]any{"id": "r1"},
	}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- delete_rule ---

func TestHandler_DeleteRule_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "delete_rule", map[string]any{"id": "r1"})
	assert.False(t, res.IsError)
	// Check result text contains "deleted"
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "deleted")
}

func TestHandler_DeleteRule_MissingParam(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "delete_rule", nil)
	assert.True(t, res.IsError)
}

func TestHandler_DeleteRule_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("delete_rule")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("delete_rule", map[string]any{"id": "r1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- list_simulations ---

func TestHandler_ListSimulations_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "list_simulations", nil)
	assert.False(t, res.IsError)
}

func TestHandler_ListSimulations_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("list_simulations")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("list_simulations", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- get_simulation ---

func TestHandler_GetSimulation_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "get_simulation", map[string]any{"id": "s1"})
	assert.False(t, res.IsError)
}

func TestHandler_GetSimulation_MissingParam(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "get_simulation", nil)
	assert.True(t, res.IsError)
}

func TestHandler_GetSimulation_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("get_simulation")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("get_simulation", map[string]any{"id": "s1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- create_simulation ---

func TestHandler_CreateSimulation_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "create_simulation", map[string]any{
		"simulation": map[string]any{"id": "s2", "protocol": "http"},
	})
	assert.False(t, res.IsError)
}

func TestHandler_CreateSimulation_MissingParam(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "create_simulation", nil)
	assert.True(t, res.IsError)
}

func TestHandler_CreateSimulation_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("create_simulation")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("create_simulation", map[string]any{
		"simulation": map[string]any{"id": "s2"},
	}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- update_simulation ---

func TestHandler_UpdateSimulation_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "update_simulation", map[string]any{
		"id":         "s1",
		"simulation": map[string]any{"id": "s1", "protocol": "graphql"},
	})
	assert.False(t, res.IsError)
}

func TestHandler_UpdateSimulation_MissingID(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "update_simulation", map[string]any{
		"simulation": map[string]any{"id": "s1"},
	})
	assert.True(t, res.IsError)
}

func TestHandler_UpdateSimulation_MissingSimulation(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "update_simulation", map[string]any{"id": "s1"})
	assert.True(t, res.IsError)
}

func TestHandler_UpdateSimulation_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("update_simulation")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("update_simulation", map[string]any{
		"id":         "s1",
		"simulation": map[string]any{"id": "s1"},
	}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- delete_simulation ---

func TestHandler_DeleteSimulation_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "delete_simulation", map[string]any{"id": "s1"})
	assert.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "deleted")
}

func TestHandler_DeleteSimulation_MissingParam(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "delete_simulation", nil)
	assert.True(t, res.IsError)
}

func TestHandler_DeleteSimulation_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("delete_simulation")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("delete_simulation", map[string]any{"id": "s1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- get_metrics ---

func TestHandler_GetMetrics_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "get_metrics", nil)
	assert.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.True(t, strings.Contains(text.Text, "total_requests") || len(text.Text) > 0)
}

func TestHandler_GetMetrics_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("get_metrics")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("get_metrics", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- list_unmatched ---

func TestHandler_ListUnmatched_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "list_unmatched", nil)
	assert.False(t, res.IsError)
}

func TestHandler_ListUnmatched_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("list_unmatched")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("list_unmatched", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- clear_unmatched ---

func TestHandler_ClearUnmatched_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "clear_unmatched", nil)
	assert.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "cleared")
}

func TestHandler_ClearUnmatched_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("clear_unmatched")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("clear_unmatched", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- reload ---

func TestHandler_Reload_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "reload", nil)
	assert.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "reload")
}

func TestHandler_Reload_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("reload")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("reload", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- health ---

func TestHandler_Health_Success(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	res := invoke(t, srv.URL, "health", nil)
	assert.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Equal(t, "ok", text.Text)
}

func TestHandler_Health_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("health")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("health", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- generate_from_openapi ---

func TestHandler_GenerateFromOpenAPI_Success(t *testing.T) {
	specYAML := `
openapi: "3.0.0"
info:
  title: T
  version: "1"
paths:
  /items:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:
                    type: integer
`
	specSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = io.WriteString(w, specYAML)
	}))
	defer specSrv.Close()

	createdSims := map[string]bool{}
	createdRules := map[string]bool{}

	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// Conflict check — 404 means "not found"
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/rules/"):
			http.Error(w, "not found", 404)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/simulations/"):
			http.Error(w, "not found", 404)
		// Create simulation
		case r.Method == http.MethodPost && r.URL.Path == "/api/simulations":
			var sim map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&sim)
			id, _ := sim["id"].(string)
			createdSims[id] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(sim)
		// Create rule
		case r.Method == http.MethodPost && r.URL.Path == "/api/rules":
			var rule map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&rule)
			id, _ := rule["id"].(string)
			createdRules[id] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rule)
		default:
			http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, 500)
		}
	}))
	defer admin.Close()

	res := invoke(t, admin.URL, "generate_from_openapi", map[string]any{
		"source": specSrv.URL + "/spec.yaml",
	})
	require.False(t, res.IsError, "unexpected tool error: %v", res.Content)

	assert.True(t, createdSims["get-items-sim"], "expected get-items-sim to be created")
	assert.True(t, createdRules["get-items"], "expected get-items rule to be created")

	text := res.Content[0].(mcpsdk.TextContent).Text
	assert.Contains(t, text, "1 rules created")
	assert.Contains(t, text, "1 simulations created")
}

func TestHandler_GenerateFromOpenAPI_Conflict(t *testing.T) {
	specYAML := `
openapi: "3.0.0"
info:
  title: T
  version: "1"
paths:
  /items:
    get:
      responses:
        "200":
          description: ok
`
	specSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, specYAML)
	}))
	defer specSrv.Close()

	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate that rule and sim already exist (200 on GET).
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "get-items"})
			return
		}
		http.Error(w, "should not be called", 500)
	}))
	defer admin.Close()

	res := invoke(t, admin.URL, "generate_from_openapi", map[string]any{
		"source": specSrv.URL + "/spec.yaml",
	})
	assert.True(t, res.IsError, "expected isError=true for conflicts")

	text := res.Content[0].(mcpsdk.TextContent).Text
	assert.Contains(t, text, "conflict")
	assert.Contains(t, text, "get-items")
}

func TestHandler_GenerateFromOpenAPI_Overwrite(t *testing.T) {
	specYAML := `
openapi: "3.0.0"
info:
  title: T
  version: "1"
paths:
  /items:
    get:
      responses:
        "200":
          description: ok
`
	specSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, specYAML)
	}))
	defer specSrv.Close()

	updatedSims := map[string]bool{}
	updatedRules := map[string]bool{}

	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		// GET returns 200 → already exists → will PUT
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "get-items"})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/simulations/"):
			updatedSims[strings.TrimPrefix(r.URL.Path, "/api/simulations/")] = true
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "get-items-sim"})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/rules/"):
			updatedRules[strings.TrimPrefix(r.URL.Path, "/api/rules/")] = true
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "get-items"})
		default:
			http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, 500)
		}
	}))
	defer admin.Close()

	res := invoke(t, admin.URL, "generate_from_openapi", map[string]any{
		"source":    specSrv.URL + "/spec.yaml",
		"overwrite": true,
	})
	require.False(t, res.IsError, "unexpected error: %v", res.Content)

	assert.True(t, updatedSims["get-items-sim"], "expected get-items-sim to be updated")
	assert.True(t, updatedRules["get-items"], "expected get-items rule to be updated")
}

func TestCaptureFilterArgs(t *testing.T) {
	req := makeReq("any", map[string]any{
		"body":   map[string]any{"$.correlation_id": "abc"},
		"attr":   map[string]any{"tenant": "acme"},
		"query":  map[string]any{"source": "billing"},
		"method": "Publish",
		"limit":  float64(5),
	})
	q := mockwavemcp.CaptureFilterArgs(req)
	if got := q["body"]; len(got) != 1 || got[0] != "$.correlation_id:abc" {
		t.Fatalf("body = %v", q["body"])
	}
	if q.Get("attr") != "tenant:acme" {
		t.Fatalf("attr = %q", q.Get("attr"))
	}
	if q.Get("query") != "source:billing" {
		t.Fatalf("query = %q", q.Get("query"))
	}
	if q.Get("method") != "Publish" {
		t.Fatalf("method = %q", q.Get("method"))
	}
	if q.Get("limit") != "5" {
		t.Fatalf("limit = %q", q.Get("limit"))
	}
	// Absent optional args produce no params.
	empty := mockwavemcp.CaptureFilterArgs(makeReq("any", map[string]any{}))
	if len(empty) != 0 {
		t.Fatalf("empty args should yield no params, got %v", empty)
	}
}

func TestHandler_GenerateFromOpenAPI_Overwrite_NewResource(t *testing.T) {
	specYAML := `
openapi: "3.0.0"
info:
  title: T
  version: "1"
paths:
  /items:
    get:
      responses:
        "200":
          description: ok
`
	specSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, specYAML)
	}))
	defer specSrv.Close()

	createdSims := map[string]bool{}
	createdRules := map[string]bool{}

	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		// GET returns 404 → resource doesn't exist → should POST (create)
		case r.Method == http.MethodGet:
			http.Error(w, "not found", 404)
		case r.Method == http.MethodPost && r.URL.Path == "/api/simulations":
			var sim map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&sim)
			id, _ := sim["id"].(string)
			createdSims[id] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(sim)
		case r.Method == http.MethodPost && r.URL.Path == "/api/rules":
			var rule map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&rule)
			id, _ := rule["id"].(string)
			createdRules[id] = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rule)
		default:
			http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, 500)
		}
	}))
	defer admin.Close()

	res := invoke(t, admin.URL, "generate_from_openapi", map[string]any{
		"source":    specSrv.URL + "/spec.yaml",
		"overwrite": true,
	})
	require.False(t, res.IsError, "unexpected error: %v", res.Content)

	assert.True(t, createdSims["get-items-sim"], "expected get-items-sim to be created (not updated)")
	assert.True(t, createdRules["get-items"], "expected get-items rule to be created (not updated)")
}
