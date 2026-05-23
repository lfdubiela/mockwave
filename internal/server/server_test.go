package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubStore struct{}

func (s *stubStore) GetRules() ([]domain.Rule, error)                    { return nil, nil }
func (s *stubStore) GetSimulation(id string) (*domain.Simulation, error) { return nil, fmt.Errorf("nope") }
func (s *stubStore) ListSimulations() ([]domain.Simulation, error)       { return nil, nil }
func (s *stubStore) SaveRule(r domain.Rule) error                        { return nil }
func (s *stubStore) SaveSimulation(sim domain.Simulation) error          { return nil }
func (s *stubStore) DeleteRule(id string) error                          { return nil }
func (s *stubStore) DeleteSimulation(id string) error                    { return nil }

func TestServer_BuildsPipeline(t *testing.T) {
	srv, err := server.New(server.Config{Store: &stubStore{}})
	require.NoError(t, err)
	assert.NotNil(t, srv)
}

func TestServer_NilStoreReturnsError(t *testing.T) {
	_, err := server.New(server.Config{Store: nil})
	assert.Error(t, err)
}

func TestServer_Rebuild(t *testing.T) {
	srv, err := server.New(server.Config{Store: &stubStore{}})
	require.NoError(t, err)
	assert.NoError(t, srv.Rebuild())
}

func TestServer_HTTPHandler_NotNil(t *testing.T) {
	srv, err := server.New(server.Config{Store: &stubStore{}})
	require.NoError(t, err)
	h := srv.HTTPHandler()
	assert.NotNil(t, h)
}

func TestServer_HTTPHandler_ServesRequest(t *testing.T) {
	store := &stubStoreWithData{}
	srv, err := server.New(server.Config{Store: store})
	require.NoError(t, err)
	h := srv.HTTPHandler()
	require.NotNil(t, h)
	// Actually execute a request through the server (exercises pipelineProxy.Execute)
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
}

type stubStoreWithData struct{}

func (s *stubStoreWithData) GetRules() ([]domain.Rule, error) {
	return []domain.Rule{{
		ID:      "r1",
		Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/ping"},
		Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
	}}, nil
}
func (s *stubStoreWithData) GetSimulation(id string) (*domain.Simulation, error) {
	if id == "s1" {
		return &domain.Simulation{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}, nil
	}
	return nil, fmt.Errorf("not found")
}
func (s *stubStoreWithData) ListSimulations() ([]domain.Simulation, error) { return nil, nil }
func (s *stubStoreWithData) SaveRule(r domain.Rule) error                  { return nil }
func (s *stubStoreWithData) SaveSimulation(sim domain.Simulation) error    { return nil }
func (s *stubStoreWithData) DeleteRule(id string) error                    { return nil }
func (s *stubStoreWithData) DeleteSimulation(id string) error              { return nil }

func newStubStore() *stubStore { return &stubStore{} }

func TestServer_MockHandler_RoutesGraphQL(t *testing.T) {
	store := newStubStore()
	srv, err := server.New(server.Config{Store: store})
	require.NoError(t, err)

	h := srv.MockHandler([]string{"http", "graphql"}, srv.NewProxy())

	body := `{"query":"query GetItem { item { id } }","operationName":"GetItem"}`
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// Pipeline returns 404 (no rules), but format is GraphQL errors JSON
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Contains(t, resp, "errors")
}

func TestServer_MockHandler_RoutesSOAP(t *testing.T) {
	store := newStubStore()
	srv, err := server.New(server.Config{Store: store})
	require.NoError(t, err)

	h := srv.MockHandler([]string{"http", "soap"}, srv.NewProxy())

	req := httptest.NewRequest(http.MethodPost, "/service", strings.NewReader(`<soap:Envelope/>`))
	req.Header.Set("SOAPAction", "GetItem")
	req.Header.Set("Content-Type", "text/xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// Pipeline returns 404 as SOAP fault
	assert.Contains(t, w.Header().Get("Content-Type"), "text/xml")
	assert.Contains(t, w.Body.String(), "soap:Fault")
}

func TestServer_MockHandler_DefaultsToHTTP(t *testing.T) {
	store := newStubStore()
	srv, err := server.New(server.Config{Store: store})
	require.NoError(t, err)

	h := srv.MockHandler([]string{"http"}, srv.NewProxy())

	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// HTTP handler responds with JSON error for no-match
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
}
