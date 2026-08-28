package dynamostore_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// pagingDynamo serves each table's items one page at a time, the way DynamoDB
// does once a Scan exceeds its 1MB response cap: every page but the last comes
// back with a LastEvaluatedKey the caller must feed into ExclusiveStartKey.
//
// mockDynamo in store_test.go always returns the whole table in one response,
// so it cannot tell a paginating reader from a truncating one.
type pagingDynamo struct {
	pages     map[string][][]map[string]types.AttributeValue
	scanCalls map[string]int
	deleted   []map[string]types.AttributeValue
}

func newPagingDynamo() *pagingDynamo {
	return &pagingDynamo{
		pages:     map[string][][]map[string]types.AttributeValue{},
		scanCalls: map[string]int{},
	}
}

// setJSONPages splits ids into pages of one item each, storing every value as
// the JSON "data" attribute the store expects.
func (m *pagingDynamo) setJSONPages(table string, payloads []string) {
	var pages [][]map[string]types.AttributeValue
	for _, p := range payloads {
		pages = append(pages, []map[string]types.AttributeValue{
			{"data": &types.AttributeValueMemberS{Value: p}},
		})
	}
	m.pages[table] = pages
}

func (m *pagingDynamo) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	table := aws.ToString(in.TableName)
	m.scanCalls[table]++
	pages := m.pages[table]

	idx := 0
	if in.ExclusiveStartKey != nil {
		if n, ok := in.ExclusiveStartKey["page"].(*types.AttributeValueMemberN); ok {
			v, err := strconv.Atoi(n.Value)
			if err != nil {
				return nil, fmt.Errorf("bad page cursor %q", n.Value)
			}
			idx = v
		}
	}
	if idx >= len(pages) {
		return &dynamodb.ScanOutput{}, nil
	}
	out := &dynamodb.ScanOutput{Items: pages[idx]}
	if idx+1 < len(pages) {
		out.LastEvaluatedKey = map[string]types.AttributeValue{
			"page": &types.AttributeValueMemberN{Value: strconv.Itoa(idx + 1)},
		}
	}
	return out, nil
}

func (m *pagingDynamo) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}
func (m *pagingDynamo) PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}
func (m *pagingDynamo) UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{}, nil
}
func (m *pagingDynamo) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	m.deleted = append(m.deleted, in.Key)
	return &dynamodb.DeleteItemOutput{}, nil
}

func pagedCfg() dynamostore.Config {
	return dynamostore.Config{
		RulesTable: "rules", SimsTable: "sims", FaultsTable: "faults",
		ScenariosTable: "scenarios", EventRulesTable: "events",
		MatchedTable: "matched",
	}
}

func jsonEach(t *testing.T, vals ...any) []string {
	t.Helper()
	out := make([]string, len(vals))
	for i, v := range vals {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		out[i] = string(b)
	}
	return out
}

func TestGetRules_ReadsEveryPage(t *testing.T) {
	m := newPagingDynamo()
	m.setJSONPages("rules", jsonEach(t,
		domain.Rule{ID: "r1", Match: domain.MatchCriteria{Path: "/a"}},
		domain.Rule{ID: "r2", Match: domain.MatchCriteria{Path: "/b"}},
		domain.Rule{ID: "r3", Match: domain.MatchCriteria{Path: "/c"}},
	))
	st := dynamostore.NewStoreFromClient(m, pagedCfg())

	got, err := st.GetRules()
	require.NoError(t, err)

	var ids []string
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"r1", "r2", "r3"}, ids,
		"rules past the first Scan page must not be dropped")
	assert.Equal(t, 3, m.scanCalls["rules"], "should Scan once per page")
}

func TestListSimulations_ReadsEveryPage(t *testing.T) {
	m := newPagingDynamo()
	m.setJSONPages("sims", jsonEach(t,
		domain.Simulation{ID: "s1"}, domain.Simulation{ID: "s2"}, domain.Simulation{ID: "s3"},
	))
	st := dynamostore.NewStoreFromClient(m, pagedCfg())

	got, err := st.ListSimulations()
	require.NoError(t, err)
	assert.Len(t, got, 3, "simulations past the first Scan page must not be dropped")
}

