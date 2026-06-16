//go:build integration

package dynamostore_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	dynamostore "github.com/mockwave/mockwave/internal/adapters/out/dynamodb"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const matchedTable = "mockwave-matched-requests-test"

func createMatchedTable(t *testing.T, client *dynamodb.Client, name string) {
	t.Helper()
	_, _ = client.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{
		TableName: aws.String(name),
	})
	_, err := client.CreateTable(context.Background(), &dynamodb.CreateTableInput{
		TableName: aws.String(name),
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("rule_id"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("sk"), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("rule_id"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("sk"), KeyType: types.KeyTypeRange},
		},
		BillingMode: types.BillingModePayPerRequest,
	})
	require.NoError(t, err)
}

func newMatchedStore(t *testing.T) (*dynamostore.Store, *dynamodb.Client) {
	t.Helper()
	endpoint := dynamoEndpoint(t)
	client := newLocalClient(t, endpoint)
	createTable(t, client, rulesTable) // needed for compile-time constraint
	createMatchedTable(t, client, matchedTable)
	st := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable:   rulesTable,
		MatchedTable: matchedTable,
	})
	return st, client
}

func TestDynamo_SaveAndListMatched(t *testing.T) {
	st, _ := newMatchedStore(t)
	ctx := context.Background()

	reqs := []matched.Request{
		{
			ID:             matched.NewID(),
			RuleID:         "rule-1",
			At:             time.Now(),
			Protocol:       "http",
			Method:         "GET",
			Path:           "/ping",
			ResponseStatus: 200,
		},
		{
			ID:             matched.NewID(),
			RuleID:         "rule-1",
			At:             time.Now(),
			Protocol:       "http",
			Method:         "POST",
			Path:           "/create",
			ResponseStatus: 201,
		},
	}

	require.NoError(t, st.SaveMatched(ctx, reqs, nil, nil))

	page, err := st.ListMatched(ctx, "rule-1", matched.Query{})
	require.NoError(t, err)
	assert.Len(t, page.Items, 2)
	// newest first (desc by ID)
	assert.True(t, page.Items[0].ID >= page.Items[1].ID)
}

func TestDynamo_ListMatched_FilterByMethod(t *testing.T) {
	st, _ := newMatchedStore(t)
	ctx := context.Background()

	reqs := []matched.Request{
		{ID: matched.NewID(), RuleID: "rule-2", Method: "GET", Path: "/a", At: time.Now()},
		{ID: matched.NewID(), RuleID: "rule-2", Method: "POST", Path: "/b", At: time.Now()},
	}
	require.NoError(t, st.SaveMatched(ctx, reqs, nil, nil))

	page, err := st.ListMatched(ctx, "rule-2", matched.Query{Method: "GET"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "GET", page.Items[0].Method)
}

func TestDynamo_GetMatched_Found(t *testing.T) {
	st, _ := newMatchedStore(t)
	ctx := context.Background()

	id := matched.NewID()
	reqs := []matched.Request{
		{ID: id, RuleID: "rule-3", Method: "GET", Path: "/x", At: time.Now()},
	}
	require.NoError(t, st.SaveMatched(ctx, reqs, nil, nil))

	full, err := st.GetMatched(ctx, "rule-3", id)
	require.NoError(t, err)
	require.NotNil(t, full)
	assert.Equal(t, id, full.ID)
}

func TestDynamo_GetMatched_NotFound(t *testing.T) {
	st, _ := newMatchedStore(t)
	ctx := context.Background()

	full, err := st.GetMatched(ctx, "rule-x", "nonexistent-id")
	require.NoError(t, err)
	assert.Nil(t, full)
}

func TestDynamo_DeleteMatched_ByRule(t *testing.T) {
	st, _ := newMatchedStore(t)
	ctx := context.Background()

	reqs := []matched.Request{
		{ID: matched.NewID(), RuleID: "rule-del", Method: "GET", Path: "/a", At: time.Now()},
		{ID: matched.NewID(), RuleID: "rule-keep", Method: "GET", Path: "/b", At: time.Now()},
	}
	require.NoError(t, st.SaveMatched(ctx, reqs, nil, nil))

	require.NoError(t, st.DeleteMatched(ctx, "rule-del"))

	page, err := st.ListMatched(ctx, "rule-del", matched.Query{})
	require.NoError(t, err)
	assert.Empty(t, page.Items)
}

func TestDynamo_SweepExpired_Noop(t *testing.T) {
	st, _ := newMatchedStore(t)
	n, err := st.SweepExpired(context.Background(), time.Now().Unix())
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestDynamo_ListMatched_AllRules(t *testing.T) {
	st, _ := newMatchedStore(t)
	ctx := context.Background()

	reqs := []matched.Request{
		{ID: matched.NewID(), RuleID: "r-a", Method: "GET", Path: "/a", At: time.Now()},
		{ID: matched.NewID(), RuleID: "r-b", Method: "GET", Path: "/b", At: time.Now()},
	}
	require.NoError(t, st.SaveMatched(ctx, reqs, nil, nil))

	// ruleID="" lists across all rules.
	page, err := st.ListMatched(ctx, "", matched.Query{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(page.Items), 2)
}
