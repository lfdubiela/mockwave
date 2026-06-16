package cosmos_test

import (
	"context"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/adapters/out/cosmos"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

// Cosmos delegates all MatchedStore calls to mongodb.Store.
// These tests verify the delegation works through the mock client.

func TestCosmos_SaveMatched(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("upsert", func(mt *mtest.T) {
		mt.AddMockResponses(
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 0}, {Key: "nModified", Value: 0}},
		)
		s := cosmos.NewStoreFromClient(mt.Client, "mockwave")
		req := matched.Request{ID: "r1", RuleID: "rule-1", Method: "GET", At: time.Now()}
		err := s.SaveMatched(context.Background(), []matched.Request{req}, nil, nil)
		require.NoError(mt, err)
	})
}

func TestCosmos_SweepExpired_Noop(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("noop", func(mt *mtest.T) {
		s := cosmos.NewStoreFromClient(mt.Client, "mockwave")
		n, err := s.SweepExpired(context.Background(), time.Now().Unix())
		require.NoError(mt, err)
		assert.Equal(mt, 0, n)
	})
}

func TestCosmos_GetMatched_NotFound(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("not found", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "mockwave.matched_requests", mtest.FirstBatch))
		s := cosmos.NewStoreFromClient(mt.Client, "mockwave")
		full, err := s.GetMatched(context.Background(), "rule-1", "missing")
		require.NoError(mt, err)
		assert.Nil(mt, full)
	})
}
