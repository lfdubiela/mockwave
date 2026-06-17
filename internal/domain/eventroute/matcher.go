// Package eventroute matches normalized outgoing events against event rules.
// First match wins, rules ordered most-specific first. Pure domain logic.
package eventroute

import (
	"encoding/json"
	"path"
	"strings"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/jsonpath"
)

// Matcher resolves the first matching event rule for an event.
type Matcher struct {
	rules []domain.EventRule
}

// NewMatcher drops disabled rules and sorts the rest most-specific first.
func NewMatcher(rules []domain.EventRule) *Matcher {
	active := make([]domain.EventRule, 0, len(rules))
	for _, r := range rules {
		if !r.Disabled {
			active = append(active, r)
		}
	}
	// Stable insertion sort by descending specificity (small rule sets).
	for i := 1; i < len(active); i++ {
		for j := i; j > 0 && specificity(active[j]) > specificity(active[j-1]); j-- {
			active[j], active[j-1] = active[j-1], active[j]
		}
	}
	return &Matcher{rules: active}
}

// Match returns the first matching rule, or nil.
func (m *Matcher) Match(ev domain.Event) *domain.EventRule {
	for i := range m.rules {
		if matchEvent(&m.rules[i], ev) {
			return &m.rules[i]
		}
	}
	return nil
}

// specificity counts the constraints a rule imposes; higher = checked first.
func specificity(r domain.EventRule) int {
	mc := r.Match
	n := 0
	for _, s := range []string{mc.Operation, mc.Target, mc.Source, mc.DetailType} {
		if s != "" {
			n++
		}
	}
	n += len(mc.Attributes) + len(mc.Message)
	return n
}

func matchEvent(r *domain.EventRule, ev domain.Event) bool {
	mc := r.Match
	if mc.Service != "" && mc.Service != ev.Service {
		return false
	}
	if mc.Operation != "" && !strings.EqualFold(mc.Operation, ev.Operation) {
		return false
	}
	if !globOK(mc.Target, ev.Target) || !globOK(mc.Source, ev.Source) || !globOK(mc.DetailType, ev.DetailType) {
		return false
	}
	for k, v := range mc.Attributes {
		if ev.Attributes[k] != v {
			return false
		}
	}
	if len(mc.Message) > 0 {
		var parsed interface{}
		if err := json.Unmarshal(ev.Message, &parsed); err != nil {
			return false
		}
		for expr, want := range mc.Message {
			leaf, ok := jsonpath.Resolve(parsed, expr)
			if !ok || jsonpath.LeafToString(leaf) != want {
				return false
			}
		}
	}
	return true
}

// globOK reports whether pattern (empty = wildcard) matches value.
func globOK(pattern, value string) bool {
	if pattern == "" {
		return true
	}
	ok, err := path.Match(pattern, value)
	return err == nil && ok
}