func TestListFaultProfiles_ReadsEveryPage(t *testing.T) {
	m := newPagingDynamo()
	m.setJSONPages("faults", jsonEach(t,
		domain.FaultProfile{ID: "f1"}, domain.FaultProfile{ID: "f2"}, domain.FaultProfile{ID: "f3"},
	))
	st := dynamostore.NewStoreFromClient(m, pagedCfg())

	got, err := st.ListFaultProfiles()
	require.NoError(t, err)
	assert.Len(t, got, 3, "fault profiles past the first Scan page must not be dropped")
}

func TestListScenarios_ReadsEveryPage(t *testing.T) {
	m := newPagingDynamo()
	m.setJSONPages("scenarios", jsonEach(t,
		domain.Scenario{ID: "sc1"}, domain.Scenario{ID: "sc2"}, domain.Scenario{ID: "sc3"},
	))
	st := dynamostore.NewStoreFromClient(m, pagedCfg())

	got, err := st.ListScenarios()
	require.NoError(t, err)
	assert.Len(t, got, 3, "scenarios past the first Scan page must not be dropped")
}

func TestGetEventRules_ReadsEveryPage(t *testing.T) {
	m := newPagingDynamo()
	m.setJSONPages("events", jsonEach(t,
		domain.EventRule{ID: "e1"}, domain.EventRule{ID: "e2"}, domain.EventRule{ID: "e3"},
	))
	st := dynamostore.NewStoreFromClient(m, pagedCfg())

	got, err := st.GetEventRules()
	require.NoError(t, err)
	assert.Len(t, got, 3, "event rules past the first Scan page must not be dropped")
}

// setKeyPages splits rule_id/sk key pairs into one-item pages, matching the
// shape deleteMatchedByScan projects and feeds to batchDelete.
func (m *pagingDynamo) setKeyPages(table string, ruleID string, sks []string) {
	var pages [][]map[string]types.AttributeValue
	for _, sk := range sks {
		pages = append(pages, []map[string]types.AttributeValue{
			{
				"rule_id": &types.AttributeValueMemberS{Value: ruleID},
				"sk":      &types.AttributeValueMemberS{Value: sk},
			},
		})
	}
	m.pages[table] = pages
}

// TestDeleteMatched_ScanFallbackDeletesEveryPage covers the path taken when the
// client has no Query support: DeleteMatched falls back to a Scan, which must
// page or it silently leaves most of a rule's matched requests behind.
//
// pagingDynamo deliberately implements no Query method, which selects that path.
func TestDeleteMatched_ScanFallbackDeletesEveryPage(t *testing.T) {
	m := newPagingDynamo()
	m.setKeyPages("matched", "rule-1", []string{"sk1", "sk2", "sk3"})
	st := dynamostore.NewStoreFromClient(m, pagedCfg())

	require.NoError(t, st.DeleteMatched(context.Background(), "rule-1"))

	var sks []string
	for _, k := range m.deleted {
		sks = append(sks, k["sk"].(*types.AttributeValueMemberS).Value)
	}
	assert.Equal(t, []string{"sk1", "sk2", "sk3"}, sks,
		"matched requests past the first Scan page must still be deleted")
}

// emptyKeyDynamo returns an empty-but-non-nil LastEvaluatedKey. A loop testing
// that key against nil rather than length would never terminate; the call cap
// keeps this test from hanging if that regresses.
type emptyKeyDynamo struct {
	calls int
}

func (m *emptyKeyDynamo) Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	m.calls++
	if m.calls > 50 {
		return &dynamodb.ScanOutput{}, nil // bail out instead of hanging the suite
	}
	return &dynamodb.ScanOutput{
		Items:            []map[string]types.AttributeValue{},
		LastEvaluatedKey: map[string]types.AttributeValue{},
	}, nil
}

func (m *emptyKeyDynamo) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}
func (m *emptyKeyDynamo) PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}
func (m *emptyKeyDynamo) UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{}, nil
}
func (m *emptyKeyDynamo) DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

func TestGetRules_EmptyLastEvaluatedKeyTerminates(t *testing.T) {
	m := &emptyKeyDynamo{}
	st := dynamostore.NewStoreFromClient(m, pagedCfg())

	got, err := st.GetRules()
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, 1, m.calls, "an empty LastEvaluatedKey means done, not another page")
}
