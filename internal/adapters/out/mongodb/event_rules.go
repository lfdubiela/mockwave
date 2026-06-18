package mongodb

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/mockwave/mockwave/domain"
)

type eventRuleDoc struct {
	ID   string           `bson:"_id"`
	Data domain.EventRule `bson:"data"`
}

// GetEventRules returns all AWS event rules. A missing collection yields an
// empty cursor (no error), so deployments without event rules start cleanly.
func (s *Store) GetEventRules() ([]domain.EventRule, error) {
	ctx := context.Background()
	cur, err := s.eventRules.Find(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("mongodb: find event rules: %w", err)
	}
	defer cur.Close(ctx)
	var docs []eventRuleDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongodb: decode event rules: %w", err)
	}
	rules := make([]domain.EventRule, len(docs))
	for i, d := range docs {
		rules[i] = d.Data
	}
	return rules, nil
}

// SaveEventRule upserts an event rule by id and bumps the config version.
func (s *Store) SaveEventRule(r domain.EventRule) error {
	ctx := context.Background()
	filter := bson.D{{Key: "_id", Value: r.ID}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "data", Value: r}}}}
	if _, err := s.eventRules.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true)); err != nil {
		return fmt.Errorf("mongodb: upsert event rule %q: %w", r.ID, err)
	}
	return s.bumpVersion()
}

// DeleteEventRule removes an event rule by id and bumps the config version.
func (s *Store) DeleteEventRule(id string) error {
	ctx := context.Background()
	if _, err := s.eventRules.DeleteOne(ctx, bson.D{{Key: "_id", Value: id}}); err != nil {
		return fmt.Errorf("mongodb: delete event rule %q: %w", id, err)
	}
	return s.bumpVersion()
}
