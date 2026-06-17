package domain

import (
	"encoding/json"
	"testing"
)

func TestEventRuleValidate(t *testing.T) {
	cases := []struct {
		name    string
		rule    EventRule
		wantErr bool
	}{
		{"ok sns", EventRule{ID: "r1", Match: EventMatch{Service: EventServiceSNS}}, false},
		{"ok sqs", EventRule{ID: "r2", Match: EventMatch{Service: EventServiceSQS}}, false},
		{"ok eventbridge", EventRule{ID: "r3", Match: EventMatch{Service: EventServiceEventBridge}}, false},
		{"missing id", EventRule{Match: EventMatch{Service: EventServiceSNS}}, true},
		{"bad service", EventRule{ID: "r4", Match: EventMatch{Service: "kinesis"}}, true},
		{"negative delay", EventRule{ID: "r5", Match: EventMatch{Service: EventServiceSNS}, Forward: &EventForward{DelayMs: -1}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.rule.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestConfigEventRulesRoundTrip(t *testing.T) {
	in := Config{EventRules: []EventRule{{
		ID:    "publish-orders",
		Name:  "Order events",
		Match: EventMatch{Service: EventServiceSNS, Target: "arn:aws:sns:*:*:orders"},
	}}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Config
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.EventRules) != 1 || out.EventRules[0].ID != "publish-orders" {
		t.Fatalf("round-trip lost event rules: %+v", out.EventRules)
	}
}
