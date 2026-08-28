package mongodb_test

import (
	"context"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/adapters/out/mongodb"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestMongo_SaveMatched_Upserts(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("upsert request", func(mt *mtest.T) {
		// One UpdateOne response per request + one per body (none here).
		mt.AddMockResponses(
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 0}, {Key: "nModified", Value: 0}},
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		req := matched.Request{
			ID:     "r1",
			RuleID: "rule-1",
			At:     time.Now(),
			Method: "GET",
			Path:   "/ping",
		}
		err := s.SaveMatched(context.Background(), []matched.Request{req}, nil, nil)
		require.NoError(mt, err)
	})
}

func TestMongo_SaveMatched_WithBodies(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("upsert request + req body + resp body", func(mt *mtest.T) {
		mt.AddMockResponses(
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 0}, {Key: "nModified", Value: 0}}, // request
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 0}, {Key: "nModified", Value: 0}}, // req body
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 0}, {Key: "nModified", Value: 0}}, // resp body
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		req := matched.Request{ID: "r1", RuleID: "rule-1", RequestBodyID: "rb1", ResponseBodyID: "sb1"}
		reqBody := matched.RequestBody{ID: "rb1", Body: []byte(`{"q":"test"}`)}
		respBody := matched.ResponseBody{ID: "sb1", Body: map[string]interface{}{"ok": true}}
		err := s.SaveMatched(context.Background(),
			[]matched.Request{req},
			[]matched.RequestBody{reqBody},
			[]matched.ResponseBody{respBody},
		)
		require.NoError(mt, err)
	})
}

func TestMongo_ListMatched_Empty(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("empty collection", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "mockwave.matched_requests", mtest.FirstBatch))
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		page, err := s.ListMatched(context.Background(), "rule-1", matched.Query{})
		require.NoError(mt, err)
		assert.Empty(mt, page.Items)
		assert.Empty(mt, page.NextCursor)
	})
}

func TestMongo_ListMatched_ReturnsItems(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("two items", func(mt *mtest.T) {
		req1 := matched.Request{ID: "id2", RuleID: "rule-1", Method: "GET", Path: "/b"}
		req2 := matched.Request{ID: "id1", RuleID: "rule-1", Method: "POST", Path: "/a"}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(1, "mockwave.matched_requests", mtest.FirstBatch,
				bson.D{{Key: "_id", Value: "id2"}, {Key: "rule_id", Value: "rule-1"}, {Key: "data", Value: req1}},
				bson.D{{Key: "_id", Value: "id1"}, {Key: "rule_id", Value: "rule-1"}, {Key: "data", Value: req2}},
			),
			mtest.CreateCursorResponse(0, "mockwave.matched_requests", mtest.NextBatch),
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		page, err := s.ListMatched(context.Background(), "rule-1", matched.Query{})
		require.NoError(mt, err)
		assert.Len(mt, page.Items, 2)
	})
}

func TestMongo_ListMatched_ClientSideFilter(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("filter by method", func(mt *mtest.T) {
		req1 := matched.Request{ID: "id2", RuleID: "rule-1", Method: "GET", Path: "/b"}
		req2 := matched.Request{ID: "id1", RuleID: "rule-1", Method: "POST", Path: "/a"}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(1, "mockwave.matched_requests", mtest.FirstBatch,
				bson.D{{Key: "_id", Value: "id2"}, {Key: "rule_id", Value: "rule-1"}, {Key: "data", Value: req1}},
				bson.D{{Key: "_id", Value: "id1"}, {Key: "rule_id", Value: "rule-1"}, {Key: "data", Value: req2}},
			),
			mtest.CreateCursorResponse(0, "mockwave.matched_requests", mtest.NextBatch),
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		page, err := s.ListMatched(context.Background(), "rule-1", matched.Query{Method: "GET"})
		require.NoError(mt, err)
		require.Len(mt, page.Items, 1)
		assert.Equal(mt, "GET", page.Items[0].Method)
	})
}

