package server

import (
	"testing"

	"github.com/mockwave/mockwave/domain"
)

func TestResolveEventConfigDefaults(t *testing.T) {
	c := resolveEventConfig(EventConfig{Enabled: true})
	if c.BufferSize != 10000 || c.ttlSeconds() != 3600 {
		t.Fatalf("defaults = buffer %d ttl %ds", c.BufferSize, c.ttlSeconds())
	}
}

func TestEventQuery(t *testing.T) {
	ev := domain.Event{
		Source:     "billing",
		DetailType: "InvoicePaid",
		Subject:    "subj",
		Attributes: map[string]string{"env": "prod"},
	}
	q := eventQuery(ev)
	if q["source"] != "billing" || q["detail_type"] != "InvoicePaid" || q["subject"] != "subj" || q["attr.env"] != "prod" {
		t.Fatalf("query = %v", q)
	}
}
