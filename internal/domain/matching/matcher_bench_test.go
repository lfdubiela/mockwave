package matching

import (
	"context"
	"fmt"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

// benchRules builds n rules shaped like a realistic config: a mix of exact and
// wildcard paths across a few resources, each with a header constraint.
func benchRules(n int) []domain.Rule {
	rules := make([]domain.Rule, n)
	for i := 0; i < n; i++ {
		p := fmt.Sprintf("/api/v1/resource%d/item%d", i, i)
		if i%3 == 0 {
			p = fmt.Sprintf("/api/v1/resource%d/*", i)
		}
		rules[i] = domain.Rule{
			ID:   fmt.Sprintf("rule-%d", i),
			Name: fmt.Sprintf("rule %d", i),
			Match: domain.MatchCriteria{
				Protocol: "http",
				Method:   "GET",
				Path:     p,
				Headers:  map[string]string{"x-tenant": fmt.Sprintf("t%d", i)},
			},
			Buckets: []domain.WeightedBucket{{Weight: 100}},
		}
	}
	return rules
}

// worstCaseReq matches nothing, forcing a full scan of every rule.
func worstCaseReq() pipeline.NormalizedRequest {
	return pipeline.NormalizedRequest{
		Protocol: "http",
		Method:   "GET",
		Path:     "/api/v1/does-not-exist/nope",
		Headers:  map[string]string{"x-tenant": "absent"},
	}
}

// BenchmarkMatch_FullScan is the pessimistic per-request cost: no rule matches,
// so every rule is evaluated. Real traffic that hits a rule early is cheaper.
func BenchmarkMatch_FullScan(b *testing.B) {
	for _, n := range []int{10, 50, 500, 5000} {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			stage := NewConditionMatchStage(benchRules(n))
			req := worstCaseReq()
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pctx := &pipeline.PipelineContext{Request: req}
				_ = stage.Execute(ctx, pctx)
			}
		})
	}
}

// BenchmarkMatch_FirstHit is the optimistic case: the request matches a rule
// near the front of the specificity-sorted list.
func BenchmarkMatch_FirstHit(b *testing.B) {
	for _, n := range []int{10, 50, 500, 5000} {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			rules := benchRules(n)
			stage := NewConditionMatchStage(rules)
			target := rules[1] // exact-path rule, ranks high after sorting
			req := pipeline.NormalizedRequest{
				Protocol: "http",
				Method:   "GET",
				Path:     target.Match.Path,
				Headers:  map[string]string{"x-tenant": target.Match.Headers["x-tenant"]},
			}
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pctx := &pipeline.PipelineContext{Request: req}
				_ = stage.Execute(ctx, pctx)
			}
		})
	}
}

// BenchmarkNewConditionMatchStage measures rebuild cost (insertion sort is
// O(n^2)); this runs on every rule change, not per request.
func BenchmarkNewConditionMatchStage(b *testing.B) {
	for _, n := range []int{10, 50, 500, 5000} {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			rules := benchRules(n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = NewConditionMatchStage(rules)
			}
		})
	}
}
