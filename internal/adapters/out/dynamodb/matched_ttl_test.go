package dynamostore_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	dynamostore "github.com/mockwave/mockwave/internal/adapters/out/dynamodb"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSaveMatched_BodyRowsCarryTTL pins that out-of-line body rows expire.
//
// DynamoDB only expires an item that carries the ttl attribute. Request rows
// had it and body rows did not, so every captured request left one or two body
// rows behind permanently and the table could only grow.
func TestSaveMatched_BodyRowsCarryTTL(t *testing.T) {
	m := &mockDynamo{}
	st := dynamostore.NewStoreFromClient(m, dynamostore.Config{
		RulesTable: "rules", MatchedTable: "matched",
	})

	const ttl = int64(1893456000) // fixed epoch second
	reqs := []matched.Request{{
		ID: "req-1", RuleID: "rule-1", TTL: ttl,
		RequestBodyID: "rb-1", ResponseBodyID: "sb-1",
	}}
	reqBodies := []matched.RequestBody{{ID: "rb-1", Body: []byte(`{"a":1}`), TTL: ttl}}
	respBodies := []matched.ResponseBody{{ID: "sb-1", Body: map[string]any{"b": 2}, TTL: ttl}}

	require.NoError(t, st.SaveMatched(context.Background(), reqs, reqBodies, respBodies))
	require.Len(t, m.putItems, 3, "one request row plus two body rows")

	for _, in := range m.putItems {
		sk := in.Item["sk"].(*types.AttributeValueMemberS).Value
		got, ok := in.Item["ttl"]
		require.Truef(t, ok, "row %q written without a ttl attribute; DynamoDB will never expire it", sk)
		assert.Equalf(t, "1893456000", got.(*types.AttributeValueMemberN).Value,
			"row %q has the wrong ttl", sk)
	}
}

// TestSaveMatched_ZeroTTLOmitsAttribute keeps the existing contract that a zero
// TTL means "no expiry", rather than writing ttl=0 which DynamoDB would treat
// as an epoch far in the past and delete immediately.
func TestSaveMatched_ZeroTTLOmitsAttribute(t *testing.T) {
	m := &mockDynamo{}
	st := dynamostore.NewStoreFromClient(m, dynamostore.Config{
		RulesTable: "rules", MatchedTable: "matched",
	})

	require.NoError(t, st.SaveMatched(context.Background(),
		[]matched.Request{{ID: "req-1", RuleID: "rule-1"}},
		[]matched.RequestBody{{ID: "rb-1", Body: []byte(`{}`)}},
		[]matched.ResponseBody{{ID: "sb-1", Body: map[string]any{}}},
	))
	require.Len(t, m.putItems, 3)
	for _, in := range m.putItems {
		sk := in.Item["sk"].(*types.AttributeValueMemberS).Value
		_, ok := in.Item["ttl"]
		assert.Falsef(t, ok, "row %q should have no ttl attribute when TTL is zero", sk)
	}
}
