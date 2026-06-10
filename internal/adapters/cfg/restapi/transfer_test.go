package restapi_test

import (
	"bytes"
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

func previewReq(t *testing.T, mux http.Handler, cfg domain.Config) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(cfg)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/import/preview", bytes.NewReader(body)))
	return w
}

func validRule(id, path string) domain.Rule {
	return domain.Rule{ID: id, Name: id, Match: domain.MatchCriteria{Path: path},
		Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionForward, ForwardURL: "http://up"}}}
}

func TestImportPreview_Conflicts(t *testing.T) {
	store := transferStore() // has r1 /one, r2 /two
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil, restapi.WithImportExport())

	w := previewReq(t, mux, domain.Config{Rules: []domain.Rule{
		validRule("r1", "/changed"), // same id, different match → "id"
		validRule("r-new", "/two"),  // different id, same match as r2 → "match"
		validRule("r-ok", "/fresh"), // clean
	}})
	require.Equal(t, 200, w.Code)
	var resp struct {
		Importable int `json:"importable"`
		Conflicts  []struct {
			Reason   string `json:"reason"`
			Incoming struct{ ID, Name string }
			Existing struct{ ID, Name string }
		} `json:"conflicts"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 1, resp.Importable)
	require.Len(t, resp.Conflicts, 2)
	assert.Equal(t, "id", resp.Conflicts[0].Reason)
	assert.Equal(t, "r1", resp.Conflicts[0].Existing.ID)
	assert.Equal(t, "match", resp.Conflicts[1].Reason)
	assert.Equal(t, "r2", resp.Conflicts[1].Existing.ID)
	assert.Equal(t, "r-new", resp.Conflicts[1].Incoming.ID)
}

func TestImportPreview_IDAndMatchReportsID(t *testing.T) {
	mux := restapi.NewMux(transferStore(), nil, nil, nil, nil, nil, restapi.WithImportExport())
	r := validRule("r1", "/one") // same id AND same match
	w := previewReq(t, mux, domain.Config{Rules: []domain.Rule{r}})
	require.Equal(t, 200, w.Code)
	var resp struct {
		Conflicts []struct{ Reason string } `json:"conflicts"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Conflicts, 1)
	assert.Equal(t, "id", resp.Conflicts[0].Reason)
}

func TestImportPreview_Validation422(t *testing.T) {
	mux := restapi.NewMux(transferStore(), nil, nil, nil, nil, nil, restapi.WithImportExport())
	for name, cfg := range map[string]domain.Config{
		"invalid rule": {Rules: []domain.Rule{{ID: "bad"}}}, // no path/buckets
		"dangling sim ref": {Rules: []domain.Rule{{ID: "rx", Match: domain.MatchCriteria{Path: "/x"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "nowhere"}}}}},
		"internal dup id":    {Rules: []domain.Rule{validRule("dup", "/a"), validRule("dup", "/b")}},
		"internal dup match": {Rules: []domain.Rule{validRule("a", "/same"), validRule("b", "/same")}},
	} {
		t.Run(name, func(t *testing.T) {
			w := previewReq(t, mux, cfg)
			assert.Equal(t, 422, w.Code)
		})
	}
}

func TestImportPreview_SimRefResolvesInStore(t *testing.T) {
	// bucket references s1 which exists in the store (not in payload) — valid
	mux := restapi.NewMux(transferStore(), nil, nil, nil, nil, nil, restapi.WithImportExport())
	cfg := domain.Config{Rules: []domain.Rule{{ID: "rx", Match: domain.MatchCriteria{Path: "/x"},
		Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s1"}}}}}
	w := previewReq(t, mux, cfg)
	assert.Equal(t, 200, w.Code)
}

func TestImportPreview_Disabled403(t *testing.T) {
	mux := restapi.NewMux(transferStore(), nil, nil, nil, nil, nil)
	w := previewReq(t, mux, domain.Config{})
	assert.Equal(t, 403, w.Code)
}

func TestImportPreview_BadJSON400(t *testing.T) {
	mux := restapi.NewMux(transferStore(), nil, nil, nil, nil, nil, restapi.WithImportExport())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/import/preview", strings.NewReader("{nope")))
	assert.Equal(t, 400, w.Code)
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
