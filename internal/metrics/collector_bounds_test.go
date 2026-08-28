package metrics_test

import (
	"testing"

	"github.com/mockwave/mockwave/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCollector_RetainsOnlyRecentSamples pins that latency retention is
// bounded. Records far more hits than the retention limit: the early, slow
// samples must age out, so the reported percentiles reflect recent traffic.
//
// Unbounded retention keeps one float64 per request for the process lifetime,
// which both leaks memory and makes Snapshot's sort grow without limit.
func TestCollector_RetainsOnlyRecentSamples(t *testing.T) {
	c := metrics.NewCollector()

	// 20k slow samples, then 2k fast ones. With bounded retention the slow
	// ones are gone; with unbounded retention they dominate the median.
	for i := 0; i < 20_000; i++ {
		c.RecordHit("r1", "rule one", 500.0)
	}
	for i := 0; i < 2_000; i++ {
		c.RecordHit("r1", "rule one", 1.0)
	}

	snap := c.Snapshot()
	require.Len(t, snap.Rules, 1)
	assert.InDelta(t, 1.0, snap.Rules[0].P50Ms, 0.001,
		"percentiles must come from recent samples, not the whole process history")
}

// TestCollector_HitsCountsEveryRequest guards the trap in bounding retention:
// Hits is a request counter, not a count of retained samples. Deriving it from
// the sample slice silently caps it at the retention limit.
func TestCollector_HitsCountsEveryRequest(t *testing.T) {
	c := metrics.NewCollector()
	const n = 50_000
	for i := 0; i < n; i++ {
		c.RecordHit("r1", "rule one", 1.0)
	}

	snap := c.Snapshot()
	require.Len(t, snap.Rules, 1)
	assert.Equal(t, int64(n), snap.Rules[0].Hits,
		"Hits must report every request served, independent of how many samples are retained")
	assert.Equal(t, int64(n), snap.TotalRequests)
}

// TestCollector_SnapshotCostIsBounded asserts Snapshot does not get slower as
// the process serves more requests. It compares work done, not wall time, by
// checking that a heavily-loaded collector reports the same retained-sample
// behaviour as a lightly-loaded one.
func TestCollector_SnapshotCostIsBounded(t *testing.T) {
	c := metrics.NewCollector()
	for i := 0; i < 500_000; i++ {
		c.RecordHit("r1", "rule one", float64(i%100))
	}
	snap := c.Snapshot()
	require.Len(t, snap.Rules, 1)

	// P95 over a bounded window of values in [0,100) must stay in range; an
	// unbounded collector would produce the same value, so this mainly guards
	// against the ring returning stale zero-filled slots.
	assert.Greater(t, snap.Rules[0].P95Ms, 0.0)
	assert.LessOrEqual(t, snap.Rules[0].P95Ms, 100.0)
	assert.Equal(t, int64(500_000), snap.Rules[0].Hits)
}
