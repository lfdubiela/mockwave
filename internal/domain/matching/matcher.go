package matching

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/jsonpath"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

type ConditionMatchStage struct {
	rules []domain.Rule
}

func NewConditionMatchStage(rules []domain.Rule) *ConditionMatchStage {
	sorted := make([]domain.Rule, len(rules))
	copy(sorted, rules)
	sortBySpecificity(sorted)
	return &ConditionMatchStage{rules: sorted}
}

func (s *ConditionMatchStage) Name() string { return "condition-match" }

func (s *ConditionMatchStage) Execute(_ context.Context, pctx *pipeline.PipelineContext) error {
	req := pctx.Request
	for i := range s.rules {
		if matchRule(&s.rules[i], req) {
			pctx.Matched = &s.rules[i]
			return nil
		}
	}
	return fmt.Errorf("no rule matched: %s %s", req.Method, req.Path)
}

func matchRule(r *domain.Rule, req pipeline.NormalizedRequest) bool {
	if r.Disabled {
		return false
	}
	m := r.Match
	if m.Protocol != "" && m.Protocol != req.Protocol {
		return false
	}
	if m.Method != "" && !strings.EqualFold(m.Method, req.Method) {
		return false
	}
	if m.Path != "" {
		matched, err := path.Match(m.Path, req.Path)
		if err != nil || !matched {
			return false
		}
	}
	for k, v := range m.Headers {
		if req.Headers[strings.ToLower(k)] != v {
			return false
		}
	}
	for k, v := range m.Query {
		if req.Query[k] != v {
			return false
		}
	}
	if len(m.Body) > 0 {
		var parsed interface{}
		if err := json.Unmarshal(req.Body, &parsed); err != nil {
			return false
		}
		for pathExpr, want := range m.Body {
			leaf, ok := jsonpath.Resolve(parsed, pathExpr)
			if !ok || jsonpath.LeafToString(leaf) != want {
				return false
			}
		}
	}
	return true
}

// sortBySpecificity orders rules most-specific first using a strict tiered
// (lexicographic) comparison. Stable: equal rules keep input order.
func sortBySpecificity(rules []domain.Rule) {
	for i := 1; i < len(rules); i++ {
		for j := i; j > 0; j-- {
			if moreSpecific(rules[j], rules[j-1]) {
				rules[j], rules[j-1] = rules[j-1], rules[j]
			} else {
				break
			}
		}
	}
}

// moreSpecific reports whether a ranks strictly above b under the tiered order
// (headers, url, body, query) compared element-wise.
func moreSpecific(a, b domain.Rule) bool {
	ta, tb := specTuple(a), specTuple(b)
	for i := range ta {
		if ta[i] != tb[i] {
			return ta[i] > tb[i]
		}
	}
	return false
}

// specTuple builds the precedence tuple: (headers, url, body, query).
func specTuple(r domain.Rule) [4]int {
	return [4]int{
		len(r.Match.Headers),
		urlScore(r.Match.Path),
		len(r.Match.Body),
		len(r.Match.Query),
	}
}

// urlScore: exact non-empty path -> 1000; wildcard -> 100 + literal segment
// count; empty -> 0. Exact always outranks any wildcard.
func urlScore(p string) int {
	if p == "" {
		return 0
	}
	if !strings.Contains(p, "*") {
		return 1000
	}
	literal := 0
	for _, seg := range strings.Split(p, "/") {
		if seg != "" && !strings.Contains(seg, "*") {
			literal++
		}
	}
	return 100 + literal
}

