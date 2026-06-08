package matching

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/mockwave/mockwave/domain"
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
	if len(m.Body) > 0 {
		var parsed interface{}
		if err := json.Unmarshal(req.Body, &parsed); err != nil {
			return false
		}
		for pathExpr, want := range m.Body {
			leaf, ok := resolvePath(parsed, pathExpr)
			if !ok || leafToString(leaf) != want {
				return false
			}
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

// resolvePath walks a dotted JSON path (optionally prefixed with "$." or "$")
// and returns the leaf value. Numeric segments index into arrays. The second
// return is false if the path does not resolve to a scalar leaf.
func resolvePath(root interface{}, expr string) (interface{}, bool) {
	expr = strings.TrimPrefix(expr, "$")
	expr = strings.TrimPrefix(expr, ".")
	if expr == "" {
		return nil, false
	}
	cur := root
	for _, seg := range strings.Split(expr, ".") {
		switch node := cur.(type) {
		case map[string]interface{}:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []interface{}:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	switch cur.(type) {
	case map[string]interface{}, []interface{}:
		return nil, false
	}
	return cur, true
}

// leafToString stringifies a scalar JSON value for exact comparison. JSON
// numbers decode to float64; render integers without a trailing ".0".
func leafToString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}
