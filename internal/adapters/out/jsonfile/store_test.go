package jsonfile_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, cfg domain.Config) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "mockwave-*.json")
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(f).Encode(cfg))
	require.NoError(t, f.Close())
	return f.Name()
}

func bucket() domain.WeightedBucket {
	return domain.WeightedBucket{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}
}

func TestStore_GetRules(t *testing.T) {
	cfg := domain.Config{Rules: []domain.Rule{{ID: "r1", Match: domain.MatchCriteria{Path: "/foo"}, Buckets: []domain.WeightedBucket{bucket()}}}}
	store, err := jsonfile.NewStore(writeConfig(t, cfg))
	require.NoError(t, err)
	rules, err := store.GetRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "r1", rules[0].ID)
}

func TestStore_GetSimulation(t *testing.T) {
	cfg := domain.Config{Simulations: []domain.Simulation{{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}}}
	store, err := jsonfile.NewStore(writeConfig(t, cfg))
	require.NoError(t, err)
	sim, err := store.GetSimulation("s1")
	require.NoError(t, err)
	assert.Equal(t, 200, sim.Response.Status)
}

func TestStore_GetSimulation_NotFound(t *testing.T) {
	store, err := jsonfile.NewStore(writeConfig(t, domain.Config{}))
	require.NoError(t, err)
	sim, err := store.GetSimulation("missing")
	require.NoError(t, err)
	assert.Nil(t, sim)
}

func TestStore_SaveRule(t *testing.T) {
	store, err := jsonfile.NewStore(writeConfig(t, domain.Config{}))
	require.NoError(t, err)
	r := domain.Rule{ID: "r-new", Match: domain.MatchCriteria{Path: "/bar"}, Buckets: []domain.WeightedBucket{bucket()}}
	require.NoError(t, store.SaveRule(r))
	rules, err := store.GetRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "r-new", rules[0].ID)
}

func TestStore_DeleteRule(t *testing.T) {
	cfg := domain.Config{Rules: []domain.Rule{
		{ID: "r1", Match: domain.MatchCriteria{Path: "/a"}, Buckets: []domain.WeightedBucket{bucket()}},
		{ID: "r2", Match: domain.MatchCriteria{Path: "/b"}, Buckets: []domain.WeightedBucket{bucket()}},
	}}
	store, err := jsonfile.NewStore(writeConfig(t, cfg))
	require.NoError(t, err)
	require.NoError(t, store.DeleteRule("r1"))
	rules, err := store.GetRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "r2", rules[0].ID)
}

func TestStore_FileNotFound(t *testing.T) {
	_, err := jsonfile.NewStore(filepath.Join(t.TempDir(), "nope.json"))
	assert.Error(t, err)
}

func TestStore_SaveSimulation(t *testing.T) {
	store, err := jsonfile.NewStore(writeConfig(t, domain.Config{}))
	require.NoError(t, err)
	sim := domain.Simulation{ID: "s-new", Protocol: "http", Response: domain.HTTPResponse{Status: 201}}
	require.NoError(t, store.SaveSimulation(sim))
	got, err := store.GetSimulation("s-new")
	require.NoError(t, err)
	assert.Equal(t, 201, got.Response.Status)
}

func TestStore_SaveSimulation_UpdatesExisting(t *testing.T) {
	cfg := domain.Config{Simulations: []domain.Simulation{{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}}}
	store, err := jsonfile.NewStore(writeConfig(t, cfg))
	require.NoError(t, err)
	updated := domain.Simulation{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 404}}
	require.NoError(t, store.SaveSimulation(updated))
	got, err := store.GetSimulation("s1")
	require.NoError(t, err)
	assert.Equal(t, 404, got.Response.Status)
}

func TestStore_DeleteSimulation(t *testing.T) {
	cfg := domain.Config{Simulations: []domain.Simulation{
		{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}},
		{ID: "s2", Protocol: "http", Response: domain.HTTPResponse{Status: 201}},
	}}
	store, err := jsonfile.NewStore(writeConfig(t, cfg))
	require.NoError(t, err)
	require.NoError(t, store.DeleteSimulation("s1"))
	deleted, err := store.GetSimulation("s1")
	require.NoError(t, err)
	assert.Nil(t, deleted)
	got, err := store.GetSimulation("s2")
	require.NoError(t, err)
	assert.Equal(t, 201, got.Response.Status)
}

func TestStore_DeleteSimulation_NotFound(t *testing.T) {
	store, err := jsonfile.NewStore(writeConfig(t, domain.Config{}))
	require.NoError(t, err)
	assert.Error(t, store.DeleteSimulation("nope"))
}

