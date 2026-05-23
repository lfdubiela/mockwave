package jsonfile_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/domain"
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
	_, err = store.GetSimulation("missing")
	assert.Error(t, err)
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
