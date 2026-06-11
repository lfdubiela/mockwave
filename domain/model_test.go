package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMatchCriteria_Equal(t *testing.T) {
	base := MatchCriteria{
		Protocol: "http", Method: "GET", Path: "/users/*",
		Headers: map[string]string{"X-A": "1"},
		Query:   map[string]string{"q": "x"},
		Body:    map[string]string{"$.id": "5"},
	}
	t.Run("equal", func(t *testing.T) {
		other := base
		other.Headers = map[string]string{"X-A": "1"}
		if !base.Equal(other) {
			t.Fatal("expected equal")
		}
	})
	t.Run("nil vs empty map equal", func(t *testing.T) {
		a := MatchCriteria{Path: "/x"}
		b := MatchCriteria{Path: "/x", Headers: map[string]string{}, Query: map[string]string{}, Body: map[string]string{}}
		if !a.Equal(b) {
			t.Fatal("nil and empty maps should compare equal")
		}
	})
	diffs := map[string]func(*MatchCriteria){
		"protocol": func(m *MatchCriteria) { m.Protocol = "grpc" },
		"method":   func(m *MatchCriteria) { m.Method = "POST" },
		"path":     func(m *MatchCriteria) { m.Path = "/other" },
		"header":   func(m *MatchCriteria) { m.Headers = map[string]string{"X-A": "2"} },
		"query":    func(m *MatchCriteria) { m.Query = map[string]string{"q": "y"} },
		"body":     func(m *MatchCriteria) { m.Body = map[string]string{"$.id": "6"} },
	}
	for name, mut := range diffs {
		t.Run("differs by "+name, func(t *testing.T) {
			other := base
			mut(&other)
			if base.Equal(other) {
				t.Fatalf("expected not equal when %s differs", name)
			}
		})
	}
}

func TestWeightedBucket_NegativeDelayRejected(t *testing.T) {
	b := WeightedBucket{Weight: 100, Action: ActionForward, DelayMs: -1}
	if err := b.Validate(); err == nil {
		t.Fatal("expected error for negative delay_ms, got nil")
	}
}

func TestWeightedBucket_ForwardWithDelayValid(t *testing.T) {
	b := WeightedBucket{Weight: 100, Action: ActionForward, DelayMs: 2000, ForwardURL: "https://upstream.example.com"}
	if err := b.Validate(); err != nil {
		t.Fatalf("expected forward bucket with delay to be valid, got %v", err)
	}
}

func TestFaultProfileValidate(t *testing.T) {
	valid := FaultProfile{
		ID:      "flaky-db",
		Name:    "Flaky database",
		Enabled: true,
		Faults: []Fault{
			{Type: FaultJitter, Probability: 1.0, Params: FaultParams{BaseDelayMs: 200, JitterMs: 300}},
			{Type: FaultError, Probability: 0.3, Params: FaultParams{StatusCode: 503, Body: `{"error":"unavailable"}`}},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid profile: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*FaultProfile)
	}{
		{"missing id", func(p *FaultProfile) { p.ID = "" }},
		{"no faults", func(p *FaultProfile) { p.Faults = nil }},
		{"unknown type", func(p *FaultProfile) { p.Faults[0].Type = "explode" }},
		{"probability below 0", func(p *FaultProfile) { p.Faults[0].Probability = -0.1 }},
		{"probability above 1", func(p *FaultProfile) { p.Faults[0].Probability = 1.1 }},
		{"jitter without delay params", func(p *FaultProfile) { p.Faults[0].Params.JitterMs = 0; p.Faults[0].Params.BaseDelayMs = 0 }},
		{"negative base delay", func(p *FaultProfile) { p.Faults[0].Params.BaseDelayMs = -100 }},
		{"negative jitter", func(p *FaultProfile) { p.Faults[0].Params.BaseDelayMs = 200; p.Faults[0].Params.JitterMs = -50 }},
		{"error without status", func(p *FaultProfile) { p.Faults[1].Params.StatusCode = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			p.Faults = append([]Fault(nil), valid.Faults...)
			tc.mut(&p)
			if err := p.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestConnectionFaultValidate(t *testing.T) {
	cases := []struct {
		name  string
		fault Fault
		ok    bool
	}{
		{"hang valid", Fault{Type: FaultHang, Probability: 1, Params: FaultParams{MaxMs: 5000}}, true},
		{"hang missing max_ms", Fault{Type: FaultHang, Probability: 1}, false},
		{"hang negative max_ms", Fault{Type: FaultHang, Probability: 1, Params: FaultParams{MaxMs: -1}}, false},
		{"reset valid", Fault{Type: FaultReset, Probability: 1}, true},
		{"halfResponse valid", Fault{Type: FaultHalfResponse, Probability: 1, Params: FaultParams{Fraction: 0.5}}, true},
		{"halfResponse fraction 0", Fault{Type: FaultHalfResponse, Probability: 1, Params: FaultParams{Fraction: 0}}, false},
		{"halfResponse fraction >1", Fault{Type: FaultHalfResponse, Probability: 1, Params: FaultParams{Fraction: 1.5}}, false},
		{"slowBody valid", Fault{Type: FaultSlowBody, Probability: 1, Params: FaultParams{BytesPerSec: 1024}}, true},
		{"slowBody zero rate", Fault{Type: FaultSlowBody, Probability: 1, Params: FaultParams{BytesPerSec: 0}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fault.Validate()
			if tc.ok && err != nil {
				t.Fatalf("expected valid, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestBucketFaultProfileIDRoundTrip(t *testing.T) {
	b := WeightedBucket{Weight: 100, Action: ActionSimulate, SimulationID: "s1", FaultProfileID: "flaky-db"}
	if err := b.Validate(); err != nil {
		t.Fatalf("bucket with fault profile id should be valid: %v", err)
	}
	data, _ := json.Marshal(b)
	if !strings.Contains(string(data), `"fault_profile_id":"flaky-db"`) {
		t.Fatalf("missing fault_profile_id in %s", data)
	}
	b2 := WeightedBucket{Weight: 100, Action: ActionSimulate, SimulationID: "s1"}
	data2, _ := json.Marshal(b2)
	if strings.Contains(string(data2), "fault_profile_id") {
		t.Fatalf("fault_profile_id should be omitted: %s", data2)
	}
}
