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
		mt.AddMockResponses(bson.D{
			{Key: "ok", Value: 1},
			{Key: "n", Value: 0},
			{Key: "nModified", Value: 0},
		})
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
		mt.AddMockResponses(bson.D{
			{Key: "ok", Value: 1},
			{Key: "n", Value: 0},
			{Key: "nModified", Value: 0},
		})
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		require.NoError(mt, s.SaveSimulation(domain.Simulation{ID: "s1", Protocol: "http"}))
	})
}

func TestMongo_DeleteRule(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("delete", func(mt *mtest.T) {
		mt.AddMockResponses(bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}})
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		require.NoError(mt, s.DeleteRule("r1"))
	})
}

func TestMongo_DeleteSimulation(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("delete", func(mt *mtest.T) {
		mt.AddMockResponses(bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}})
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		require.NoError(mt, s.DeleteSimulation("s1"))
	})
}
