package eventroute

import (
	"testing"

	"github.com/mockwave/mockwave/domain"
)

func ev() domain.Event {
	return domain.Event{
		Service:    domain.EventServiceSNS,
		Operation:  "Publish",
		Target:     "arn:aws:sns:us-east-1:123:orders",
		Message:    []byte(`{"type":"created","total":42}`),
		Attributes: map[string]string{"env": "prod"},
	}
}

func TestMatch(t *testing.T) {
	rules := []domain.EventRule{
		{ID: "any-sns", Match: domain.EventMatch{Service: domain.EventServiceSNS}},
		{ID: "orders", Match: domain.EventMatch{Service: domain.EventServiceSNS, Target: "arn:aws:sns:*:*:orders"}},
		{ID: "orders-created", Match: domain.EventMatch{
			Service: domain.EventServiceSNS, Target: "arn:aws:sns:*:*:orders",
			Message: map[string]string{"$.type": "created"},
		}},
	}
	m := NewMatcher(rules)

	// Most specific (message + target) wins over target-only and service-only.
	if got := m.Match(ev()); got == nil || got.ID != "orders-created" {
		t.Fatalf("Match = %v, want orders-created", got)
	}

	// Attribute mismatch rejects the rule.
	e := ev()
	e.Attributes = map[string]string{"env": "dev"}
	r := []domain.EventRule{{ID: "prod-only", Match: domain.EventMatch{Service: domain.EventServiceSNS, Attributes: map[string]string{"env": "prod"}}}}
	if got := NewMatcher(r).Match(e); got != nil {
		t.Fatalf("Match = %v, want nil (attr mismatch)", got)
	}

	// Disabled rules never match.
	d := []domain.EventRule{{ID: "off", Disabled: true, Match: domain.EventMatch{Service: domain.EventServiceSNS}}}
	if got := NewMatcher(d).Match(ev()); got != nil {
		t.Fatalf("Match = %v, want nil (disabled)", got)
	}

	// Wrong service: no match.
	e2 := ev()
	e2.Service = domain.EventServiceSQS
	if got := m.Match(e2); got != nil {
		t.Fatalf("Match = %v, want nil (service mismatch)", got)
	}
}

func TestMatchRejectPaths(t *testing.T) {
	// Operation mismatch: rule requires Publish, event carries DeleteMessage.
	e := ev()
	e.Operation = "DeleteMessage"
	r := []domain.EventRule{{ID: "publish-only", Match: domain.EventMatch{Service: domain.EventServiceSNS, Operation: "Publish"}}}
	if got := NewMatcher(r).Match(e); got != nil {
		t.Fatalf("operation mismatch: Match = %v, want nil", got)
	}

	// Glob reject: rule targets payments topic, event targets orders topic.
	e2 := ev()
	r2 := []domain.EventRule{{ID: "payments", Match: domain.EventMatch{Service: domain.EventServiceSNS, Target: "arn:aws:sns:*:*:payments"}}}
	if got := NewMatcher(r2).Match(e2); got != nil {
		t.Fatalf("glob reject: Match = %v, want nil", got)
	}

	// Malformed message JSON: rule has a message filter, event body is not JSON.
	e3 := ev()
	e3.Message = []byte("not json")
	r3 := []domain.EventRule{{ID: "json-filter", Match: domain.EventMatch{Service: domain.EventServiceSNS, Message: map[string]string{"$.type": "created"}}}}
	if got := NewMatcher(r3).Match(e3); got != nil {
		t.Fatalf("malformed json: Match = %v, want nil", got)
	}
}
