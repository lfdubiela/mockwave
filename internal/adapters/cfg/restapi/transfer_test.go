package restapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func transferStore() *memStore {
	return &memStore{
		rules: []domain.Rule{
			{ID: "r1", Name: "One", Match: domain.MatchCriteria{Path: "/one"},
				Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s1"}}},
			{ID: "r2", Name: "Two", Match: domain.MatchCriteria{Path: "/two"},
				Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionForward, ForwardURL: "http://up"}}},
		},
		sims: []domain.Simulation{{ID: "s1", Protocol: "http"}, {ID: "s-orphan", Protocol: "http"}},
	}
}

func TestExport_All(t *testing.T) {
	mux := restapi.NewMux(transferStore(), nil, nil, nil, nil, nil, restapi.WithImportExport())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/export", nil))
	require.Equal(t, 200, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "mockwave-export.json")
	var cfg domain.Config
	require.NoError(t, json.NewDecoder(w.Body).Decode(&cfg))
	assert.Len(t, cfg.Rules, 2)
	// only sims referenced by exported rules — s-orphan excluded
	require.Len(t, cfg.Simulations, 1)
	assert.Equal(t, "s1", cfg.Simulations[0].ID)
}

func TestExport_Subset(t *testing.T) {
	mux := restapi.NewMux(transferStore(), nil, nil, nil, nil, nil, restapi.WithImportExport())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/export?rules=r2", nil))
	require.Equal(t, 200, w.Code)
	var cfg domain.Config
	require.NoError(t, json.NewDecoder(w.Body).Decode(&cfg))
	require.Len(t, cfg.Rules, 1)
	assert.Equal(t, "r2", cfg.Rules[0].ID)
	assert.Empty(t, cfg.Simulations)
}

func TestExport_UnknownID(t *testing.T) {
	mux := restapi.NewMux(transferStore(), nil, nil, nil, nil, nil, restapi.WithImportExport())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/export?rules=r1,ghost", nil))
	assert.Equal(t, 404, w.Code)
	assert.True(t, strings.Contains(w.Body.String(), "ghost"))
}

func TestExport_DisabledOnJSONStore(t *testing.T) {
	mux := restapi.NewMux(transferStore(), nil, nil, nil, nil, nil) // no option
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/export", nil))
	assert.Equal(t, 403, w.Code)
}

func TestExport_MethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(transferStore(), nil, nil, nil, nil, nil, restapi.WithImportExport())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/export", nil))
	assert.Equal(t, 405, w.Code)
}

func TestHealth_ImportExportFlag(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opts    []restapi.MuxOption
		enabled bool
	}{
		{"default disabled", nil, false},
		{"enabled", []restapi.MuxOption{restapi.WithImportExport()}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil, tc.opts...)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/health", nil))
			assert.Equal(t, 200, w.Code)
			var body map[string]interface{}
			require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
			assert.Equal(t, tc.enabled, body["import_export"])
		})
	}
}
