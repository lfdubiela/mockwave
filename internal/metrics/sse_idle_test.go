package metrics

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// countingSnapshotter records how often the broadcast loop asked for a
// snapshot.
type countingSnapshotter struct{ calls atomic.Int64 }

func (c *countingSnapshotter) Snapshot() Snapshot {
	c.calls.Add(1)
	return Snapshot{}
}

func newTestBroker(s snapshotter) *Broker {
	return &Broker{collector: s, clients: make(map[chan string]struct{})}
}

// TestBroker_SkipsSnapshotWhenNoClients pins that an idle server does no
// snapshot work.
//
// Snapshot sorts each rule's retained latencies while holding the mutex that
// RecordHit needs, so running it on a timer stalls live requests. With no SSE
// client connected there is nobody to receive the result, making that stall
// pure waste -- and it is the common case, since the dashboard is usually
// closed.
func TestBroker_SkipsSnapshotWhenNoClients(t *testing.T) {
	fake := &countingSnapshotter{}
	b := newTestBroker(fake)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Start(ctx)

	time.Sleep(2200 * time.Millisecond) // ~2 ticks of the 1s broadcast loop

	if got := fake.calls.Load(); got != 0 {
		t.Fatalf("Snapshot called %d times with no clients connected, want 0", got)
	}
}

// TestBroker_SnapshotsWhenClientConnected is the other half of the guard: the
// skip must not disable broadcasting for real subscribers.
func TestBroker_SnapshotsWhenClientConnected(t *testing.T) {
	fake := &countingSnapshotter{}
	b := newTestBroker(fake)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Start(ctx)

	_, unsub := b.Subscribe()
	defer unsub()

	time.Sleep(2200 * time.Millisecond)

	if got := fake.calls.Load(); got == 0 {
		t.Fatal("Snapshot never called while a client was subscribed")
	}
}
