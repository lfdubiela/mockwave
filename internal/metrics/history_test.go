package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHistoryBuffer_RecordAndSnapshot(t *testing.T) {
	c := NewCollector()
	now := time.Now().Truncate(time.Minute)

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
	t0 := time.Now().Truncate(time.Minute)
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
	base := time.Now().Truncate(time.Minute)
	for i := 0; i < 35; i++ {
		c.recordHistory(base.Add(time.Duration(i) * time.Minute))
	}
	buckets := c.History()
	assert.LessOrEqual(t, len(buckets), 30)
	// The oldest remaining should be minute 5 (0-indexed).
	assert.Equal(t, base.Add(5*time.Minute), buckets[0].Ts)
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
