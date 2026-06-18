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

// fakeAdminServerWithCaptures starts an httptest server that serves:
//   - GET  /api/event-captures/{rule}        → paginated list
//   - GET  /api/event-captures/{rule}/{id}   → full detail
//   - DELETE /api/event-captures/{rule}      → clear
//
// The stub records the last received request so tests can assert query params.
func fakeAdminServerWithCaptures(t *testing.T) (srv *httptest.Server, lastReq func() *http.Request) {
	t.Helper()

	var last *http.Request

	mux := http.NewServeMux()

	// /api/event-captures/ — handles both /{rule} and /{rule}/{id}
	mux.HandleFunc("/api/event-captures/", func(w http.ResponseWriter, r *http.Request) {
		last = r

		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/api/event-captures/")
		parts := strings.SplitN(path, "/", 2)

		switch {
		case r.Method == http.MethodDelete && len(parts) == 1:
			// DELETE /api/event-captures/{rule}
			w.WriteHeader(204)
		case r.Method == http.MethodGet && len(parts) == 2:
			// GET /api/event-captures/{rule}/{id}
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
			// GET /api/event-captures/{rule}?...
			page := matched.Page{
				Items: []matched.Request{
					{ID: "cap1", RuleID: parts[0], At: time.Now(), Method: "POST", Path: "/foo"},
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

// invokeCapture builds a server backed by the captures stub and invokes the named tool.
func invokeCapture(t *testing.T, toolName string, args map[string]any) (*mcpsdk.CallToolResult, func() *http.Request) {
	t.Helper()
	srv, lastReq := fakeAdminServerWithCaptures(t)
	t.Cleanup(srv.Close)
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool(toolName)
	require.NotNil(t, tool, "tool %s not found", toolName)
	res, err := tool.Handler(context.Background(), makeReq(toolName, args))
	require.NoError(t, err)
	return res, lastReq
}

// --- client unit tests ---

func TestClient_ListEventCaptures(t *testing.T) {
	srv, _ := fakeAdminServerWithCaptures(t)
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	page, err := c.ListEventCaptures("rule1", nil)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "cap1", page.Items[0].ID)
}

func TestClient_ListEventCaptures_WithQuery(t *testing.T) {
	srv, lastReq := fakeAdminServerWithCaptures(t)
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)

	q := map[string][]string{"body": {"$.x:v"}}
	page, err := c.ListEventCaptures("rule1", q)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	r := lastReq()
	require.NotNil(t, r)
	got := r.URL.Query()
	assert.Equal(t, []string{"$.x:v"}, got["body"])
}

func TestClient_GetEventCapture(t *testing.T) {
	srv, _ := fakeAdminServerWithCaptures(t)
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	full, err := c.GetEventCapture("rule1", "cap1")
	require.NoError(t, err)
	require.NotNil(t, full)
	assert.Equal(t, "cap1", full.ID)
}

func TestClient_ClearEventCaptures(t *testing.T) {
	srv, _ := fakeAdminServerWithCaptures(t)
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.ClearEventCaptures("rule1")
	require.NoError(t, err)
}

// --- list_event_captures tool ---

func TestHandler_ListEventCaptures_Success(t *testing.T) {
	res, _ := invokeCapture(t, "list_event_captures", map[string]any{"rule": "rule1"})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_ListEventCaptures_WithBodyFilter(t *testing.T) {
	res, lastReq := invokeCapture(t, "list_event_captures", map[string]any{
		"rule": "rule1",
		"body": map[string]any{"$.correlation_id": "abc"},
	})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)

	r := lastReq()
	require.NotNil(t, r)
	// The stub received the body filter as a query param
	bodyParams := r.URL.Query()["body"]
	require.Len(t, bodyParams, 1)
	assert.Equal(t, "$.correlation_id:abc", bodyParams[0])
}

func TestHandler_ListEventCaptures_MissingRule(t *testing.T) {
	srv, _ := fakeAdminServerWithCaptures(t)
	defer srv.Close()
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool("list_event_captures")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("list_event_captures", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestHandler_ListEventCaptures_ReturnsPageItems(t *testing.T) {
	res, _ := invokeCapture(t, "list_event_captures", map[string]any{"rule": "rule1"})
	assert.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "cap1")
}

func TestHandler_ListEventCaptures_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("list_event_captures")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("list_event_captures", map[string]any{"rule": "rule1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- get_event_capture tool ---

func TestHandler_GetEventCapture_Success(t *testing.T) {
	res, _ := invokeCapture(t, "get_event_capture", map[string]any{"rule": "rule1", "id": "cap1"})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_GetEventCapture_ReturnsFullRequest(t *testing.T) {
	res, _ := invokeCapture(t, "get_event_capture", map[string]any{"rule": "rule1", "id": "cap1"})
	assert.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "cap1")
}

func TestHandler_GetEventCapture_MissingRule(t *testing.T) {
	srv, _ := fakeAdminServerWithCaptures(t)
	defer srv.Close()
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool("get_event_capture")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("get_event_capture", map[string]any{"id": "cap1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestHandler_GetEventCapture_MissingID(t *testing.T) {
	srv, _ := fakeAdminServerWithCaptures(t)
	defer srv.Close()
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool("get_event_capture")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("get_event_capture", map[string]any{"rule": "rule1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestHandler_GetEventCapture_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("get_event_capture")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("get_event_capture", map[string]any{"rule": "rule1", "id": "cap1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- clear_event_captures tool ---

func TestHandler_ClearEventCaptures_Success(t *testing.T) {
	res, _ := invokeCapture(t, "clear_event_captures", map[string]any{"rule": "rule1"})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "cleared")
}

func TestHandler_ClearEventCaptures_MissingRule(t *testing.T) {
	srv, _ := fakeAdminServerWithCaptures(t)
	defer srv.Close()
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool("clear_event_captures")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("clear_event_captures", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestHandler_ClearEventCaptures_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("clear_event_captures")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("clear_event_captures", map[string]any{"rule": "rule1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}
