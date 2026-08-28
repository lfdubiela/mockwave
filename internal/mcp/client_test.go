package mcp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mockwave/mockwave/domain"
	mockwavemcp "github.com/mockwave/mockwave/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeAdminServer() *httptest.Server {
	mux := http.NewServeMux()

	// Rules
	mux.HandleFunc("/api/rules", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			rules := []domain.Rule{{ID: "r1", Name: "R1", Match: domain.MatchCriteria{Path: "/foo"}}}
			json.NewEncoder(w).Encode(rules)
		case http.MethodPost:
			var rule domain.Rule
			json.NewDecoder(r.Body).Decode(&rule)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(rule)
		}
	})
	mux.HandleFunc("/api/rules/", func(w http.ResponseWriter, r *http.Request) {
		rule := domain.Rule{ID: "r1", Match: domain.MatchCriteria{Path: "/foo"}}
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(rule)
		case http.MethodPut:
			var r2 domain.Rule
			json.NewDecoder(r.Body).Decode(&r2)
			json.NewEncoder(w).Encode(r2)
		case http.MethodDelete:
			w.WriteHeader(204)
		}
	})

	// Simulations
	mux.HandleFunc("/api/simulations", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			sims := []domain.Simulation{{ID: "s1", Protocol: "http"}}
			json.NewEncoder(w).Encode(sims)
		case http.MethodPost:
			var sim domain.Simulation
			json.NewDecoder(r.Body).Decode(&sim)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(sim)
		}
	})
	mux.HandleFunc("/api/simulations/", func(w http.ResponseWriter, r *http.Request) {
		sim := domain.Simulation{ID: "s1", Protocol: "http"}
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(sim)
		case http.MethodPut:
			var s2 domain.Simulation
			json.NewDecoder(r.Body).Decode(&s2)
			json.NewEncoder(w).Encode(s2)
		case http.MethodDelete:
			w.WriteHeader(204)
		}
	})

	// Observability
	mux.HandleFunc("/api/metrics", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"total_requests": 5})
	})
	mux.HandleFunc("/api/unmatched", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		json.NewEncoder(w).Encode([]any{})
	})
	mux.HandleFunc("/api/reload", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	return httptest.NewServer(mux)
}

func TestClient_ListRules(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	rules, err := c.ListRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "r1", rules[0].ID)
}

func TestClient_GetRule(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	rule, err := c.GetRule("r1")
	require.NoError(t, err)
	assert.Equal(t, "r1", rule.ID)
}

func TestClient_CreateRule(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	r := domain.Rule{ID: "r2", Match: domain.MatchCriteria{Path: "/bar"}}
	out, err := c.CreateRule(r)
	require.NoError(t, err)
	assert.Equal(t, "r2", out.ID)
}

func TestClient_UpdateRule(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	r := domain.Rule{ID: "r1", Match: domain.MatchCriteria{Path: "/updated"}}
	out, err := c.UpdateRule("r1", r)
	require.NoError(t, err)
	assert.Equal(t, "/updated", out.Match.Path)
}

func TestClient_DeleteRule(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.DeleteRule("r1")
	require.NoError(t, err)
}

func TestClient_ListSimulations(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	sims, err := c.ListSimulations()
	require.NoError(t, err)
	require.Len(t, sims, 1)
	assert.Equal(t, "s1", sims[0].ID)
}

func TestClient_GetSimulation(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	sim, err := c.GetSimulation("s1")
	require.NoError(t, err)
	assert.Equal(t, "s1", sim.ID)
}

func TestClient_CreateSimulation(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	s := domain.Simulation{ID: "s2", Protocol: "http"}
	out, err := c.CreateSimulation(s)
	require.NoError(t, err)
	assert.Equal(t, "s2", out.ID)
}

func TestClient_UpdateSimulation(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	s := domain.Simulation{Protocol: "graphql"}
	out, err := c.UpdateSimulation("s1", s)
	require.NoError(t, err)
	assert.Equal(t, "graphql", out.Protocol)
}

func TestClient_DeleteSimulation(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.DeleteSimulation("s1")
	require.NoError(t, err)
}

func TestClient_GetMetrics(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	raw, err := c.GetMetrics()
	require.NoError(t, err)
	assert.Contains(t, string(raw), "total_requests")
}

func TestClient_ListUnmatched(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	raw, err := c.ListUnmatched()
	require.NoError(t, err)
	assert.NotNil(t, raw)
}

func TestClient_ClearUnmatched(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.ClearUnmatched()
	require.NoError(t, err)
}

func TestClient_Reload(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.Reload()
	require.NoError(t, err)
}

func TestClient_Health(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.Health()
	require.NoError(t, err)
}

func TestClient_Health_Unreachable(t *testing.T) {
	c := mockwavemcp.NewClient("http://127.0.0.1:1")
	err := c.Health()
	assert.Error(t, err)
}
