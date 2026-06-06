package restapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/metrics"
	"github.com/mockwave/mockwave/internal/unmatched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memStore struct {
	rules []domain.Rule
	sims  []domain.Simulation
}

func (m *memStore) GetRules() ([]domain.Rule, error) { return m.rules, nil }
func (m *memStore) GetSimulation(id string) (*domain.Simulation, error) {
	for _, s := range m.sims {
		if s.ID == id {
			ss := s
			return &ss, nil
		}
	}
	return nil, nil
}
func (m *memStore) SaveRule(r domain.Rule) error             { m.rules = append(m.rules, r); return nil }
func (m *memStore) SaveSimulation(s domain.Simulation) error {
	for i, existing := range m.sims {
		if existing.ID == s.ID {
			m.sims[i] = s
			return nil
		}
	}
	m.sims = append(m.sims, s)
	return nil
}
func (m *memStore) DeleteRule(id string) error {
	for i, r := range m.rules {
		if r.ID == id {
			m.rules = append(m.rules[:i], m.rules[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("not found")
}
func (m *memStore) ListSimulations() ([]domain.Simulation, error) { return m.sims, nil }
func (m *memStore) DeleteSimulation(id string) error              { return nil }

func TestAdminAPI_GetRules(t *testing.T) {
	store := &memStore{rules: []domain.Rule{{ID: "r1", Match: domain.MatchCriteria{Path: "/foo"}}}}
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/rules", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	var rules []domain.Rule
	require.NoError(t, json.NewDecoder(w.Body).Decode(&rules))
	require.Len(t, rules, 1)
	assert.Equal(t, "r1", rules[0].ID)
}

func TestAdminAPI_PostRule(t *testing.T) {
	store := &memStore{}
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	r := domain.Rule{
		ID: "r-new", Match: domain.MatchCriteria{Path: "/bar"},
		Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s1"}},
	}
	body, _ := json.Marshal(r)
	req := httptest.NewRequest(http.MethodPost, "/api/rules", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 201, w.Code)
}

func TestAdminAPI_PostRule_WeightsNotSumming100(t *testing.T) {
	store := &memStore{}
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	r := domain.Rule{
		ID: "r-bad", Match: domain.MatchCriteria{Path: "/bar"},
		Buckets: []domain.WeightedBucket{
			{Weight: 60, Action: domain.ActionSimulate, SimulationID: "s1"},
			{Weight: 60, Action: domain.ActionSimulate, SimulationID: "s2"},
		},
	}
	body, _ := json.Marshal(r)
	req := httptest.NewRequest(http.MethodPost, "/api/rules", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 422, w.Code)
	assert.Contains(t, w.Body.String(), "sum to 100")
}

func TestAdminAPI_Health(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
}

func TestAdminAPI_DeleteRule(t *testing.T) {
	store := &memStore{rules: []domain.Rule{
		{ID: "r1", Match: domain.MatchCriteria{Path: "/foo"}, Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}}},
	}}
	reloaded := false
	mux := restapi.NewMux(store, func() { reloaded = true }, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/rules/r1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 204, w.Code)
	assert.True(t, reloaded)
}

func TestAdminAPI_DeleteRule_NotFound(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/rules/nope", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 404, w.Code)
}

func TestAdminAPI_PutRule(t *testing.T) {
	store := &memStore{}
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	r := domain.Rule{
		Match:   domain.MatchCriteria{Path: "/bar"},
		Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s1"}},
	}
	body, _ := json.Marshal(r)
	req := httptest.NewRequest(http.MethodPut, "/api/rules/r-updated", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
}

func TestAdminAPI_PostSimulation(t *testing.T) {
	store := &memStore{}
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	sim := domain.Simulation{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}
	body, _ := json.Marshal(sim)
	req := httptest.NewRequest(http.MethodPost, "/api/simulations", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 201, w.Code)
}

func TestAdminAPI_DeleteSimulation(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/simulations/s1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 204, w.Code)
}

func TestAdminAPI_MethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/rules", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

func TestAdminAPI_PostRule_InvalidJSON(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/rules", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 400, w.Code)
}

func TestAdminAPI_PostRule_InvalidRule(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	// Missing required fields
	r := domain.Rule{ID: "x"}
	body, _ := json.Marshal(r)
	req := httptest.NewRequest(http.MethodPost, "/api/rules", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 422, w.Code)
}

func TestAdminAPI_GetSimulations(t *testing.T) {
	store := &memStore{sims: []domain.Simulation{
		{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}},
	}}
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/simulations", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	var sims []domain.Simulation
	require.NoError(t, json.NewDecoder(w.Body).Decode(&sims))
	require.Len(t, sims, 1)
	assert.Equal(t, "s1", sims[0].ID)
}

func TestAdminAPI_SimulationByIDMethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPatch, "/api/simulations/s1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

func TestAdminAPI_RuleByIDMethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPatch, "/api/rules/r1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

func TestAdminAPI_PutRule_InvalidJSON(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPut, "/api/rules/r1", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 400, w.Code)
}

