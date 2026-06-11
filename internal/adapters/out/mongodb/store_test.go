package mongodb_test

import (
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/out/mongodb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestMongo_GetRules_Empty(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("empty", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "mockwave.rules", mtest.FirstBatch))
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		rules, err := s.GetRules()
		require.NoError(mt, err)
		assert.Empty(mt, rules)
	})
}

func TestMongo_GetRules_ReturnsAll(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("two rules", func(mt *mtest.T) {
		r1 := domain.Rule{
			ID:      "r1",
			Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/ping"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(1, "mockwave.rules", mtest.FirstBatch,
				bson.D{{Key: "_id", Value: "r1"}, {Key: "data", Value: r1}},
			),
			mtest.CreateCursorResponse(0, "mockwave.rules", mtest.NextBatch),
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		rules, err := s.GetRules()
		require.NoError(mt, err)
		require.Len(mt, rules, 1)
		assert.Equal(mt, "r1", rules[0].ID)
		assert.Equal(mt, "/ping", rules[0].Match.Path)
	})
}

func TestMongo_GetSimulation_Found(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("found", func(mt *mtest.T) {
		sim := domain.Simulation{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(1, "mockwave.simulations", mtest.FirstBatch,
				bson.D{{Key: "_id", Value: "s1"}, {Key: "data", Value: sim}},
			),
			mtest.CreateCursorResponse(0, "mockwave.simulations", mtest.NextBatch),
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		got, err := s.GetSimulation("s1")
		require.NoError(mt, err)
		require.NotNil(mt, got)
		assert.Equal(mt, "s1", got.ID)
		assert.Equal(mt, 200, got.Response.Status)
	})
}

func TestMongo_GetSimulation_NotFound(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("not found", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "mockwave.simulations", mtest.FirstBatch))
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		got, err := s.GetSimulation("missing")
		require.NoError(mt, err)
		assert.Nil(mt, got)
	})
}

func TestMongo_SaveRule(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("upsert", func(mt *mtest.T) {
		mt.AddMockResponses(
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 0}, {Key: "nModified", Value: 0}},
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}}, // bumpVersion
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		rule := domain.Rule{
			ID:      "r1",
			Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/x"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
		}
		require.NoError(mt, s.SaveRule(rule))
	})
}

func TestMongo_SaveSimulation(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("upsert", func(mt *mtest.T) {
		mt.AddMockResponses(
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 0}, {Key: "nModified", Value: 0}},
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}}, // bumpVersion
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		require.NoError(mt, s.SaveSimulation(domain.Simulation{ID: "s1", Protocol: "http"}))
	})
}

func TestMongo_DeleteRule(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("delete", func(mt *mtest.T) {
		mt.AddMockResponses(
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}},
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}}, // bumpVersion
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		require.NoError(mt, s.DeleteRule("r1"))
	})
}

func TestMongo_ListSimulations(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("all sims", func(mt *mtest.T) {
		sim := domain.Simulation{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(1, "mockwave.simulations", mtest.FirstBatch,
				bson.D{{Key: "_id", Value: "s1"}, {Key: "data", Value: sim}},
			),
			mtest.CreateCursorResponse(0, "mockwave.simulations", mtest.NextBatch),
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		sims, err := s.ListSimulations()
		require.NoError(mt, err)
		require.Len(mt, sims, 1)
		assert.Equal(mt, "s1", sims[0].ID)
	})
}

func TestMongo_DeleteSimulation(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("delete", func(mt *mtest.T) {
		mt.AddMockResponses(
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}},
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}}, // bumpVersion
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		require.NoError(mt, s.DeleteSimulation("s1"))
	})
}

func TestStore_SaveFaultProfile(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("upsert + version bump", func(mt *mtest.T) {
		mt.AddMockResponses(
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 0}, {Key: "nModified", Value: 0}}, // UpdateOne (fault_profiles upsert)
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}}, // bumpVersion
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		err := s.SaveFaultProfile(domain.FaultProfile{ID: "fp1", Name: "p", Enabled: true,
			Faults: []domain.Fault{{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}}}})
		require.NoError(mt, err)
	})
}

func TestStore_GetFaultProfile_NotFound(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("miss returns nil,nil", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "mockwave.fault_profiles", mtest.FirstBatch))
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		got, err := s.GetFaultProfile("nope")
		require.NoError(mt, err)
		assert.Nil(mt, got)
	})
}