func TestMongo_GetMatched_Found(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("found", func(mt *mtest.T) {
		req := matched.Request{ID: "r1", RuleID: "rule-1", Method: "GET", Path: "/x"}
		mt.AddMockResponses(mtest.CreateCursorResponse(1, "mockwave.matched_requests", mtest.FirstBatch,
			bson.D{{Key: "_id", Value: "r1"}, {Key: "rule_id", Value: "rule-1"}, {Key: "data", Value: req}},
		))
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		full, err := s.GetMatched(context.Background(), "rule-1", "r1")
		require.NoError(mt, err)
		require.NotNil(mt, full)
		assert.Equal(mt, "r1", full.ID)
	})
}

func TestMongo_GetMatched_NotFound(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("not found", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "mockwave.matched_requests", mtest.FirstBatch))
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		full, err := s.GetMatched(context.Background(), "rule-1", "missing")
		require.NoError(mt, err)
		assert.Nil(mt, full)
	})
}

func TestMongo_DeleteMatched_ByRule(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("delete by rule", func(mt *mtest.T) {
		// Find (empty cursor for body ID collection), then DeleteMany.
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, "mockwave.matched_requests", mtest.FirstBatch), // Find for body IDs
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 2}},                          // DeleteMany requests
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		err := s.DeleteMatched(context.Background(), "rule-1")
		require.NoError(mt, err)
	})
}

func TestMongo_DeleteMatched_All(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("delete all", func(mt *mtest.T) {
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, "mockwave.matched_requests", mtest.FirstBatch), // Find (empty)
			bson.D{{Key: "ok", Value: 1}, {Key: "n", Value: 0}},                          // DeleteMany requests
		)
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		err := s.DeleteMatched(context.Background(), "")
		require.NoError(mt, err)
	})
}

// TestMongo_GetMatched_BinaryBody verifies that a request body stored as
// primitive.Binary (what bson produces for []byte) is decoded back to []byte.
func TestMongo_GetMatched_BinaryBody(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("binary body decoded", func(mt *mtest.T) {
		bodyBytes := []byte(`{"q":"test"}`)
		req := matched.Request{ID: "r1", RuleID: "rule-1", Method: "GET", Path: "/x", RequestBodyID: "rb1"}

		// First FindOne: the request doc. cursorID=0 means exhausted (no killCursors needed).
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "mockwave.matched_requests", mtest.FirstBatch,
			bson.D{
				{Key: "_id", Value: "r1"},
				{Key: "rule_id", Value: "rule-1"},
				{Key: "data", Value: req},
			},
		))
		// Second FindOne: the body doc — body as primitive.Binary (what bson actually stores).
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "mockwave.matched_request_bodies", mtest.FirstBatch,
			bson.D{
				{Key: "_id", Value: "rb1"},
				{Key: "body", Value: primitive.Binary{Subtype: 0x00, Data: bodyBytes}},
			},
		))

		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		full, err := s.GetMatched(context.Background(), "rule-1", "r1")
		require.NoError(mt, err)
		require.NotNil(mt, full)
		assert.Equal(mt, bodyBytes, full.RequestBody)
	})
}

// TestMongo_GetMatched_WrongRule verifies that GetMatched returns (nil, nil)
// when the id exists but belongs to a different rule.
func TestMongo_GetMatched_WrongRule(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("wrong rule returns nil", func(mt *mtest.T) {
		// MongoDB filter includes rule_id, so no document matches → ErrNoDocuments.
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "mockwave.matched_requests", mtest.FirstBatch))
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		full, err := s.GetMatched(context.Background(), "wrong-rule", "r1")
		require.NoError(mt, err)
		assert.Nil(mt, full)
	})
}

func TestMongo_SweepExpired_Noop(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("noop", func(mt *mtest.T) {
		s := mongodb.NewStoreFromClient(mt.Client, "mockwave")
		n, err := s.SweepExpired(context.Background(), time.Now().Unix())
		require.NoError(mt, err)
		assert.Equal(mt, 0, n)
	})
}
