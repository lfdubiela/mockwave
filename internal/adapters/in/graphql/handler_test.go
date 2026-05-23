package graphql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
