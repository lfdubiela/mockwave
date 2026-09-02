package dynamostore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	dynamostore "github.com/mockwave/mockwave/internal/adapters/out/dynamodb"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setMatchedPages seeds n single-item pages of matched-request rows.
func (m *pagingDynamo) setMatchedPages(table string, n int) {
	var pages [][]map[string]types.AttributeValue
	for i := 0; i < n; i++ {
		r := matched.Request{
			ID: fmt.Sprintf("req-%04d", i), RuleID: "rule-1",
			At: time.Unix(int64(1700000000+i), 0), Protocol: "http",
		}
		data, _ := json.Marshal(r)
		pages = append(pages, []map[string]types.AttributeValue{{
			"rule_id": &types.AttributeValueMemberS{Value: "rule-1"},
			"sk":      &types.AttributeValueMemberS{Value: "r#" + r.ID},
			"id":      &types.AttributeValueMemberS{Value: r.ID},
			"data":    &types.AttributeValueMemberS{Value: string(data)},
		}})
	}
	m.pages[table] = pages
}

// TestListMatched_ScanStopsOnceLimitReached pins that the hydration path does
// bounded work.
//
// The scan read the whole table into memory and only then applied the limit,
// so asking for a handful of entries against a large table walked every row.
// In production that table reached 1.18M rows and the scan hung pod startup.
func TestListMatched_ScanStopsOnceLimitReached(t *testing.T) {
	m := newPagingDynamo()
	m.setMatchedPages("matched", 500) // 500 pages, one row each
	st := dynamostore.NewStoreFromClient(m, dynamostore.Config{
		RulesTable: "rules", MatchedTable: "matched",
	})

	page, err := st.ListMatched(context.Background(), "", store.MatchedQuery{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, page.Items, 10, "should still return a full page")
	assert.LessOrEqual(t, m.scanCalls["matched"], 20,
		"scan must stop once the page is full, not walk all 500 pages")
}
