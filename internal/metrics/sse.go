package metrics

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// snapshotter is the part of Collector the Broker depends on. Narrowing it
// keeps the broadcast loop testable without a live collector.
type snapshotter interface {
	Snapshot() Snapshot
}

// Broker fans out Snapshot JSON to all connected SSE clients every second.
// All methods are safe for concurrent use.
type Broker struct {
	collector snapshotter
	mu        sync.Mutex
	clients   map[chan string]struct{}
}

// NewBroker creates a Broker that reads from c.
func NewBroker(c *Collector) *Broker {
	return &Broker{
		collector: c,
		clients:   make(map[chan string]struct{}),
	}
}

// Start broadcasts snapshots every second until ctx is cancelled.
// Call in a goroutine: go broker.Start(ctx).
func (b *Broker) Start(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// Nobody is listening: skip the tick entirely. Snapshot holds the
			// collector mutex that RecordHit needs, so taking one with no
			// subscribers stalls live requests to produce a result that is
			// then discarded. An idle dashboard is the common case.
			b.mu.Lock()
			idle := len(b.clients) == 0
			b.mu.Unlock()
			if idle {
				continue
			}

			snap := b.collector.Snapshot()
			data, _ := json.Marshal(snap)
			b.mu.Lock()
			for ch := range b.clients {
				select {
				case ch <- string(data):
				default: // slow client — skip this tick
				}
			}
			b.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

// Subscribe returns a buffered channel that receives JSON snapshots and a
// cancel function that must be called when the client disconnects.
func (b *Broker) Subscribe() (chan string, func()) {
	ch := make(chan string, 4)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.clients, ch)
		close(ch)
		b.mu.Unlock()
	}
}
