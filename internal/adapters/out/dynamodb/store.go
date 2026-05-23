package dynamostore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/store"
)

// compile-time interface check
var _ store.DataStore = (*Store)(nil)

// DynamoClient is the subset of DynamoDB operations used by Store.
// Exported so tests can inject a mock without hitting AWS.
type DynamoClient interface {
	Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

// Config holds DynamoDB connection parameters.
type Config struct {
	RulesTable string // DynamoDB table name for rules (PK: "id")
	SimsTable  string // DynamoDB table name for simulations (PK: "id")
	Region     string // AWS region (e.g. "us-east-1")
	Endpoint   string // optional custom endpoint for local DynamoDB
}

// Store is a DataStore backed by DynamoDB.
type Store struct {
	client     DynamoClient
	rulesTable string
	simsTable  string
}

// NewStore creates a Store using the default AWS credential chain
// (env vars, ~/.aws/credentials, IAM role, etc.).
func NewStore(cfg Config) (*Store, error) {
	optFns := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.Region),
	}
	if cfg.Endpoint != "" {
		customResolver := aws.EndpointResolverWithOptionsFunc(
			func(service, region string, opts ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: cfg.Endpoint, HostnameImmutable: true}, nil
			},
		)
		optFns = append(optFns, awsconfig.WithEndpointResolverWithOptions(customResolver))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), optFns...)
	if err != nil {
		return nil, fmt.Errorf("dynamodb: load aws config: %w", err)
	}
	return NewStoreFromClient(dynamodb.NewFromConfig(awsCfg), cfg), nil
}

// NewStoreFromClient creates a Store using the provided DynamoClient.
// Use in tests to inject a mock client.
func NewStoreFromClient(client DynamoClient, cfg Config) *Store {
	return &Store{
		client:     client,
		rulesTable: cfg.RulesTable,
		simsTable:  cfg.SimsTable,
	}
}

func (s *Store) GetRules() ([]domain.Rule, error) {
	out, err := s.client.Scan(context.Background(), &dynamodb.ScanInput{
		TableName: aws.String(s.rulesTable),
	})
	if err != nil {
		return nil, fmt.Errorf("dynamodb: scan rules: %w", err)
	}
	rules := make([]domain.Rule, 0, len(out.Items))
	for _, item := range out.Items {
		dataAttr, ok := item["data"].(*types.AttributeValueMemberS)
		if !ok {
			continue
		}
		var r domain.Rule
		if err := json.Unmarshal([]byte(dataAttr.Value), &r); err != nil {
			return nil, fmt.Errorf("dynamodb: unmarshal rule: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

func (s *Store) GetSimulation(id string) (*domain.Simulation, error) {
	out, err := s.client.GetItem(context.Background(), &dynamodb.GetItemInput{
		TableName: aws.String(s.simsTable),
		Key: map[string]types.AttributeValue{
			"id": &types.AttributeValueMemberS{Value: id},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamodb: get simulation %q: %w", id, err)
	}
	if out.Item == nil {
		return nil, nil // not found
	}
	dataAttr, ok := out.Item["data"].(*types.AttributeValueMemberS)
	if !ok {
		return nil, nil
	}
	var sim domain.Simulation
	if err := json.Unmarshal([]byte(dataAttr.Value), &sim); err != nil {
		return nil, fmt.Errorf("dynamodb: unmarshal simulation: %w", err)
	}
	return &sim, nil
}

func (s *Store) SaveRule(r domain.Rule) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("dynamodb: marshal rule: %w", err)
	}
	_, err = s.client.PutItem(context.Background(), &dynamodb.PutItemInput{
		TableName: aws.String(s.rulesTable),
		Item: map[string]types.AttributeValue{
			"id":   &types.AttributeValueMemberS{Value: r.ID},
			"data": &types.AttributeValueMemberS{Value: string(data)},
		},
	})
	return wrapErr(err, "put rule %q", r.ID)
}

func (s *Store) SaveSimulation(sim domain.Simulation) error {
	data, err := json.Marshal(sim)
	if err != nil {
		return fmt.Errorf("dynamodb: marshal simulation: %w", err)
	}
	_, err = s.client.PutItem(context.Background(), &dynamodb.PutItemInput{
		TableName: aws.String(s.simsTable),
		Item: map[string]types.AttributeValue{
			"id":   &types.AttributeValueMemberS{Value: sim.ID},
			"data": &types.AttributeValueMemberS{Value: string(data)},
		},
	})
	return wrapErr(err, "put simulation %q", sim.ID)
}

func (s *Store) DeleteRule(id string) error {
	_, err := s.client.DeleteItem(context.Background(), &dynamodb.DeleteItemInput{
		TableName: aws.String(s.rulesTable),
		Key:       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}},
	})
	return wrapErr(err, "delete rule %q", id)
}

func (s *Store) DeleteSimulation(id string) error {
	_, err := s.client.DeleteItem(context.Background(), &dynamodb.DeleteItemInput{
		TableName: aws.String(s.simsTable),
		Key:       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}},
	})
	return wrapErr(err, "delete simulation %q", id)
}

func wrapErr(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("dynamodb: "+format+": %w", append(args, err)...)
}
