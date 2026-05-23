package matching

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/mockwave/mockwave/internal/domain"
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
	return true
}

func sortBySpecificity(rules []domain.Rule) {
	for i := 1; i < len(rules); i++ {
		for j := i; j > 0; j-- {
			if specificity(rules[j]) > specificity(rules[j-1]) {
				rules[j], rules[j-1] = rules[j-1], rules[j]
			}
		}
	}
}

func specificity(r domain.Rule) int {
	score := len(r.Match.Headers)*10 + len(r.Match.Query)*5
	if !strings.Contains(r.Match.Path, "*") {
		score += 3
	}
	return score
}
