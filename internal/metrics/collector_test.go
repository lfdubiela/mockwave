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
