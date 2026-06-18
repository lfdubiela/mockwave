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

// fakeAdminServerWithEventRules starts an httptest server that serves
// /api/event-rules (collection) and /api/event-rules/{id} (item).
func fakeAdminServerWithEventRules() *httptest.Server {
	mux := http.NewServeMux()

	// --- event-rules collection ---
	mux.HandleFunc("/api/event-rules", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			rules := []domain.EventRule{
				{
					ID:   "er1",
					Name: "ER1",
					Match: domain.EventMatch{
						Service: "sns",
						Target:  "arn:aws:sns:*:*:orders",
					},
				},
			}
			json.NewEncoder(w).Encode(rules)
		case http.MethodPost:
			var rule domain.EventRule
			json.NewDecoder(r.Body).Decode(&rule)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(rule)
		}
	})

	// --- event-rules item ---
	mux.HandleFunc("/api/event-rules/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPut:
			var r2 domain.EventRule
			json.NewDecoder(r.Body).Decode(&r2)
			json.NewEncoder(w).Encode(r2)
		case http.MethodDelete:
			w.WriteHeader(204)
		default:
			rule := domain.EventRule{ID: "er1", Name: "ER1", Match: domain.EventMatch{Service: "sns"}}
			json.NewEncoder(w).Encode(rule)
		}
	})

	return httptest.NewServer(mux)
}

// invokeEventRule is a convenience helper that creates a server backed by
// fakeAdminServerWithEventRules and invokes the named tool.
func invokeEventRule(t *testing.T, toolName string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	srv := fakeAdminServerWithEventRules()
	defer srv.Close()
	s := mockwavemcp.NewServer(srv.URL, "test")
	tool := s.GetTool(toolName)
	require.NotNil(t, tool, "tool %s not found", toolName)
	res, err := tool.Handler(context.Background(), makeReq(toolName, args))
	require.NoError(t, err)
	return res
}

// --- client tests ---

func TestClient_ListEventRules(t *testing.T) {
	srv := fakeAdminServerWithEventRules()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	rules, err := c.ListEventRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "er1", rules[0].ID)
}

func TestClient_GetEventRule_Found(t *testing.T) {
	srv := fakeAdminServerWithEventRules()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	rule, err := c.GetEventRule("er1")
	require.NoError(t, err)
	require.NotNil(t, rule)
	assert.Equal(t, "er1", rule.ID)
}

func TestClient_GetEventRule_NotFound(t *testing.T) {
	srv := fakeAdminServerWithEventRules()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	rule, err := c.GetEventRule("no-such-id")
	require.NoError(t, err)
	assert.Nil(t, rule)
}

func TestClient_CreateEventRule(t *testing.T) {
	srv := fakeAdminServerWithEventRules()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	r := domain.EventRule{ID: "er2", Name: "ER2", Match: domain.EventMatch{Service: "sqs"}}
	out, err := c.CreateEventRule(r)
	require.NoError(t, err)
	assert.Equal(t, "er2", out.ID)
}

func TestClient_UpdateEventRule(t *testing.T) {
	srv := fakeAdminServerWithEventRules()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	r := domain.EventRule{ID: "er1", Name: "Updated", Match: domain.EventMatch{Service: "sns"}}
	out, err := c.UpdateEventRule("er1", r)
	require.NoError(t, err)
	assert.Equal(t, "Updated", out.Name)
}

func TestClient_DeleteEventRule(t *testing.T) {
	srv := fakeAdminServerWithEventRules()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.DeleteEventRule("er1")
	require.NoError(t, err)
}

// --- list_event_rules tool ---

func TestHandler_ListEventRules_Success(t *testing.T) {
	res := invokeEventRule(t, "list_event_rules", nil)
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_ListEventRules_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("list_event_rules")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("list_event_rules", nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- get_event_rule tool ---

func TestHandler_GetEventRule_Success(t *testing.T) {
	res := invokeEventRule(t, "get_event_rule", map[string]any{"id": "er1"})
	assert.False(t, res.IsError)
}

func TestHandler_GetEventRule_MissingParam(t *testing.T) {
	res := invokeEventRule(t, "get_event_rule", nil)
	assert.True(t, res.IsError)
}

func TestHandler_GetEventRule_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("get_event_rule")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("get_event_rule", map[string]any{"id": "er1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- create_event_rule tool ---

func TestHandler_CreateEventRule_Success(t *testing.T) {
	res := invokeEventRule(t, "create_event_rule", map[string]any{
		"event_rule": map[string]any{
			"id":    "er2",
			"name":  "ER2",
			"match": map[string]any{"service": "sqs"},
		},
	})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_CreateEventRule_MissingParam(t *testing.T) {
	res := invokeEventRule(t, "create_event_rule", nil)
	assert.True(t, res.IsError)
}

func TestHandler_CreateEventRule_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("create_event_rule")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("create_event_rule", map[string]any{
		"event_rule": map[string]any{"id": "er2"},
	}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- update_event_rule tool ---

func TestHandler_UpdateEventRule_Success(t *testing.T) {
	res := invokeEventRule(t, "update_event_rule", map[string]any{
		"id": "er1",
		"event_rule": map[string]any{
			"id":    "er1",
			"name":  "Updated",
			"match": map[string]any{"service": "sns"},
		},
	})
	assert.False(t, res.IsError, "expected success, got error: %v", res.Content)
}

func TestHandler_UpdateEventRule_MissingID(t *testing.T) {
	res := invokeEventRule(t, "update_event_rule", map[string]any{
		"event_rule": map[string]any{"id": "er1"},
	})
	assert.True(t, res.IsError)
}

func TestHandler_UpdateEventRule_MissingEventRule(t *testing.T) {
	res := invokeEventRule(t, "update_event_rule", map[string]any{"id": "er1"})
	assert.True(t, res.IsError)
}

func TestHandler_UpdateEventRule_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("update_event_rule")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("update_event_rule", map[string]any{
		"id":         "er1",
		"event_rule": map[string]any{"id": "er1"},
	}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- delete_event_rule tool ---

func TestHandler_DeleteEventRule_Success(t *testing.T) {
	res := invokeEventRule(t, "delete_event_rule", map[string]any{"id": "er1"})
	assert.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "deleted")
}

func TestHandler_DeleteEventRule_MissingParam(t *testing.T) {
	res := invokeEventRule(t, "delete_event_rule", nil)
	assert.True(t, res.IsError)
}

func TestHandler_DeleteEventRule_Error(t *testing.T) {
	s := mockwavemcp.NewServer("http://127.0.0.1:1", "test")
	tool := s.GetTool("delete_event_rule")
	require.NotNil(t, tool)
	res, err := tool.Handler(context.Background(), makeReq("delete_event_rule", map[string]any{"id": "er1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}
