package dynamostore_test

import (
	"context"
	"encoding/json"
	"strconv"
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
	scanOut      map[string]*dynamodb.ScanOutput    // keyed by table name
	getOut       map[string]*dynamodb.GetItemOutput // keyed by table name
	putItems     []dynamodb.PutItemInput
	delItems     []dynamodb.DeleteItemInput
	updateCount  int
	versionValue int64
}

func (m *mockDynamo) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if out, ok := m.scanOut[aws.ToString(in.TableName)]; ok {
		return out, nil
	}
	return &dynamodb.ScanOutput{}, nil
}

func (m *mockDynamo) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if idAttr, ok := in.Key["id"].(*types.AttributeValueMemberS); ok && idAttr.Value == "__mockwave_config_version__" {
		if m.versionValue == 0 {
			return &dynamodb.GetItemOutput{}, nil
		}
		return &dynamodb.GetItemOutput{Item: map[string]types.AttributeValue{
			"id":      &types.AttributeValueMemberS{Value: "__mockwave_config_version__"},
			"version": &types.AttributeValueMemberN{Value: strconv.FormatInt(m.versionValue, 10)},
		}}, nil
	}
	if m.getOut != nil {
		if out, ok := m.getOut[aws.ToString(in.TableName)]; ok {
			return out, nil
		}
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockDynamo) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	m.updateCount++
	return &dynamodb.UpdateItemOutput{}, nil
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

func TestStore_ConfigVersion_AndBumpOnWrite(t *testing.T) {
	m := &mockDynamo{}
	s := dynamostore.NewStoreFromClient(m, dynamostore.Config{
		RulesTable: "rules", SimsTable: "sims",
	})

	require.NoError(t, s.SaveRule(domain.Rule{ID: "r1", Match: domain.MatchCriteria{Path: "/x"},
		Buckets: []domain.WeightedBucket{{Weight: 100, Action: domain.ActionSimulate, SimulationID: "s1"}}}))
	require.NoError(t, s.SaveSimulation(domain.Simulation{ID: "s1", Protocol: "http"}))
	require.NoError(t, s.DeleteRule("r1"))
	require.NoError(t, s.DeleteSimulation("s1"))
	assert.Equal(t, 4, m.updateCount, "expected one version bump per write")

	m.versionValue = 7
	v, err := s.ConfigVersion()
	require.NoError(t, err)
	assert.Equal(t, int64(7), v)
}

func TestStore_FaultProfileCRUD(t *testing.T) {
	client := &mockDynamo{
		getOut:  map[string]*dynamodb.GetItemOutput{},
		scanOut: map[string]*dynamodb.ScanOutput{},
	}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable: "rules", SimsTable: "sims",
		FaultsTable: "faults", ScenariosTable: "scenarios",
	})

	p := domain.FaultProfile{ID: "fp1", Name: "p", Enabled: true,
		Faults: []domain.Fault{{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}}}}
	require.NoError(t, s.SaveFaultProfile(p))
	require.Len(t, client.putItems, 1)
	assert.Equal(t, "faults", aws.ToString(client.putItems[0].TableName))

	data, _ := json.Marshal(p)
	client.getOut["faults"] = &dynamodb.GetItemOutput{Item: map[string]types.AttributeValue{
		"id":   &types.AttributeValueMemberS{Value: "fp1"},
		"data": &types.AttributeValueMemberS{Value: string(data)},
	}}
	got, err := s.GetFaultProfile("fp1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "p", got.Name)

	client.getOut["faults"] = &dynamodb.GetItemOutput{Item: nil}
	got, err = s.GetFaultProfile("missing")
	require.NoError(t, err)
	assert.Nil(t, got)

	client.scanOut["faults"] = &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{
		{"id": &types.AttributeValueMemberS{Value: "fp1"}, "data": &types.AttributeValueMemberS{Value: string(data)}},
	}}
	list, err := s.ListFaultProfiles()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "fp1", list[0].ID)

	require.NoError(t, s.DeleteFaultProfile("fp1"))
	assert.Equal(t, "faults", aws.ToString(client.delItems[len(client.delItems)-1].TableName))
}

func TestStore_ScenarioCRUD(t *testing.T) {
	client := &mockDynamo{
		getOut:  map[string]*dynamodb.GetItemOutput{},
		scanOut: map[string]*dynamodb.ScanOutput{},
	}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable: "rules", SimsTable: "sims",
		FaultsTable: "faults", ScenariosTable: "scenarios",
	})
	sc := domain.Scenario{ID: "sc1", Name: "n", RuleIDs: []string{"r1"},
		Phases: []domain.ScenarioPhase{{DurationSec: 10, FaultProfileID: "p"}}}

	require.NoError(t, s.SaveScenario(sc))
	require.Len(t, client.putItems, 1)
	assert.Equal(t, "scenarios", aws.ToString(client.putItems[0].TableName))

	data, _ := json.Marshal(sc)
	client.getOut["scenarios"] = &dynamodb.GetItemOutput{Item: map[string]types.AttributeValue{
		"id":   &types.AttributeValueMemberS{Value: "sc1"},
		"data": &types.AttributeValueMemberS{Value: string(data)},
	}}
	got, err := s.GetScenario("sc1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "n", got.Name)

	client.getOut["scenarios"] = &dynamodb.GetItemOutput{Item: nil}
	got, err = s.GetScenario("missing")
	require.NoError(t, err)
	assert.Nil(t, got)

	client.scanOut["scenarios"] = &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{
		{"id": &types.AttributeValueMemberS{Value: "sc1"}, "data": &types.AttributeValueMemberS{Value: string(data)}},
	}}
	list, err := s.ListScenarios()
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, s.DeleteScenario("sc1"))
	assert.Equal(t, "scenarios", aws.ToString(client.delItems[len(client.delItems)-1].TableName))
}
