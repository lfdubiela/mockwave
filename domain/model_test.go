package domain

import "testing"

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
