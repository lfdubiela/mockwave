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

// Clear removes all captured requests.
func (b *Buffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.size = 0
	b.head = 0
}
