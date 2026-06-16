package mongodb

import (
	"context"
	"fmt"
	"time"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// compile-time MatchedStore check.
var _ store.MatchedStore = (*Store)(nil)

const (
	colMatchedRequests = "matched_requests"
	colMatchedBodies   = "matched_request_bodies"
)

// matchedRequestDoc is the MongoDB document shape for a matched request.
type matchedRequestDoc struct {
	ID       string          `bson:"_id"`
	RuleID   string          `bson:"rule_id"`
	Data     matched.Request `bson:"data"`
	ExpireAt *time.Time      `bson:"expire_at,omitempty"`
}

// matchedBodyDoc is the MongoDB document shape for a request/response body.
type matchedBodyDoc struct {
	ID   string      `bson:"_id"`
	Body interface{} `bson:"body"`
}

// ensureMatchedIndexes creates the TTL and query indexes for matched collections.
// Called lazily on first use; idempotent (MongoDB ignores duplicate index creation
// if the definition matches).
func (s *Store) ensureMatchedIndexes(ctx context.Context) error {
	reqs := s.client.Database(s.dbName).Collection(colMatchedRequests)

	// TTL index on expire_at.
	_, err := reqs.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expire_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0).SetSparse(true),
	})
	if err != nil {
		return fmt.Errorf("mongodb: create matched TTL index: %w", err)
	}

	// Compound index for rule queries: rule_id ASC, id DESC (newest first).
	_, err = reqs.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "rule_id", Value: 1}, {Key: "_id", Value: -1}},
	})
	if err != nil {
		return fmt.Errorf("mongodb: create matched compound index: %w", err)
	}
	return nil
}

// SaveMatched upserts a batch of requests and their out-of-line bodies.
func (s *Store) SaveMatched(ctx context.Context, reqs []matched.Request, reqBodies []matched.RequestBody, respBodies []matched.ResponseBody) error {
	db := s.client.Database(s.dbName)
	reqsColl := db.Collection(colMatchedRequests)
	bodiesColl := db.Collection(colMatchedBodies)

	for _, r := range reqs {
		doc := matchedRequestDoc{
			ID:     r.ID,
			RuleID: r.RuleID,
			Data:   r,
		}
		if r.TTL > 0 {
			t := time.Unix(r.TTL, 0)
			doc.ExpireAt = &t
		}
		filter := bson.D{{Key: "_id", Value: r.ID}}
		update := bson.D{{Key: "$set", Value: doc}}
		if _, err := reqsColl.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true)); err != nil {
			return fmt.Errorf("mongodb: upsert matched request %q: %w", r.ID, err)
		}
	}
	for _, rb := range reqBodies {
		doc := matchedBodyDoc{ID: rb.ID, Body: rb.Body}
		filter := bson.D{{Key: "_id", Value: rb.ID}}
		update := bson.D{{Key: "$set", Value: doc}}
		if _, err := bodiesColl.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true)); err != nil {
			return fmt.Errorf("mongodb: upsert req body %q: %w", rb.ID, err)
		}
	}
	for _, sb := range respBodies {
		doc := matchedBodyDoc{ID: sb.ID, Body: sb.Body}
		filter := bson.D{{Key: "_id", Value: sb.ID}}
		update := bson.D{{Key: "$set", Value: doc}}
		if _, err := bodiesColl.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true)); err != nil {
			return fmt.Errorf("mongodb: upsert resp body %q: %w", sb.ID, err)
		}
	}
	return nil
}

