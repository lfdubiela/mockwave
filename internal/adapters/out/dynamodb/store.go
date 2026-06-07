package dynamostore

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/store"
)

// compile-time interface checks
var (
	_ store.DataStore      = (*Store)(nil)
	_ store.VersionedStore = (*Store)(nil)
)

// versionItemID is the reserved rules-table key holding the config-version
// marker. GetRules skips it naturally (it has no "data" string attribute).
const versionItemID = "__mockwave_config_version__"

// DynamoClient is the subset of DynamoDB operations used by Store.
// Exported so tests can inject a mock without hitting AWS.
type DynamoClient interface {
	Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
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
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.Region),
	)
	if err != nil {
		return nil, fmt.Errorf("dynamodb: load aws config: %w", err)
	}
	client := dynamodb.NewFromConfig(awsCfg, func(o *dynamodb.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	return NewStoreFromClient(client, cfg), nil
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

func (s *Store) ListSimulations() ([]domain.Simulation, error) {
	out, err := s.client.Scan(context.Background(), &dynamodb.ScanInput{
		TableName: aws.String(s.simsTable),
	})
	if err != nil {
		return nil, fmt.Errorf("dynamodb: scan simulations: %w", err)
	}
	sims := make([]domain.Simulation, 0, len(out.Items))
	for _, item := range out.Items {
		dataAttr, ok := item["data"].(*types.AttributeValueMemberS)
		if !ok {
			continue
		}
		var sim domain.Simulation
		if err := json.Unmarshal([]byte(dataAttr.Value), &sim); err != nil {
			return nil, fmt.Errorf("dynamodb: unmarshal simulation: %w", err)
		}
		sims = append(sims, sim)
	}
	return sims, nil
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
	if err := wrapErr(err, "put rule %q", r.ID); err != nil {
		return err
	}
	return s.bumpVersion()
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
	if err := wrapErr(err, "put simulation %q", sim.ID); err != nil {
		return err
	}
	return s.bumpVersion()
}

func (s *Store) DeleteRule(id string) error {
	_, err := s.client.DeleteItem(context.Background(), &dynamodb.DeleteItemInput{
		TableName: aws.String(s.rulesTable),
		Key:       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}},
	})
	if err := wrapErr(err, "delete rule %q", id); err != nil {
		return err
	}
	return s.bumpVersion()
}

func (s *Store) DeleteSimulation(id string) error {
	_, err := s.client.DeleteItem(context.Background(), &dynamodb.DeleteItemInput{
		TableName: aws.String(s.simsTable),
		Key:       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: id}},
	})
	if err := wrapErr(err, "delete simulation %q", id); err != nil {
		return err
	}
	return s.bumpVersion()
}

// bumpVersion atomically increments the config-version marker in the rules table.
func (s *Store) bumpVersion() error {
	_, err := s.client.UpdateItem(context.Background(), &dynamodb.UpdateItemInput{
		TableName:                 aws.String(s.rulesTable),
		Key:                       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: versionItemID}},
		UpdateExpression:          aws.String("ADD #v :one"),
		ExpressionAttributeNames:  map[string]string{"#v": "version"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":one": &types.AttributeValueMemberN{Value: "1"}},
	})
	return wrapErr(err, "bump config version")
}

// ConfigVersion returns the current config-version marker (0 if absent).
func (s *Store) ConfigVersion() (int64, error) {
	out, err := s.client.GetItem(context.Background(), &dynamodb.GetItemInput{
		TableName: aws.String(s.rulesTable),
		Key:       map[string]types.AttributeValue{"id": &types.AttributeValueMemberS{Value: versionItemID}},
	})
	if err != nil {
		return 0, wrapErr(err, "get config version")
	}
	if out.Item == nil {
		return 0, nil
	}
	n, ok := out.Item["version"].(*types.AttributeValueMemberN)
	if !ok {
		return 0, nil
	}
	v, err := strconv.ParseInt(n.Value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("dynamodb: parse config version: %w", err)
	}
	return v, nil
}

func wrapErr(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("dynamodb: "+format+": %w", append(args, err)...)
}
