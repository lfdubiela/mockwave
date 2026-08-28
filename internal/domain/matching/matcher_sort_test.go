package matching_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/matching"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interleavedRules builds n rules alternating between two specificity tiers:
// odd indexes carry a header (more specific), even indexes do not. Every rule
// matches headerReq, so ordering alone decides which one wins.
//
// The interleaving matters. An unstable sort leaves an all-equal slice
// untouched (pdqsort short-circuits), so a uniform slice cannot detect lost
// stability; two interleaved tiers force real partitioning and expose it.
func interleavedRules(n int) []domain.Rule {
	rules := make([]domain.Rule, n)
	for i := range rules {
		m := domain.MatchCriteria{Protocol: "http", Path: "/orders"}
		if i%2 == 1 {
			m.Headers = map[string]string{"x-cid": "1"}
		}
		rules[i] = mkMatchRule(fmt.Sprintf("rule-%02d", i), m)
	}
	return rules
}

// expectedOrder returns the IDs of interleavedRules(n) in the order the sort
// must produce: header rules first, then the rest, each group keeping input
// order.
func expectedOrder(n int) []string {
	var ids []string
	for i := 1; i < n; i += 2 {
		ids = append(ids, fmt.Sprintf("rule-%02d", i))
	}
	for i := 0; i < n; i += 2 {
		ids = append(ids, fmt.Sprintf("rule-%02d", i))
	}
	return ids
}

func headerReq() *pipeline.PipelineContext {
	return &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{
		Protocol: "http", Method: "GET", Path: "/orders",
		Headers: map[string]string{"x-cid": "1"},
	}}
}

// TestPrecedence_FullOrderIsStable pins the position of every rule, not just
// the winner. Disabling the first k rules in expected order makes rule k the
// only possible match, so iterating k sweeps the entire ordering.
//
// TestPrecedence_StableWhenEqual uses two rules, which no sort permutes; this
// test is what actually guards stability.
func TestPrecedence_FullOrderIsStable(t *testing.T) {
	const n = 32
	want := expectedOrder(n)

	for k := range want {
		rules := interleavedRules(n)
		disabled := map[string]bool{}
		for _, id := range want[:k] {
			disabled[id] = true
		}
		for i := range rules {
			if disabled[rules[i].ID] {
				rules[i].Disabled = true
			}
		}

		stage := matching.NewConditionMatchStage(rules)
		pctx := headerReq()
		require.NoError(t, stage.Execute(context.Background(), pctx))
		assert.Equal(t, want[k], pctx.Matched.ID,
			"with the first %d rules of the expected order disabled, %s must win", k, want[k])
	}
}

// TestPrecedence_TieredOrderHoldsAtScale checks the tiered tuple still ranks
// correctly when the most-specific rule sits at the end of a large slice.
func TestPrecedence_TieredOrderHoldsAtScale(t *testing.T) {
	rules := interleavedRules(64)
	rules = append(rules, mkMatchRule("winner", domain.MatchCriteria{
		Protocol: "http", Path: "/orders",
		Headers: map[string]string{"x-cid": "1"},
		Query:   map[string]string{"a": "1"},
	}))

	stage := matching.NewConditionMatchStage(rules)
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{
		Protocol: "http", Method: "GET", Path: "/orders",
		Headers: map[string]string{"x-cid": "1"},
		Query:   map[string]string{"a": "1"},
	}}
	require.NoError(t, stage.Execute(context.Background(), pctx))
	assert.Equal(t, "winner", pctx.Matched.ID)
}
