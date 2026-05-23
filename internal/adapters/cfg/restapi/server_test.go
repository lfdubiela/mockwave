package restapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
	"github.com/mockwave/mockwave/domain"
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
	return nil, fmt.Errorf("not found")
}
func (m *memStore) SaveRule(r domain.Rule) error             { m.rules = append(m.rules, r); return nil }
func (m *memStore) SaveSimulation(s domain.Simulation) error { m.sims = append(m.sims, s); return nil }
func (m *memStore) DeleteRule(id string) error {
	for i, r := range m.rules {
		if r.ID == id {
			m.rules = append(m.rules[:i], m.rules[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("not found")
}
func (m *memStore) DeleteSimulation(id string) error { return nil }

func TestAdminAPI_GetRules(t *testing.T) {
	store := &memStore{rules: []domain.Rule{{ID: "r1", Match: domain.MatchCriteria{Path: "/foo"}}}}
	mux := restapi.NewMux(store, nil)
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
	mux := restapi.NewMux(store, nil)
	r := domain.Rule{
		ID: "r-new", Match: domain.MatchCriteria{Path: "/bar"},
		Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
	}
	body, _ := json.Marshal(r)
	req := httptest.NewRequest(http.MethodPost, "/api/rules", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 201, w.Code)
}

func TestAdminAPI_Health(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil)
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
	mux := restapi.NewMux(store, func() { reloaded = true })
	req := httptest.NewRequest(http.MethodDelete, "/api/rules/r1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 204, w.Code)
	assert.True(t, reloaded)
}

func TestAdminAPI_DeleteRule_NotFound(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/rules/nope", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 404, w.Code)
}

func TestAdminAPI_PutRule(t *testing.T) {
	store := &memStore{}
	mux := restapi.NewMux(store, nil)
	r := domain.Rule{
		Match:   domain.MatchCriteria{Path: "/bar"},
		Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
	}
	body, _ := json.Marshal(r)
	req := httptest.NewRequest(http.MethodPut, "/api/rules/r-updated", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
}

func TestAdminAPI_PostSimulation(t *testing.T) {
	store := &memStore{}
	mux := restapi.NewMux(store, nil)
	sim := domain.Simulation{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}
	body, _ := json.Marshal(sim)
	req := httptest.NewRequest(http.MethodPost, "/api/simulations", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 201, w.Code)
}

func TestAdminAPI_DeleteSimulation(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/simulations/s1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 204, w.Code)
}

func TestAdminAPI_MethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/rules", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

func TestAdminAPI_PostRule_InvalidJSON(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/rules", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 400, w.Code)
}

func TestAdminAPI_PostRule_InvalidRule(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil)
	// Missing required fields
	r := domain.Rule{ID: "x"}
	body, _ := json.Marshal(r)
	req := httptest.NewRequest(http.MethodPost, "/api/rules", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 422, w.Code)
}

func TestAdminAPI_SimulationsMethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/simulations", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

func TestAdminAPI_SimulationByIDMethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/simulations/s1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

func TestAdminAPI_RuleByIDMethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/rules/r1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

func TestAdminAPI_PutRule_InvalidJSON(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil)
	req := httptest.NewRequest(http.MethodPut, "/api/rules/r1", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 400, w.Code)
}

func TestAdminAPI_PutRule_InvalidRule(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil)
	r := domain.Rule{} // missing required fields
	body, _ := json.Marshal(r)
	req := httptest.NewRequest(http.MethodPut, "/api/rules/r1", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 422, w.Code)
}

func TestAdminAPI_PostSimulation_InvalidJSON(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/simulations", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 400, w.Code)
}

func TestAdminAPI_DeleteSimulation_NotFound(t *testing.T) {
	store := &errorDeleteSimStore{}
	mux := restapi.NewMux(store, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/simulations/s1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 404, w.Code)
}

type errorDeleteSimStore struct{ memStore }

func (e *errorDeleteSimStore) DeleteSimulation(id string) error {
	return fmt.Errorf("not found: %s", id)
}