// ListMatched returns a filtered, paginated page for a rule, newest first.
func (s *Store) ListMatched(ctx context.Context, ruleID string, q store.MatchedQuery) (store.MatchedPage, error) {
	db := s.client.Database(s.dbName)
	coll := db.Collection(colMatchedRequests)

	afterID, _ := matched.DecodeCursor(q.Cursor)
	limit := q.EffectiveLimit()

	filter := bson.D{}
	if ruleID != "" {
		filter = bson.D{{Key: "rule_id", Value: ruleID}}
	}
	if afterID != "" {
		filter = append(filter, bson.E{Key: "_id", Value: bson.D{{Key: "$lt", Value: afterID}}})
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "_id", Value: -1}}).
		SetLimit(int64(limit * 10)) // over-fetch for client-side filtering

	cur, err := coll.Find(ctx, filter, findOpts)
	if err != nil {
		return store.MatchedPage{}, fmt.Errorf("mongodb: find matched requests: %w", err)
	}
	defer cur.Close(ctx)

	var docs []matchedRequestDoc
	if err := cur.All(ctx, &docs); err != nil {
		return store.MatchedPage{}, fmt.Errorf("mongodb: decode matched requests: %w", err)
	}

	var result []matched.Request
	for _, d := range docs {
		if !q.Matches(d.Data) {
			continue
		}
		result = append(result, d.Data)
		if len(result) == limit {
			break
		}
	}

	page := store.MatchedPage{Items: result}
	if len(result) == limit {
		page.NextCursor = matched.EncodeCursor(result[len(result)-1].ID)
	}
	return page, nil
}

// GetMatched returns the full request (with bodies), or (nil, nil) when absent.
func (s *Store) GetMatched(ctx context.Context, ruleID, id string) (*matched.FullRequest, error) {
	db := s.client.Database(s.dbName)
	reqsColl := db.Collection(colMatchedRequests)
	bodiesColl := db.Collection(colMatchedBodies)

	var doc matchedRequestDoc
	err := reqsColl.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mongodb: get matched request %q: %w", id, err)
	}

	full := &matched.FullRequest{Request: doc.Data}

	if doc.Data.RequestBodyID != "" {
		var bodyDoc matchedBodyDoc
		err := bodiesColl.FindOne(ctx, bson.D{{Key: "_id", Value: doc.Data.RequestBodyID}}).Decode(&bodyDoc)
		if err == nil {
			if b, ok := bodyDoc.Body.([]byte); ok {
				full.RequestBody = b
			}
		}
	}
	if doc.Data.ResponseBodyID != "" {
		var bodyDoc matchedBodyDoc
		err := bodiesColl.FindOne(ctx, bson.D{{Key: "_id", Value: doc.Data.ResponseBodyID}}).Decode(&bodyDoc)
		if err == nil {
			full.ResponseBody = bodyDoc.Body
		}
	}
	return full, nil
}

// DeleteMatched removes a rule's captures (+bodies), or all when ruleID == "".
func (s *Store) DeleteMatched(ctx context.Context, ruleID string) error {
	db := s.client.Database(s.dbName)
	reqsColl := db.Collection(colMatchedRequests)
	bodiesColl := db.Collection(colMatchedBodies)

	filter := bson.D{}
	if ruleID != "" {
		filter = bson.D{{Key: "rule_id", Value: ruleID}}
	}

	// Collect body IDs before deleting requests.
	cur, err := reqsColl.Find(ctx, filter, options.Find().SetProjection(bson.D{
		{Key: "data.request_body_id", Value: 1},
		{Key: "data.response_body_id", Value: 1},
	}))
	if err != nil {
		return fmt.Errorf("mongodb: find matched requests for delete: %w", err)
	}
	defer cur.Close(ctx)
	var docs []matchedRequestDoc
	if err := cur.All(ctx, &docs); err != nil {
		return fmt.Errorf("mongodb: decode matched requests for delete: %w", err)
	}

	bodyIDs := make([]string, 0)
	for _, d := range docs {
		if d.Data.RequestBodyID != "" {
			bodyIDs = append(bodyIDs, d.Data.RequestBodyID)
		}
		if d.Data.ResponseBodyID != "" {
			bodyIDs = append(bodyIDs, d.Data.ResponseBodyID)
		}
	}

	if _, err := reqsColl.DeleteMany(ctx, filter); err != nil {
		return fmt.Errorf("mongodb: delete matched requests: %w", err)
	}
	if len(bodyIDs) > 0 {
		if _, err := bodiesColl.DeleteMany(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: bodyIDs}}}}); err != nil {
			return fmt.Errorf("mongodb: delete matched bodies: %w", err)
		}
	}
	return nil
}

// SweepExpired is a no-op — MongoDB TTL index handles expiry.
func (s *Store) SweepExpired(_ context.Context, _ int64) (int, error) {
	return 0, nil
}
