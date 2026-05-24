package dynamostore_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/mockwave/mockwave/domain"
	dynamostore "github.com/mockwave/mockwave/internal/adapters/out/dynamodb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDynamo implements dynamostore.DynamoClient for testing.
type mockDynamo struct {
	scanOut  map[string]*dynamodb.ScanOutput    // keyed by table name
	getOut   map[string]*dynamodb.GetItemOutput // keyed by table name
	putItems []dynamodb.PutItemInput
	delItems []dynamodb.DeleteItemInput
}

func (m *mockDynamo) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if out, ok := m.scanOut[aws.ToString(in.TableName)]; ok {
		return out, nil
	}
	return &dynamodb.ScanOutput{}, nil
}

func (m *mockDynamo) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if m.getOut != nil {
		if out, ok := m.getOut[aws.ToString(in.TableName)]; ok {
			return out, nil
		}
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockDynamo) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.putItems = append(m.putItems, *in)
	return &dynamodb.PutItemOutput{}, nil
}

func (m *mockDynamo) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	m.delItems = append(m.delItems, *in)
	return &dynamodb.DeleteItemOutput{}, nil
}

func ruleItem(r domain.Rule) map[string]types.AttributeValue {
	data, _ := json.Marshal(r)
	return map[string]types.AttributeValue{
		"id":   &types.AttributeValueMemberS{Value: r.ID},
		"data": &types.AttributeValueMemberS{Value: string(data)},
	}
}

func simItem(s domain.Simulation) map[string]types.AttributeValue {
	data, _ := json.Marshal(s)
	return map[string]types.AttributeValue{
		"id":   &types.AttributeValueMemberS{Value: s.ID},
		"data": &types.AttributeValueMemberS{Value: string(data)},
	}
}

func TestDynamo_GetRules_Empty(t *testing.T) {
	s := dynamostore.NewStoreFromClient(&mockDynamo{}, dynamostore.Config{RulesTable: "rules", SimsTable: "sims"})
	rules, err := s.GetRules()
	require.NoError(t, err)
	assert.Empty(t, rules)
}

func TestDynamo_GetRules_ReturnsAll(t *testing.T) {
	rule := domain.Rule{
		ID:      "r1",
		Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/ping"},
		Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
	}
	client := &mockDynamo{
		scanOut: map[string]*dynamodb.ScanOutput{
			"rules": {Items: []map[string]types.AttributeValue{ruleItem(rule)}},
		},
	}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{RulesTable: "rules", SimsTable: "sims"})
	rules, err := s.GetRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "r1", rules[0].ID)
	assert.Equal(t, "/ping", rules[0].Match.Path)
}

func TestDynamo_GetSimulation_Found(t *testing.T) {
	sim := domain.Simulation{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}}
	data, _ := json.Marshal(sim)
	client := &mockDynamo{
		getOut: map[string]*dynamodb.GetItemOutput{
			"sims": {
				Item: map[string]types.AttributeValue{
					"id":   &types.AttributeValueMemberS{Value: "s1"},
					"data": &types.AttributeValueMemberS{Value: string(data)},
				},
			},
		},
	}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{RulesTable: "rules", SimsTable: "sims"})
	got, err := s.GetSimulation("s1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "s1", got.ID)
	assert.Equal(t, 200, got.Response.Status)
}

func TestDynamo_GetSimulation_NotFound(t *testing.T) {
	s := dynamostore.NewStoreFromClient(&mockDynamo{}, dynamostore.Config{RulesTable: "rules", SimsTable: "sims"})
	got, err := s.GetSimulation("missing")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestDynamo_SaveRule(t *testing.T) {
	client := &mockDynamo{}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{RulesTable: "rules", SimsTable: "sims"})
	rule := domain.Rule{
		ID:      "r1",
		Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/x"},
		Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}},
	}
	require.NoError(t, s.SaveRule(rule))
	require.Len(t, client.putItems, 1)
	assert.Equal(t, "rules", aws.ToString(client.putItems[0].TableName))
}

func TestDynamo_SaveSimulation(t *testing.T) {
	client := &mockDynamo{}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{RulesTable: "rules", SimsTable: "sims"})
	require.NoError(t, s.SaveSimulation(domain.Simulation{ID: "s1", Protocol: "http"}))
	require.Len(t, client.putItems, 1)
	assert.Equal(t, "sims", aws.ToString(client.putItems[0].TableName))
}

func TestDynamo_DeleteRule(t *testing.T) {
	client := &mockDynamo{}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{RulesTable: "rules", SimsTable: "sims"})
	require.NoError(t, s.DeleteRule("r1"))
	require.Len(t, client.delItems, 1)
	assert.Equal(t, "rules", aws.ToString(client.delItems[0].TableName))
}

func TestDynamo_ListSimulations(t *testing.T) {
	sim := domain.Simulation{ID: "s1", Protocol: "http"}
	client := &mockDynamo{
		scanOut: map[string]*dynamodb.ScanOutput{
			"sims": {Items: []map[string]types.AttributeValue{simItem(sim)}},
		},
	}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{RulesTable: "rules", SimsTable: "sims"})
	sims, err := s.ListSimulations()
	require.NoError(t, err)
	require.Len(t, sims, 1)
	assert.Equal(t, "s1", sims[0].ID)
}

func TestDynamo_DeleteSimulation(t *testing.T) {
	client := &mockDynamo{}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{RulesTable: "rules", SimsTable: "sims"})
	require.NoError(t, s.DeleteSimulation("s1"))
	require.Len(t, client.delItems, 1)
	assert.Equal(t, "sims", aws.ToString(client.delItems[0].TableName))
}
