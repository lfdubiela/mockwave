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

func TestMongo_GetEventRules_Empty(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("empty", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "mockwave.event_rules", mtest.FirstBatch))
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		rules, err := s.GetEventRules()
		require.NoError(mt, err)
		assert.Empty(mt, rules)
	})
}

func TestMongo_GetEventRules_ReturnsAll(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("two event rules", func(mt *mtest.T) {
		r1 := domain.EventRule{
			ID:   "er1",
			Name: "test rule",
			Match: domain.EventMatch{
				Service:   "sns",
				Operation: "Publish",
			},
		}
		r2 := domain.EventRule{
			ID:   "er2",
			Name: "second rule",
			Match: domain.EventMatch{
				Service:   "sqs",
				Operation: "SendMessage",
			},
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(1, "mockwave.event_rules", mtest.FirstBatch,
				bson.D{{Key: "_id", Value: "er1"}, {Key: "data", Value: r1}},
				bson.D{{Key: "_id", Value: "er2"}, {Key: "data", Value: r2}},
			),
			mtest.CreateCursorResponse(0, "mockwave.event_rules", mtest.NextBatch),
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		rules, err := s.GetEventRules()
		require.NoError(mt, err)
		require.Len(mt, rules, 2)
		assert.Equal(mt, "er1", rules[0].ID)
		assert.Equal(mt, "sns", rules[0].Match.Service)
		assert.Equal(mt, "er2", rules[1].ID)
		assert.Equal(mt, "sqs", rules[1].Match.Service)
	})
}

func TestMongo_SaveEventRule(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("upsert", func(mt *mtest.T) {
		mt.AddMockResponses(
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 0}, {Key: "nModified", Value: 0}},
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}}, // bumpVersion
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		rule := domain.EventRule{
			ID:   "er1",
			Name: "test rule",
			Match: domain.EventMatch{
				Service:   "sns",
				Operation: "Publish",
			},
		}
		require.NoError(mt, s.SaveEventRule(rule))
	})
}

func TestMongo_DeleteEventRule(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("delete", func(mt *mtest.T) {
		mt.AddMockResponses(
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}},
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 1}, {Key: "nModified", Value: 1}}, // bumpVersion
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		require.NoError(mt, s.DeleteEventRule("er1"))
	})
}
