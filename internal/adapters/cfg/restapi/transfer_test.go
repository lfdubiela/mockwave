package restapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
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
		"internal dup match":   {Rules: []domain.Rule{validRule("a", "/same"), validRule("b", "/same")}},
		"internal dup sim id": {Rules: []domain.Rule{validRule("ok", "/fine")},
			Simulations: []domain.Simulation{{ID: "s-dup", Protocol: "http"}, {ID: "s-dup", Protocol: "http"}}},
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

func commitReq(t *testing.T, mux http.Handler, cfg domain.Config, query string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(cfg)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/import"+query, bytes.NewReader(body)))
	return w
}

type importReport struct {
	Imported   []string `json:"imported"`
	Skipped    []string `json:"skipped"`
	Overridden []string `json:"overridden"`
}

func TestImportCommit_SkipWithoutOverride(t *testing.T) {
	store := transferStore()
	reloads := 0
	mux := restapi.NewMux(store, func() { reloads++ }, nil, nil, nil, nil, restapi.WithImportExport())

	simRule := domain.Rule{ID: "r1", Name: "Changed", Match: domain.MatchCriteria{Path: "/changed"},
		Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s-changed"}}}
	cfg := domain.Config{
		Rules:       []domain.Rule{simRule, validRule("r-ok", "/fresh")},
		Simulations: []domain.Simulation{{ID: "s-changed", Protocol: "http"}},
	}
	w := commitReq(t, mux, cfg, "")
	require.Equal(t, 200, w.Code)
	var rep importReport
	require.NoError(t, json.NewDecoder(w.Body).Decode(&rep))
	assert.Equal(t, []string{"r-ok"}, rep.Imported)
	assert.Equal(t, []string{"r1"}, rep.Skipped) // id conflict, no override
	assert.Empty(t, rep.Overridden)
	assert.Equal(t, 1, reloads)

	// skipped rule untouched; its payload-only sim NOT written
	for _, r := range store.rules {
		if r.ID == "r1" {
			assert.Equal(t, "/one", r.Match.Path)
		}
	}
	sim, _ := store.GetSimulation("s-changed")
	assert.Nil(t, sim)
}

func TestImportCommit_OverrideReplacesAndCascades(t *testing.T) {
	store := transferStore() // r1 owns s1
	mux := restapi.NewMux(store, func() {}, nil, nil, nil, nil, restapi.WithImportExport())

	incoming := domain.Rule{ID: "r1", Name: "V2", Match: domain.MatchCriteria{Path: "/v2"},
		Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s-v2"}}}
	cfg := domain.Config{Rules: []domain.Rule{incoming},
		Simulations: []domain.Simulation{{ID: "s-v2", Protocol: "http"}}}
	w := commitReq(t, mux, cfg, "?override=r1")
	require.Equal(t, 200, w.Code)
	var rep importReport
	require.NoError(t, json.NewDecoder(w.Body).Decode(&rep))
	assert.Equal(t, []string{"r1"}, rep.Overridden)
	assert.Empty(t, rep.Skipped)

	// old rule replaced, old owned sim s1 cascaded away, new sim present
	require.Len(t, store.rules, 2)
	for _, r := range store.rules {
		if r.ID == "r1" {
			assert.Equal(t, "/v2", r.Match.Path)
		}
	}
	old, _ := store.GetSimulation("s1")
	assert.Nil(t, old)
	neu, _ := store.GetSimulation("s-v2")
	require.NotNil(t, neu)
}

func TestImportCommit_MatchConflictOverrideRemovesExistingID(t *testing.T) {
	store := transferStore()
	mux := restapi.NewMux(store, func() {}, nil, nil, nil, nil, restapi.WithImportExport())
	// r-new duplicates r2's match; override → r2 deleted, r-new saved
	cfg := domain.Config{Rules: []domain.Rule{validRule("r-new", "/two")}}
	w := commitReq(t, mux, cfg, "?override=r-new")
	require.Equal(t, 200, w.Code)
	ids := map[string]bool{}
	for _, r := range store.rules {
		ids[r.ID] = true
	}
	assert.True(t, ids["r-new"])
	assert.False(t, ids["r2"])
}

func TestImportCommit_CleanPayloadNoConflicts(t *testing.T) {
	store := transferStore()
	mux := restapi.NewMux(store, func() {}, nil, nil, nil, nil, restapi.WithImportExport())
	cfg := domain.Config{Rules: []domain.Rule{validRule("r-a", "/a"), validRule("r-b", "/b")}}
	w := commitReq(t, mux, cfg, "")
	require.Equal(t, 200, w.Code)
	var rep importReport
	require.NoError(t, json.NewDecoder(w.Body).Decode(&rep))
	assert.ElementsMatch(t, []string{"r-a", "r-b"}, rep.Imported)
	assert.Len(t, store.rules, 4)
}

func TestImportCommit_Disabled403(t *testing.T) {
	mux := restapi.NewMux(transferStore(), nil, nil, nil, nil, nil)
	w := commitReq(t, mux, domain.Config{}, "")
	assert.Equal(t, 403, w.Code)
}

func TestImportCommit_Validation422NothingWritten(t *testing.T) {
	store := transferStore()
	mux := restapi.NewMux(store, func() {}, nil, nil, nil, nil, restapi.WithImportExport())
	cfg := domain.Config{Rules: []domain.Rule{validRule("ok", "/fine"), {ID: "bad"}}}
	w := commitReq(t, mux, cfg, "")
	assert.Equal(t, 422, w.Code)
	assert.Len(t, store.rules, 2) // unchanged
}

func TestExportImport_RoundTrip(t *testing.T) {
	src := transferStore()
	srcMux := restapi.NewMux(src, nil, nil, nil, nil, nil, restapi.WithImportExport())
	w := httptest.NewRecorder()
	srcMux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/export", nil))
	require.Equal(t, 200, w.Code)

	dst := &memStore{}
	dstMux := restapi.NewMux(dst, func() {}, nil, nil, nil, nil, restapi.WithImportExport())
	w2 := httptest.NewRecorder()
	dstMux.ServeHTTP(w2, httptest.NewRequest(http.MethodPost, "/api/import", bytes.NewReader(w.Body.Bytes())))
	require.Equal(t, 200, w2.Code)
	assert.Len(t, dst.rules, 2)
	assert.Len(t, dst.sims, 1) // s1 (s-orphan was never exported)
}

// failingStore wraps memStore and fails SaveRule after n successful saves.
type failingStore struct {
	*memStore
	failAfter int
	saves     int
}

func (f *failingStore) SaveRule(r domain.Rule) error {
	if f.saves >= f.failAfter {
		return fmt.Errorf("boom")
	}
	f.saves++
	return f.memStore.SaveRule(r)
}

func TestImportCommit_PartialWriteStillReloads(t *testing.T) {
	store := &failingStore{memStore: transferStore(), failAfter: 1}
	reloads := 0
	mux := restapi.NewMux(store, func() { reloads++ }, nil, nil, nil, nil, restapi.WithImportExport())
	cfg := domain.Config{Rules: []domain.Rule{validRule("r-a", "/a"), validRule("r-b", "/b")}}
	w := commitReq(t, mux, cfg, "")
	assert.Equal(t, 500, w.Code)
	assert.Equal(t, 1, reloads) // r-a was written; pipeline must reload despite the 500
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

// TestImportCommit_OverridePreservesSharedSim verifies that when the incoming
// override rule replaces an existing rule, a sim that the existing rule shared
// with ANOTHER stored rule is NOT cascade-deleted.
func TestImportCommit_OverridePreservesSharedSim(t *testing.T) {
	// r1 and r2 both reference "s-shared". Importing override=r1 replaces r1
	// with a new version; s-shared must survive because r2 still uses it.
	// s-own is referenced only by r1 → should be cascaded.
	store := &memStore{
		rules: []domain.Rule{
			{ID: "r1", Name: "One", Match: domain.MatchCriteria{Path: "/one"},
				Buckets: []domain.WeightedBucket{
					{Weight: 50, Action: domain.ActionSimulate, SimulationID: "s-shared"},
					{Weight: 50, Action: domain.ActionSimulate, SimulationID: "s-own"},
				}},
			{ID: "r2", Name: "Two", Match: domain.MatchCriteria{Path: "/two"},
				Buckets: []domain.WeightedBucket{
					{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s-shared"},
				}},
		},
		sims: []domain.Simulation{
			{ID: "s-shared", Protocol: "http"},
			{ID: "s-own", Protocol: "http"},
		},
	}
	mux := restapi.NewMux(store, func() {}, nil, nil, nil, nil, restapi.WithImportExport())

	incoming := domain.Rule{
		ID: "r1", Name: "V2", Match: domain.MatchCriteria{Path: "/v2"},
		Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s-new"}},
	}
	cfg := domain.Config{
		Rules:       []domain.Rule{incoming},
		Simulations: []domain.Simulation{{ID: "s-new", Protocol: "http"}},
	}
	w := commitReq(t, mux, cfg, "?override=r1")
	require.Equal(t, 200, w.Code)

	// s-shared still referenced by r2 → must survive
	shared, _ := store.GetSimulation("s-shared")
	require.NotNil(t, shared, "s-shared should be preserved: still referenced by r2")

	// s-own was only referenced by old r1 → must be cascaded
	own, _ := store.GetSimulation("s-own")
	assert.Nil(t, own, "s-own should be cascaded: no longer referenced")

	// new sim present
	neu, _ := store.GetSimulation("s-new")
	require.NotNil(t, neu)
}

func TestImportCommit_OverrideKeepsStoreOnlySimReusedByIncoming(t *testing.T) {
	// The incoming override of r1 keeps referencing "s-keep", which exists only
	// in the store (not in the payload). The cascade of old r1 must not delete
	// it, or the new r1 would dangle.
	store := &memStore{
		rules: []domain.Rule{
			{ID: "r1", Name: "One", Match: domain.MatchCriteria{Path: "/one"},
				Buckets: []domain.WeightedBucket{
					{Weight: 50, Action: domain.ActionSimulate, SimulationID: "s-keep"},
					{Weight: 50, Action: domain.ActionSimulate, SimulationID: "s-drop"},
				}},
		},
		sims: []domain.Simulation{
			{ID: "s-keep", Protocol: "http"},
			{ID: "s-drop", Protocol: "http"},
		},
	}
	mux := restapi.NewMux(store, func() {}, nil, nil, nil, nil, restapi.WithImportExport())

	incoming := domain.Rule{
		ID: "r1", Name: "V2", Match: domain.MatchCriteria{Path: "/v2"},
		Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s-keep"}},
	}
	w := commitReq(t, mux, domain.Config{Rules: []domain.Rule{incoming}}, "?override=r1")
	require.Equal(t, 200, w.Code)

	kept, _ := store.GetSimulation("s-keep")
	require.NotNil(t, kept, "store-only sim reused by the incoming rule must survive the cascade")
	dropped, _ := store.GetSimulation("s-drop")
	assert.Nil(t, dropped, "sim no longer referenced must be cascaded")
}

// --- fault profile import/export (Task 11) ---

func transferFaultStore() *faultMemStore {
	return &faultMemStore{
		memStore: memStore{
			rules: []domain.Rule{
				{ID: "r1", Name: "One", Match: domain.MatchCriteria{Path: "/one"},
					Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s1", FaultProfileID: "p1"}}},
				{ID: "r2", Name: "Two", Match: domain.MatchCriteria{Path: "/two"},
					Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionForward, ForwardURL: "http://up"}}},
			},
			sims: []domain.Simulation{{ID: "s1", Protocol: "http"}},
		},
		profiles: []domain.FaultProfile{validProfile("p1"), validProfile("p-orphan")},
	}
}

func faultyRule(id, path, profileID string) domain.Rule {
	return domain.Rule{ID: id, Name: id, Match: domain.MatchCriteria{Path: path},
		Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionForward,
			ForwardURL: "http://up", FaultProfileID: profileID}}}
}

func TestExport_IncludesReferencedFaultProfiles(t *testing.T) {
	mux := restapi.NewMux(transferFaultStore(), nil, nil, nil, nil, nil, restapi.WithImportExport())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/export", nil))
	require.Equal(t, 200, w.Code)
	var cfg domain.Config
	require.NoError(t, json.NewDecoder(w.Body).Decode(&cfg))
	require.Len(t, cfg.FaultProfiles, 1, "only profiles referenced by exported rules")
	assert.Equal(t, "p1", cfg.FaultProfiles[0].ID)
}

func TestExport_SubsetExcludesUnreferencedProfiles(t *testing.T) {
	mux := restapi.NewMux(transferFaultStore(), nil, nil, nil, nil, nil, restapi.WithImportExport())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/export?rules=r2", nil))
	require.Equal(t, 200, w.Code)
	var cfg domain.Config
	require.NoError(t, json.NewDecoder(w.Body).Decode(&cfg))
	assert.Empty(t, cfg.FaultProfiles)
}

func TestImportPreview_FaultProfileIDConflict(t *testing.T) {
	mux := restapi.NewMux(transferFaultStore(), nil, nil, nil, nil, nil, restapi.WithImportExport())
	cfg := domain.Config{
		Rules:         []domain.Rule{faultyRule("r-new", "/fresh", "p1")},
		FaultProfiles: []domain.FaultProfile{validProfile("p1")},
	}
	w := previewReq(t, mux, cfg)
	require.Equal(t, 200, w.Code)
	var resp struct {
		Conflicts             []struct{ Reason string }
		FaultProfileConflicts []struct {
			Reason   string `json:"reason"`
			Incoming struct{ ID, Name string }
			Existing struct{ ID, Name string }
		} `json:"fault_profile_conflicts"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Empty(t, resp.Conflicts)
	require.Len(t, resp.FaultProfileConflicts, 1)
	assert.Equal(t, "id", resp.FaultProfileConflicts[0].Reason)
	assert.Equal(t, "p1", resp.FaultProfileConflicts[0].Incoming.ID)
	assert.Equal(t, "p1", resp.FaultProfileConflicts[0].Existing.ID)
}

func TestImportPreview_FaultProfileValidation422(t *testing.T) {
	store := transferFaultStore()
	mux := restapi.NewMux(store, nil, nil, nil, nil, nil, restapi.WithImportExport())
	bad := validProfile("p-bad")
	bad.Faults = nil // invalid: profile needs at least one fault
	for name, cfg := range map[string]domain.Config{
		"invalid profile":      {FaultProfiles: []domain.FaultProfile{bad}},
		"duplicate profile id": {FaultProfiles: []domain.FaultProfile{validProfile("dup"), validProfile("dup")}},
		"dangling profile ref": {Rules: []domain.Rule{faultyRule("rx", "/x", "nowhere")}},
	} {
		t.Run(name, func(t *testing.T) {
			w := previewReq(t, mux, cfg)
			assert.Equal(t, 422, w.Code)
		})
	}
}

func TestImportPreview_ProfileRefResolvesInStore(t *testing.T) {
	// references p1 which exists in the store (not in payload) — valid
	mux := restapi.NewMux(transferFaultStore(), nil, nil, nil, nil, nil, restapi.WithImportExport())
	cfg := domain.Config{Rules: []domain.Rule{faultyRule("rx", "/x", "p1")}}
	w := previewReq(t, mux, cfg)
	assert.Equal(t, 200, w.Code)
}

func TestImport_FaultProfilesUnsupportedStore422(t *testing.T) {
	// plain memStore has no FaultStore capability
	mux := restapi.NewMux(transferStore(), nil, nil, nil, nil, nil, restapi.WithImportExport())
	for name, cfg := range map[string]domain.Config{
		"payload profiles": {FaultProfiles: []domain.FaultProfile{validProfile("p1")}},
		"rule profile ref": {Rules: []domain.Rule{faultyRule("rx", "/x", "p1")}},
	} {
		t.Run(name, func(t *testing.T) {
			w := commitReq(t, mux, cfg, "")
			assert.Equal(t, 422, w.Code)
		})
	}
}

func TestImportCommit_UpsertsReferencedFaultProfiles(t *testing.T) {
	store := transferFaultStore()
	mux := restapi.NewMux(store, func() {}, nil, nil, nil, nil, restapi.WithImportExport())

	updated := validProfile("p1")
	updated.Name = "updated"
	cfg := domain.Config{
		Rules: []domain.Rule{
			faultyRule("r-upd", "/upd", "p1"),    // upserts existing p1
			faultyRule("r-new", "/new", "p-new"), // inserts new p-new
		},
		FaultProfiles: []domain.FaultProfile{updated, validProfile("p-new"), validProfile("p-unused")},
	}
	w := commitReq(t, mux, cfg, "")
	require.Equal(t, 200, w.Code)

	p1, _ := store.GetFaultProfile("p1")
	require.NotNil(t, p1)
	assert.Equal(t, "updated", p1.Name, "existing profile must be upserted")
	pNew, _ := store.GetFaultProfile("p-new")
	require.NotNil(t, pNew)
	unused, _ := store.GetFaultProfile("p-unused")
	assert.Nil(t, unused, "payload profile not referenced by a saved rule must not be written")
}

func TestImportCommit_SkippedRuleProfileNotWritten(t *testing.T) {
	store := transferFaultStore()
	mux := restapi.NewMux(store, func() {}, nil, nil, nil, nil, restapi.WithImportExport())
	// r1 conflicts by id and is not overridden → its payload profile must not be written
	updated := validProfile("p1")
	updated.Name = "should-not-land"
	cfg := domain.Config{
		Rules: []domain.Rule{{ID: "r1", Name: "V2", Match: domain.MatchCriteria{Path: "/changed"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionForward,
				ForwardURL: "http://up", FaultProfileID: "p1"}}}},
		FaultProfiles: []domain.FaultProfile{updated},
	}
	w := commitReq(t, mux, cfg, "")
	require.Equal(t, 200, w.Code)
	var rep importReport
	require.NoError(t, json.NewDecoder(w.Body).Decode(&rep))
	assert.Equal(t, []string{"r1"}, rep.Skipped)
	p1, _ := store.GetFaultProfile("p1")
	require.NotNil(t, p1)
	assert.NotEqual(t, "should-not-land", p1.Name)
}

func TestExportImport_RoundTripWithProfiles(t *testing.T) {
	src := transferFaultStore()
	srcMux := restapi.NewMux(src, nil, nil, nil, nil, nil, restapi.WithImportExport())
	w := httptest.NewRecorder()
	srcMux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/export", nil))
	require.Equal(t, 200, w.Code)

	dst := &faultMemStore{}
	dstMux := restapi.NewMux(dst, func() {}, nil, nil, nil, nil, restapi.WithImportExport())
	w2 := httptest.NewRecorder()
	dstMux.ServeHTTP(w2, httptest.NewRequest(http.MethodPost, "/api/import", bytes.NewReader(w.Body.Bytes())))
	require.Equal(t, 200, w2.Code)
	assert.Len(t, dst.rules, 2)
	require.Len(t, dst.profiles, 1)
	assert.Equal(t, "p1", dst.profiles[0].ID)
}
