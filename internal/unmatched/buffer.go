package unmatched

import (
	"sync"
	"time"
)

// Request captures a request that matched no rule.
type Request struct {
	At       time.Time         `json:"at"`
	Protocol string            `json:"protocol"`
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Headers  map[string]string `json:"headers"`
	Body     string            `json:"body"`
}

// Buffer is a fixed-capacity ring buffer of unmatched requests.
// All methods are safe for concurrent use.
type Buffer struct {
	mu   sync.Mutex
	ring []Request
	cap  int
	size int
	head int // index of next write position
}

// NewBuffer creates a Buffer with the given capacity.
func NewBuffer(capacity int) *Buffer {
	return &Buffer{
		ring: make([]Request, capacity),
		cap:  capacity,
	}
}

// Add inserts a request, overwriting the oldest entry when full.
func (b *Buffer) Add(r Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ring[b.head] = r
	b.head = (b.head + 1) % b.cap
	if b.size < b.cap {
		b.size++
	}
}

// List returns all captured requests in insertion order (oldest first).
func (b *Buffer) List() []Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.size == 0 {
		return nil
	}
	out := make([]Request, b.size)
	if b.size < b.cap {
		// Buffer has not wrapped; entries are at ring[0..size-1].
		copy(out, b.ring[:b.size])
	} else {
		// Buffer has wrapped; head points to the oldest entry.
		n := copy(out, b.ring[b.head:])
		copy(out[n:], b.ring[:b.head])
	}
	return out
}

// ListDeduped returns captured requests in insertion order (oldest first) with
// duplicates collapsed: entries sharing the same Path + Body are shown once,
// keeping the most recent occurrence. Method, protocol and headers are not part
// of the dedup key.
func (b *Buffer) ListDeduped() []Request {
	all := b.List()
	if len(all) == 0 {
		return all
	}
	seen := make(map[string]bool, len(all))
	// Walk newest → oldest so the kept entry is the most recent occurrence.
	kept := make([]Request, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		key := all[i].Path + "\x00" + all[i].Body
		if seen[key] {
			continue
		}
		seen[key] = true
		kept = append(kept, all[i])
	}
	// kept is newest → oldest; reverse to restore oldest-first ordering.
	for l, r := 0, len(kept)-1; l < r; l, r = l+1, r-1 {
		kept[l], kept[r] = kept[r], kept[l]
	}
	return kept
}

// Clear removes all captured requests.
func (b *Buffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.size = 0
	b.head = 0
}
