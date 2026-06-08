package domain

import "testing"

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
