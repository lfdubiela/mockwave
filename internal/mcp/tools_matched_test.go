package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mockwave/mockwave/internal/matched"
	mockwavemcp "github.com/mockwave/mockwave/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdminServerWithMatched starts an httptest server that serves:
//   - GET    /api/matched/{rule}        → paginated list
//   - GET    /api/matched/{rule}/{id}   → full detail
//   - DELETE /api/matched/{rule}        → clear
//
// The stub records the last received request so tests can assert query params.
func fakeAdminServerWithMatched(t *testing.T) (srv *httptest.Server, lastReq func() *http.Request) {
	t.Helper()

	var last *http.Request

	mux := http.NewServeMux()

	// /api/matched/ — handles both /{rule} and /{rule}/{id}
	mux.HandleFunc("/api/matched/", func(w http.ResponseWriter, r *http.Request) {
		last = r

		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/api/matched/")
		parts := strings.SplitN(path, "/", 2)

		switch {
		case r.Method == http.MethodDelete && len(parts) == 1:
			// DELETE /api/matched/{rule}
			w.WriteHeader(204)
		case r.Method == http.MethodGet && len(parts) == 2:
			// GET /api/matched/{rule}/{id}
			full := matched.FullRequest{
				Request: matched.Request{
					ID:     parts[1],
					RuleID: parts[0],
					At:     time.Now(),
					Method: "POST",
					Path:   "/foo",
				},
			}
			json.NewEncoder(w).Encode(full)
		case r.Method == http.MethodGet && len(parts) == 1:
			// GET /api/matched/{rule}?...
			page := matched.Page{
				Items: []matched.Request{
					{ID: "match1", RuleID: parts[0], At: time.Now(), Method: "POST", Path: "/foo"},
				},
			}
			json.NewEncoder(w).Encode(page)
		default:
			http.Error(w, "unexpected", 500)
		}
	})

	srv = httptest.NewServer(mux)
	return srv, func() *http.Request { return last }
}

// invokeMatched builds a server backed by the matched stub and invokes the named tool.
func invokeMatched(t *testing.T, toolName string, args map[string]any) (*mcpsdk.CallToolResult, func() *http.Request) {
	t.Helper()
	srv, lastReq := fakeAdminServerWithMatched(t)
	t.Cleanup(srv.Close)
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool(toolName)
	require.NotNil(t, tool, "tool %s not found", toolName)
	res, err := tool.Handler(context.Background(), makeReq(toolName, args))
	require.NoError(t, err)
	return res, lastReq
}

// --- client unit tests ---

func TestClient_ListMatched(t *testing.T) {
	srv, _ := fakeAdminServerWithMatched(t)
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	page, err := c.ListMatched("rule1", nil)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "match1", page.Items[0].ID)
}

func TestClient_ListMatched_WithQuery(t *testing.T) {
	srv, lastReq := fakeAdminServerWithMatched(t)
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)

	q := map[string][]string{"body": {"$.x:v"}}
	page, err := c.ListMatched("rule1", q)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	r := lastReq()
	require.NotNil(t, r)
	got := r.URL.Query()
	assert.Equal(t, []string{"$.x:v"}, got["body"])
}

func TestClient_GetMatched(t *testing.T) {
	srv, _ := fakeAdminServerWithMatched(t)
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	full, err := c.GetMatched("rule1", "match1")
	require.NoError(t, err)
	require.NotNil(t, full)
	assert.Equal(t, "match1", full.ID)
}

func TestClient_ClearMatched(t *testing.T) {
	srv, _ := fakeAdminServerWithMatched(t)
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.ClearMatched("rule1")
	require.NoError(t, err)
}

// --- list_matched tool ---

func TestHandler_ListMatched_Success(t *testing.T) {
	res, _ := invokeMatched(t, "list_matched", map[string]any{"rule": "rule1"})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_ListMatched_WithBodyFilter(t *testing.T) {
	res, lastReq := invokeMatched(t, "list_matched", map[string]any{
		"rule": "rule1",
		"body": map[string]any{"$.x": "v"},
	})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)

	r := lastReq()
	require.NotNil(t, r)
	// The stub received the body filter as a query param
	bodyParams := r.URL.Query()["body"]
	require.Len(t, bodyParams, 1)
	assert.Equal(t, "$.x:v", bodyParams[0])
}

func TestHandler_ListMatched_MissingRule(t *testing.T) {
	srv, _ := fakeAdminServerWithMatched(t)
	defer srv.Close()
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool("list_matched")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("list_matched", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestHandler_ListMatched_ReturnsPageItems(t *testing.T) {
	res, _ := invokeMatched(t, "list_matched", map[string]any{"rule": "rule1"})
	assert.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "match1")
}

func TestHandler_ListMatched_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("list_matched")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("list_matched", map[string]any{"rule": "rule1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- get_matched tool ---

func TestHandler_GetMatched_Success(t *testing.T) {
	res, _ := invokeMatched(t, "get_matched", map[string]any{"rule": "rule1", "id": "match1"})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_GetMatched_ReturnsFullRequest(t *testing.T) {
	res, _ := invokeMatched(t, "get_matched", map[string]any{"rule": "rule1", "id": "match1"})
	assert.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "match1")
}

func TestHandler_GetMatched_MissingRule(t *testing.T) {
	srv, _ := fakeAdminServerWithMatched(t)
	defer srv.Close()
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool("get_matched")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("get_matched", map[string]any{"id": "match1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestHandler_GetMatched_MissingID(t *testing.T) {
	srv, _ := fakeAdminServerWithMatched(t)
	defer srv.Close()
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool("get_matched")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("get_matched", map[string]any{"rule": "rule1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestHandler_GetMatched_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("get_matched")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("get_matched", map[string]any{"rule": "rule1", "id": "match1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- clear_matched tool ---

func TestHandler_ClearMatched_Success(t *testing.T) {
	res, _ := invokeMatched(t, "clear_matched", map[string]any{"rule": "rule1"})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "cleared")
}

func TestHandler_ClearMatched_MissingRule(t *testing.T) {
	srv, _ := fakeAdminServerWithMatched(t)
	defer srv.Close()
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool("clear_matched")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("clear_matched", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestHandler_ClearMatched_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("clear_matched")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("clear_matched", map[string]any{"rule": "rule1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}