func TestAdminAPI_PutRule_InvalidRule(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	r := domain.Rule{} // missing required fields
	body, _ := json.Marshal(r)
	req := httptest.NewRequest(http.MethodPut, "/api/rules/r1", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 422, w.Code)
}

func TestAdminAPI_PostSimulation_InvalidJSON(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/simulations", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 400, w.Code)
}

func TestAdminAPI_DeleteSimulation_NotFound(t *testing.T) {
	store := &errorDeleteSimStore{}
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/simulations/s1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 404, w.Code)
}

type errorDeleteSimStore struct{ memStore }

func (e *errorDeleteSimStore) DeleteSimulation(id string) error {
	return fmt.Errorf("not found: %s", id)
}

func TestAdminAPI_MetricsSnapshot_NilCollector(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
}

func TestAdminAPI_MetricsSnapshot_WithCollector(t *testing.T) {
	col := metrics.NewCollector()
	col.RecordHit("r1", "Rule One", 5.0)
	mux := restapi.NewMux(&memStore{}, nil, col, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	var snap metrics.Snapshot
	require.NoError(t, json.NewDecoder(w.Body).Decode(&snap))
	assert.Equal(t, int64(1), snap.TotalRequests)
}

func TestAdminAPI_Unmatched_GetEmpty(t *testing.T) {
	buf := unmatched.NewBuffer(10)
	mux := restapi.NewMux(&memStore{}, nil, nil, buf, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/unmatched", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
}

func TestAdminAPI_Unmatched_Delete(t *testing.T) {
	buf := unmatched.NewBuffer(10)
	buf.Add(unmatched.Request{Method: "GET", Path: "/x"})
	mux := restapi.NewMux(&memStore{}, nil, nil, buf, nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/unmatched", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 204, w.Code)
	assert.Empty(t, buf.List())
}

func TestAdminAPI_MetricsMethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/metrics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

func TestAdminAPI_MetricsStream_NilBroker(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics/stream", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestAdminAPI_MetricsStream_WithBroker(t *testing.T) {
	col := metrics.NewCollector()
	buf := unmatched.NewBuffer(10)
	broker := metrics.NewBroker(col)
	mux := restapi.NewMux(&memStore{}, nil, col, buf, broker, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/metrics/stream", nil)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
}

func TestAdminAPI_ServesUI(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "⚡ Mockwave")
	// Phase 6 elements
	assert.Contains(t, body, "data-tab=\"dashboard\"")
	assert.Contains(t, body, "Unmatched Requests")
	assert.Contains(t, body, "buckets-container")
	assert.Contains(t, body, "crypto.randomUUID")
	assert.Contains(t, body, "stats-rail")
	assert.Contains(t, body, "rail-total")
	assert.Contains(t, body, "main-layout")
	assert.Contains(t, body, "req-chart")
	assert.Contains(t, body, "chart-skeleton")
	assert.Contains(t, body, "loadChart")
	assert.Contains(t, body, "runScript")
	assert.Contains(t, body, "updatePathChips")
	assert.Contains(t, body, "editor-wrap")
	assert.Contains(t, body, "setButtonState")
	assert.Contains(t, body, "btn-save-rule")
	assert.Contains(t, body, "updateWeightSum")
	assert.Contains(t, body, "weight-sum-indicator")
}

func TestAdminAPI_GetRuleByID(t *testing.T) {
	store := &memStore{rules: []domain.Rule{
		{ID: "r1", Name: "R1", Match: domain.MatchCriteria{Path: "/foo"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}}},
	}}
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/rules/r1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	var rule domain.Rule
	require.NoError(t, json.NewDecoder(w.Body).Decode(&rule))
	assert.Equal(t, "r1", rule.ID)
}

func TestAdminAPI_GetRuleByID_NotFound(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/rules/nope", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 404, w.Code)
}

func TestAdminAPI_GetSimulationByID(t *testing.T) {
	store := &memStore{sims: []domain.Simulation{
		{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}},
	}}
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/simulations/s1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	var sim domain.Simulation
	require.NoError(t, json.NewDecoder(w.Body).Decode(&sim))
	assert.Equal(t, "s1", sim.ID)
}

func TestAdminAPI_GetSimulationByID_NotFound(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/simulations/nope", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 404, w.Code)
}

func TestAdminAPI_PutSimulation(t *testing.T) {
	store := &memStore{sims: []domain.Simulation{
		{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}},
	}}
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil)
	updated := domain.Simulation{Protocol: "http", Response: domain.HTTPResponse{Status: 201}}
	body, _ := json.Marshal(updated)
	req := httptest.NewRequest(http.MethodPut, "/api/simulations/s1", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	var sim domain.Simulation
	require.NoError(t, json.NewDecoder(w.Body).Decode(&sim))
	assert.Equal(t, "s1", sim.ID)
	assert.Equal(t, 201, sim.Response.Status)
}