func TestStore_GetFaultProfile_Hit(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("hit decodes data", func(mt *mtest.T) {
		p := domain.FaultProfile{ID: "fp1", Name: "p", Enabled: true,
			Faults: []domain.Fault{{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}}}}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(1, "mockwave.fault_profiles", mtest.FirstBatch,
				bson.D{{Key: "_id", Value: "fp1"}, {Key: "data", Value: p}},
			),
			mtest.CreateCursorResponse(0, "mockwave.fault_profiles", mtest.NextBatch),
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		got, err := s.GetFaultProfile("fp1")
		require.NoError(mt, err)
		require.NotNil(mt, got)
		assert.Equal(mt, "p", got.Name)
	})
}

func TestStore_ListFaultProfiles(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("list", func(mt *mtest.T) {
		p := domain.FaultProfile{ID: "fp1", Name: "p", Enabled: true}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(1, "mockwave.fault_profiles", mtest.FirstBatch,
				bson.D{{Key: "_id", Value: "fp1"}, {Key: "data", Value: p}},
			),
			mtest.CreateCursorResponse(0, "mockwave.fault_profiles", mtest.NextBatch),
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		list, err := s.ListFaultProfiles()
		require.NoError(mt, err)
		require.Len(mt, list, 1)
		assert.Equal(mt, "fp1", list[0].ID)
	})
}

func TestStore_DeleteFaultProfile(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("delete + version bump", func(mt *mtest.T) {
		mt.AddMockResponses(
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}},                               // DeleteOne
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}}, // bumpVersion
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		require.NoError(mt, s.DeleteFaultProfile("fp1"))
	})
}

func TestStore_SaveScenario(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("upsert + version bump", func(mt *mtest.T) {
		mt.AddMockResponses(
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 0}, {Key: "nModified", Value: 0}}, // UpdateOne (scenarios upsert)
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}}, // bumpVersion
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		err := s.SaveScenario(domain.Scenario{ID: "sc1", Name: "n", RuleIDs: []string{"r1"},
			Phases: []domain.ScenarioPhase{{DurationSec: 10, FaultProfileID: "p"}}})
		require.NoError(mt, err)
	})
}

func TestStore_GetScenario_NotFound(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("miss returns nil,nil", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "mockwave.scenarios", mtest.FirstBatch))
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		got, err := s.GetScenario("nope")
		require.NoError(mt, err)
		assert.Nil(mt, got)
	})
}

func TestStore_GetScenario_Hit(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("hit decodes data", func(mt *mtest.T) {
		sc := domain.Scenario{ID: "sc1", Name: "n", RuleIDs: []string{"r1"},
			Phases: []domain.ScenarioPhase{{DurationSec: 10, FaultProfileID: "p"}}}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(1, "mockwave.scenarios", mtest.FirstBatch,
				bson.D{{Key: "_id", Value: "sc1"}, {Key: "data", Value: sc}},
			),
			mtest.CreateCursorResponse(0, "mockwave.scenarios", mtest.NextBatch),
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		got, err := s.GetScenario("sc1")
		require.NoError(mt, err)
		require.NotNil(mt, got)
		assert.Equal(mt, "n", got.Name)
	})
}

func TestStore_ListScenarios(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("list", func(mt *mtest.T) {
		sc := domain.Scenario{ID: "sc1", Name: "n", RuleIDs: []string{"r1"},
			Phases: []domain.ScenarioPhase{{DurationSec: 10, FaultProfileID: "p"}}}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(1, "mockwave.scenarios", mtest.FirstBatch,
				bson.D{{Key: "_id", Value: "sc1"}, {Key: "data", Value: sc}},
			),
			mtest.CreateCursorResponse(0, "mockwave.scenarios", mtest.NextBatch),
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		list, err := s.ListScenarios()
		require.NoError(mt, err)
		require.Len(mt, list, 1)
		assert.Equal(mt, "sc1", list[0].ID)
	})
}

func TestStore_DeleteScenario(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("delete + version bump", func(mt *mtest.T) {
		mt.AddMockResponses(
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}},                               // DeleteOne
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}}, // bumpVersion
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		require.NoError(mt, s.DeleteScenario("sc1"))
	})
}