func TestStore_DeleteRule_NotFound(t *testing.T) {
	store, err := jsonfile.NewStore(writeConfig(t, domain.Config{}))
	require.NoError(t, err)
	assert.Error(t, store.DeleteRule("nope"))
}

func TestStore_SaveRule_UpdatesExisting(t *testing.T) {
	cfg := domain.Config{Rules: []domain.Rule{
		{ID: "r1", Match: domain.MatchCriteria{Path: "/old"}, Buckets: []domain.WeightedBucket{bucket()}},
	}}
	store, err := jsonfile.NewStore(writeConfig(t, cfg))
	require.NoError(t, err)
	updated := domain.Rule{ID: "r1", Match: domain.MatchCriteria{Path: "/new"}, Buckets: []domain.WeightedBucket{bucket()}}
	require.NoError(t, store.SaveRule(updated))
	rules, err := store.GetRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "/new", rules[0].Match.Path)
}

func TestStore_NewMemStore(t *testing.T) {
	cfg := domain.Config{
		Simulations: []domain.Simulation{
			{ID: "s1", Protocol: "http"},
		},
	}
	s := jsonfile.NewMemStore(cfg)
	sims, err := s.ListSimulations()
	require.NoError(t, err)
	assert.Len(t, sims, 1)
	assert.Equal(t, "s1", sims[0].ID)
}

func TestStore_ListSimulations(t *testing.T) {
	cfg := domain.Config{
		Simulations: []domain.Simulation{
			{ID: "sim1", Protocol: "http"},
			{ID: "sim2", Protocol: "graphql"},
		},
	}
	s, err := jsonfile.NewStore(writeConfig(t, cfg))
	require.NoError(t, err)
	sims, err := s.ListSimulations()
	require.NoError(t, err)
	assert.Len(t, sims, 2)
}

func TestStore_Reload(t *testing.T) {
	cfg := domain.Config{Rules: []domain.Rule{{ID: "r1", Match: domain.MatchCriteria{Path: "/foo"}, Buckets: []domain.WeightedBucket{bucket()}}}}
	path := writeConfig(t, cfg)
	store, err := jsonfile.NewStore(path)
	require.NoError(t, err)
	// Update the file externally
	newCfg := domain.Config{Rules: []domain.Rule{{ID: "r2", Match: domain.MatchCriteria{Path: "/bar"}, Buckets: []domain.WeightedBucket{bucket()}}}}
	f, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(f).Encode(newCfg))
	require.NoError(t, f.Close())
	// Reload
	require.NoError(t, store.Reload())
	rules, err := store.GetRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "r2", rules[0].ID)
}

func TestFaultProfileCRUD(t *testing.T) {
	store, err := jsonfile.NewStore(writeConfig(t, domain.Config{}))
	require.NoError(t, err)

	p := domain.FaultProfile{ID: "fp1", Name: "p", Enabled: true,
		Faults: []domain.Fault{{Type: domain.FaultJitter, Probability: 1, Params: domain.FaultParams{JitterMs: 100}}}}

	require.NoError(t, store.SaveFaultProfile(p))

	got, err := store.GetFaultProfile("fp1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "p", got.Name)

	list, err := store.ListFaultProfiles()
	require.NoError(t, err)
	require.Len(t, list, 1)

	// update path: same ID, changed name
	p.Name = "renamed"
	require.NoError(t, store.SaveFaultProfile(p))
	got, err = store.GetFaultProfile("fp1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "renamed", got.Name)
	list, err = store.ListFaultProfiles()
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, store.DeleteFaultProfile("fp1"))
	got, err = store.GetFaultProfile("fp1")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestScenarioCRUD(t *testing.T) {
	s, err := jsonfile.NewStore(writeConfig(t, domain.Config{}))
	require.NoError(t, err)
	sc := domain.Scenario{ID: "sc1", Name: "n", RuleIDs: []string{"r1"},
		Phases: []domain.ScenarioPhase{{DurationSec: 10, FaultProfileID: "p"}}}
	require.NoError(t, s.SaveScenario(sc))
	got, err := s.GetScenario("sc1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "n", got.Name)
	list, err := s.ListScenarios()
	require.NoError(t, err)
	assert.Len(t, list, 1)
	sc.Name = "n2"
	require.NoError(t, s.SaveScenario(sc))
	list, _ = s.ListScenarios()
	assert.Len(t, list, 1)
	assert.Equal(t, "n2", list[0].Name)
	require.NoError(t, s.DeleteScenario("sc1"))
	got, err = s.GetScenario("sc1")
	require.NoError(t, err)
	assert.Nil(t, got)
}
