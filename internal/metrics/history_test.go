package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoryBuffer_RecordAndSnapshot(t *testing.T) {
	c := NewCollector()
	now := time.Now().UTC().Truncate(time.Minute)

	c.recordHistory(now)
	c.recordHistory(now)
	c.recordHistory(now)

	buckets := c.History()
	require.Len(t, buckets, 1)
	assert.Equal(t, now, buckets[0].Ts)
	assert.Equal(t, int64(3), buckets[0].Count)
}

func TestHistoryBuffer_MultipleMinutes(t *testing.T) {
	c := NewCollector()
	t0 := time.Now().UTC().Truncate(time.Minute)
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)

	c.recordHistory(t0)
	c.recordHistory(t0)
	c.recordHistory(t1)
	c.recordHistory(t2)
	c.recordHistory(t2)

	buckets := c.History()
	require.Len(t, buckets, 3)
	assert.Equal(t, t0, buckets[0].Ts)
	assert.Equal(t, int64(2), buckets[0].Count)
	assert.Equal(t, t1, buckets[1].Ts)
	assert.Equal(t, int64(1), buckets[1].Count)
	assert.Equal(t, t2, buckets[2].Ts)
	assert.Equal(t, int64(2), buckets[2].Count)
}

func TestHistoryBuffer_MaxThirtySlots(t *testing.T) {
	c := NewCollector()
	base := time.Now().UTC().Truncate(time.Minute)
	for i := 0; i < 35; i++ {
		c.recordHistory(base.Add(time.Duration(i) * time.Minute))
	}
	buckets := c.History()
	assert.Len(t, buckets, 30)
	// The oldest remaining should be minute 5 (0-indexed).
	assert.Equal(t, base.Add(5*time.Minute), buckets[0].Ts)
	// Also verify newest bucket is the last one inserted
	assert.Equal(t, base.Add(34*time.Minute), buckets[len(buckets)-1].Ts)
}

func TestHistoryBuffer_EmptySnapshot(t *testing.T) {
	c := NewCollector()
	assert.Empty(t, c.History())
}

func TestHistoryBuffer_RecordHitUpdatesHistory(t *testing.T) {
	c := NewCollector()
	c.RecordHit("r1", "Rule 1", 5.0)
	assert.Len(t, c.History(), 1)
	assert.Equal(t, int64(1), c.History()[0].Count)
}

func TestHistoryBuffer_RecordMissUpdatesHistory(t *testing.T) {
	c := NewCollector()
	c.RecordMiss()
	assert.Len(t, c.History(), 1)
	assert.Equal(t, int64(1), c.History()[0].Count)
}

func TestHistRing_WindowHits(t *testing.T) {
	var h histRing
	base := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	h.record(base)                       // minute 10:00 -> 1
	h.record(base.Add(10 * time.Second)) // minute 10:00 -> 2
	h.record(base.Add(time.Minute))      // minute 10:01 -> 1
	if got := h.windowHits(); got != 3 {
		t.Fatalf("windowHits = %d, want 3", got)
	}
}

func TestHistRing_LastCompletedCount(t *testing.T) {
	var h histRing
	base := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	h.record(base)                  // 10:00 -> 1
	h.record(base)                  // 10:00 -> 2
	h.record(base.Add(time.Minute)) // 10:01 -> 1 (in-progress when now is 10:01:30)

	now := base.Add(time.Minute + 30*time.Second) // 10:01:30 -> last completed minute is 10:00
	if got := h.lastCompletedCount(now); got != 2 {
		t.Fatalf("lastCompletedCount = %d, want 2", got)
	}

	// When only the in-progress minute exists, there is no completed minute.
	var h2 histRing
	h2.record(now) // 10:01
	if got := h2.lastCompletedCount(now); got != 0 {
		t.Fatalf("lastCompletedCount (only current) = %d, want 0", got)
	}
}
