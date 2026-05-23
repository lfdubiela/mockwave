package mongodb

import (
	"context"
	"fmt"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/store"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var _ store.DataStore = (*Store)(nil) // compile-time interface check

const (
	colRules       = "rules"
	colSims        = "simulations"
	connectTimeout = 10 * time.Second
)

// ruleDoc is the MongoDB document shape for a Rule.
type ruleDoc struct {
	ID   string      `bson:"_id"`
	Data domain.Rule `bson:"data"`
}

// simDoc is the MongoDB document shape for a Simulation.
type simDoc struct {
	ID   string            `bson:"_id"`
	Data domain.Simulation `bson:"data"`
}

// Store is a DataStore backed by MongoDB.
type Store struct {
	rules *mongo.Collection
	sims  *mongo.Collection
}

// NewStore creates a Store connected to the given MongoDB URI and database.
// Returns an error if the connection or initial ping fails.
func NewStore(uri, dbName string) (*Store, error) {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongodb: connect: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("mongodb: ping: %w", err)
	}
	return NewStoreFromClient(client, dbName), nil
}

// NewStoreFromClient creates a Store using an existing mongo.Client.
// Use in tests to inject the mtest mock client.
func NewStoreFromClient(client *mongo.Client, dbName string) *Store {
	db := client.Database(dbName)
	return &Store{
		rules: db.Collection(colRules),
		sims:  db.Collection(colSims),
	}
}

func (s *Store) GetRules() ([]domain.Rule, error) {
	ctx := context.Background()
	cur, err := s.rules.Find(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("mongodb: find rules: %w", err)
	}
	defer cur.Close(ctx)
	var docs []ruleDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongodb: decode rules: %w", err)
	}
	rules := make([]domain.Rule, len(docs))
	for i, d := range docs {
		rules[i] = d.Data
	}
	return rules, nil
}

func (s *Store) GetSimulation(id string) (*domain.Simulation, error) {
	ctx := context.Background()
	cur, err := s.sims.Find(ctx, bson.D{{Key: "_id", Value: id}})
	if err != nil {
		return nil, fmt.Errorf("mongodb: find simulation %q: %w", id, err)
	}
	defer cur.Close(ctx)
	var docs []simDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongodb: decode simulation: %w", err)
	}
	if len(docs) == 0 {
		return nil, nil // not found
	}
	sim := docs[0].Data
	return &sim, nil
}

func (s *Store) SaveRule(r domain.Rule) error {
	ctx := context.Background()
	filter := bson.D{{Key: "_id", Value: r.ID}}
	update := bson.D{{Key: "$set", Value: ruleDoc{ID: r.ID, Data: r}}}
	_, err := s.rules.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("mongodb: upsert rule %q: %w", r.ID, err)
	}
	return nil
}

func (s *Store) SaveSimulation(sim domain.Simulation) error {
	ctx := context.Background()
	filter := bson.D{{Key: "_id", Value: sim.ID}}
	update := bson.D{{Key: "$set", Value: simDoc{ID: sim.ID, Data: sim}}}
	_, err := s.sims.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("mongodb: upsert simulation %q: %w", sim.ID, err)
	}
	return nil
}

func (s *Store) DeleteRule(id string) error {
	ctx := context.Background()
	if _, err := s.rules.DeleteOne(ctx, bson.D{{Key: "_id", Value: id}}); err != nil {
		return fmt.Errorf("mongodb: delete rule %q: %w", id, err)
	}
	return nil
}

func (s *Store) DeleteSimulation(id string) error {
	ctx := context.Background()
	if _, err := s.sims.DeleteOne(ctx, bson.D{{Key: "_id", Value: id}}); err != nil {
		return fmt.Errorf("mongodb: delete simulation %q: %w", id, err)
	}
	return nil
}
