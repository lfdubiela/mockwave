package metrics

import (
	"sync"
	"time"
)

// MinuteBucket holds the total request count for one UTC minute.
type MinuteBucket struct {
	Ts    time.Time `json:"ts"`
	Count int64     `json:"count"`
}

// histRing is a 30-slot ring buffer of per-minute request counts.
type histRing struct {
	mu      sync.Mutex
	slots   [30]MinuteBucket
	current int
	filled  bool
}

func (h *histRing) record(at time.Time) {
	minute := at.UTC().Truncate(time.Minute)
	h.mu.Lock()
	defer h.mu.Unlock()
	cur := &h.slots[h.current]
	if !h.filled || !cur.Ts.Equal(minute) {
		if h.filled {
			h.current = (h.current + 1) % 30
		}
		h.slots[h.current] = MinuteBucket{Ts: minute, Count: 0}
		h.filled = true
	}
	h.slots[h.current].Count++
}

func (h *histRing) snapshot() []MinuteBucket {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.filled {
		return nil
	}
	out := make([]MinuteBucket, 0, 30)
	// Iterate from oldest slot to newest (current), skipping zero-value slots.
	// Start one past current (the next slot to be overwritten = oldest) and
	// wrap around 30 positions ending at current.
	for i := 0; i < 30; i++ {
		idx := (h.current + 1 + i) % 30
		if !h.slots[idx].Ts.IsZero() {
			out = append(out, h.slots[idx])
		}
	}
	return out
}

// windowHits returns the total count across all retained buckets.
func (h *histRing) windowHits() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	var total int64
	for i := range h.slots {
		if !h.slots[i].Ts.IsZero() {
			total += h.slots[i].Count
		}
	}
	return total
}

// lastCompletedCount returns the count of the most recent bucket strictly
// before the minute containing now (i.e. the last fully-elapsed minute), or 0
// if no such bucket exists.
func (h *histRing) lastCompletedCount(now time.Time) int64 {
	curMinute := now.UTC().Truncate(time.Minute)
	h.mu.Lock()
	defer h.mu.Unlock()
	var best MinuteBucket
	for i := range h.slots {
		b := h.slots[i]
		if b.Ts.IsZero() || !b.Ts.Before(curMinute) {
			continue
		}
		if best.Ts.IsZero() || b.Ts.After(best.Ts) {
			best = b
		}
	}
	return best.Count
}
