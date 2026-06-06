package metrics_test

import (
	"testing"

	"github.com/mockwave/mockwave/internal/metrics"
	"github.com/stretchr/testify/assert"
)

func TestCollector_RecordHit(t *testing.T) {
	c := metrics.NewCollector()
	c.RecordHit("r1", "My Rule", 10.5)
	c.RecordHit("r1", "My Rule", 20.5)
	snap := c.Snapshot()
	assert.Equal(t, int64(2), snap.TotalRequests)
	assert.Equal(t, int64(0), snap.Misses)
	assert.Len(t, snap.Rules, 1)
	assert.Equal(t, "r1", snap.Rules[0].RuleID)
	assert.Equal(t, int64(2), snap.Rules[0].Hits)
	assert.InDelta(t, 10.5, snap.Rules[0].P50Ms, 0.1)
	assert.InDelta(t, 20.5, snap.Rules[0].P95Ms, 0.1)
}

func TestCollector_RecordMiss(t *testing.T) {
	c := metrics.NewCollector()
	c.RecordMiss()
	c.RecordMiss()
	snap := c.Snapshot()
	assert.Equal(t, int64(2), snap.TotalRequests)
	assert.Equal(t, int64(2), snap.Misses)
	assert.Empty(t, snap.Rules)
}

func TestCollector_MultipleRules(t *testing.T) {
	c := metrics.NewCollector()
	c.RecordHit("r1", "Rule One", 5.0)
	c.RecordHit("r2", "Rule Two", 15.0)
	c.RecordHit("r2", "Rule Two", 25.0)
	snap := c.Snapshot()
	assert.Equal(t, int64(3), snap.TotalRequests)
	assert.Len(t, snap.Rules, 2)
	// Sorted by hits descending: r2 first
	assert.Equal(t, "r2", snap.Rules[0].RuleID)
	assert.Equal(t, int64(2), snap.Rules[0].Hits)
}

func TestCollector_Percentile_SingleSample(t *testing.T) {
	c := metrics.NewCollector()
	c.RecordHit("r1", "Rule One", 42.0)
	snap := c.Snapshot()
	assert.InDelta(t, 42.0, snap.Rules[0].P50Ms, 0.1)
	assert.InDelta(t, 42.0, snap.Rules[0].P95Ms, 0.1)
}

func TestCollector_RuleHistory_PerRuleAndRanking(t *testing.T) {
	c := metrics.NewCollector()
	// r1: 3 hits, r2: 1 hit, miss: 5 (must not appear as a rule)
	c.RecordHit("r1", "Rule One", 1)
	c.RecordHit("r1", "Rule One", 1)
	c.RecordHit("r1", "Rule One", 1)
	c.RecordHit("r2", "Rule Two", 1)
	for i := 0; i < 5; i++ {
		c.RecordMiss()
	}

	series := c.RuleHistory(0) // all rules
	if len(series) != 2 {
		t.Fatalf("got %d rule series, want 2", len(series))
	}
	// Ranked by window hits desc: r1 first.
	if series[0].RuleID != "r1" || series[0].RuleName != "Rule One" {
		t.Fatalf("series[0] = %+v, want r1/Rule One", series[0])
	}
	if series[1].RuleID != "r2" {
		t.Fatalf("series[1] = %+v, want r2", series[1])
	}
	var r1Hits int64
	for _, b := range series[0].Buckets {
		r1Hits += b.Count
	}
	if r1Hits != 3 {
		t.Fatalf("r1 bucket total = %d, want 3", r1Hits)
	}
}

func TestCollector_RuleHistory_TopN(t *testing.T) {
	c := metrics.NewCollector()
	c.RecordHit("a", "A", 1)
	c.RecordHit("a", "A", 1)
	c.RecordHit("b", "B", 1)
	c.RecordHit("c", "C", 1) // single hit, lowest

	series := c.RuleHistory(2)
	if len(series) != 2 {
		t.Fatalf("topN=2 returned %d series", len(series))
	}
	if series[0].RuleID != "a" {
		t.Fatalf("top series = %s, want a", series[0].RuleID)
	}
}

func TestCollector_RuleHistory_Empty(t *testing.T) {
	c := metrics.NewCollector()
	if got := c.RuleHistory(10); len(got) != 0 {
		t.Fatalf("empty collector RuleHistory len = %d, want 0", len(got))
	}
}

func TestCollector_Snapshot_CurrentTPS(t *testing.T) {
	c := metrics.NewCollector()
	c.RecordHit("r1", "Rule One", 1)

	snap := c.Snapshot()
	if len(snap.Rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(snap.Rules))
	}
	// Only the in-progress minute has data, so no completed minute -> 0 tps.
	if snap.Rules[0].CurrentTPS != 0 {
		t.Fatalf("CurrentTPS = %v, want 0 (only in-progress minute)", snap.Rules[0].CurrentTPS)
	}
}