func TestAdminAPI_PostReload(t *testing.T) {
	reloaded := false
	mux := restapi.NewMux(&memStore{}, func() { reloaded = true }, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/reload", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 204, w.Code)
	assert.True(t, reloaded)
}

func TestAdminAPI_OpenAPI(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	body := w.Body.String()
	assert.Contains(t, body, "openapi")
	assert.Contains(t, body, "/api/rules")
}

func TestAdminAPI_OpenAPI_MethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/openapi.json", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

func TestAdminAPI_MetricsHistory_NilCollector(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics/history", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	var body struct {
		Rules []interface{} `json:"rules"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.NotNil(t, body.Rules) // must be [] not null
	assert.Empty(t, body.Rules)
}

func TestAdminAPI_MetricsHistory_WithCollector(t *testing.T) {
	col := metrics.NewCollector()
	col.RecordHit("r1", "Rule 1", 5.0)
	col.RecordHit("r1", "Rule 1", 3.0)
	mux := restapi.NewMux(&memStore{}, nil, col, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics/history", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	var body struct {
		Rules []metrics.RuleSeries `json:"rules"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.Len(t, body.Rules, 1)
	assert.Equal(t, "r1", body.Rules[0].RuleID)
	var total int64
	for _, b := range body.Rules[0].Buckets {
		total += b.Count
	}
	assert.Equal(t, int64(2), total)
}

func TestAdminAPI_MetricsHistory_MethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/metrics/history", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

func TestAdminAPI_ScriptEval_Stub(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/script/eval", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 503, w.Code)
}

func TestAdminAPI_MetricsHistory_PerRule(t *testing.T) {
	col := metrics.NewCollector()
	col.RecordHit("r1", "Rule One", 2)
	col.RecordHit("r1", "Rule One", 3)
	col.RecordHit("r2", "Rule Two", 1)

	mux := restapi.NewMux(&memStore{}, nil, col, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics/history", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)

	var body struct {
		Rules []struct {
			RuleID   string `json:"rule_id"`
			RuleName string `json:"rule_name"`
			Buckets  []struct {
				Count int64 `json:"count"`
			} `json:"buckets"`
		} `json:"rules"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.Len(t, body.Rules, 2)
	assert.Equal(t, "r1", body.Rules[0].RuleID) // busiest first
	assert.Equal(t, "Rule One", body.Rules[0].RuleName)
	var r1 int64
	for _, b := range body.Rules[0].Buckets {
		r1 += b.Count
	}
	assert.Equal(t, int64(2), r1)
}

func TestAdminAPI_MetricsHistory_EmptyRules(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, metrics.NewCollector(), nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/metrics/history", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"rules":[]}`, w.Body.String())
}
