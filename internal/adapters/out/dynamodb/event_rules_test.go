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

// mockDynamoWithScanErr wraps mockDynamo to inject a per-table Scan error.
// We embed the full DynamoClient interface and delegate everything to the
// underlying mockDynamo, only overriding Scan.
type mockDynamoWithScanErr struct {
	mockDynamo
	scanErr map[string]error // keyed by table name
}

func (m *mockDynamoWithScanErr) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	tbl := aws.ToString(in.TableName)
	if err, ok := m.scanErr[tbl]; ok {
		return nil, err
	}
	if out, ok := m.mockDynamo.scanOut[tbl]; ok {
		return out, nil
	}
	return &dynamodb.ScanOutput{}, nil
}

func eventRuleItem(r domain.EventRule) map[string]types.AttributeValue {
	data, _ := json.Marshal(r)
	return map[string]types.AttributeValue{
		"id":   &types.AttributeValueMemberS{Value: r.ID},
		"data": &types.AttributeValueMemberS{Value: string(data)},
	}
}

func TestDynamo_GetEventRules_ReturnsParsedRules(t *testing.T) {
	rule := domain.EventRule{
		ID:    "er1",
		Match: domain.EventMatch{Service: domain.EventServiceSNS},
	}
	client := &mockDynamo{
		scanOut: map[string]*dynamodb.ScanOutput{
			"event-rules": {Items: []map[string]types.AttributeValue{eventRuleItem(rule)}},
		},
	}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable: "rules", SimsTable: "sims", EventRulesTable: "event-rules",
	})
	rules, err := s.GetEventRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "er1", rules[0].ID)
	assert.Equal(t, domain.EventServiceSNS, rules[0].Match.Service)
}

func TestDynamo_GetEventRules_MissingTableReturnsNil(t *testing.T) {
	client := &mockDynamoWithScanErr{
		scanErr: map[string]error{
			"event-rules": &types.ResourceNotFoundException{Message: aws.String("table not found")},
		},
	}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable: "rules", SimsTable: "sims", EventRulesTable: "event-rules",
	})
	rules, err := s.GetEventRules()
	require.NoError(t, err, "missing table must not be returned as an error")
	assert.Nil(t, rules)
}

func TestDynamo_SaveEventRule(t *testing.T) {
	client := &mockDynamo{}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable: "rules", SimsTable: "sims", EventRulesTable: "event-rules",
	})
	rule := domain.EventRule{
		ID:    "er1",
		Match: domain.EventMatch{Service: domain.EventServiceSQS},
	}
	require.NoError(t, s.SaveEventRule(rule))
	require.Len(t, client.putItems, 1)
	assert.Equal(t, "event-rules", aws.ToString(client.putItems[0].TableName))
	idAttr, ok := client.putItems[0].Item["id"].(*types.AttributeValueMemberS)
	require.True(t, ok)
	assert.Equal(t, "er1", idAttr.Value)
	_, hasData := client.putItems[0].Item["data"]
	assert.True(t, hasData, "item must contain a data attribute")
}

func TestDynamo_DeleteEventRule(t *testing.T) {
	client := &mockDynamo{}
	s := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable: "rules", SimsTable: "sims", EventRulesTable: "event-rules",
	})
	require.NoError(t, s.DeleteEventRule("er1"))
	require.Len(t, client.delItems, 1)
	assert.Equal(t, "event-rules", aws.ToString(client.delItems[0].TableName))
	keyAttr, ok := client.delItems[0].Key["id"].(*types.AttributeValueMemberS)
	require.True(t, ok)
	assert.Equal(t, "er1", keyAttr.Value)
}
