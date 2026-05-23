package metrics_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroker_SubscribeReceivesSnapshot(t *testing.T) {
	col := metrics.NewCollector()
	col.RecordHit("r1", "Rule One", 5.0)

	broker := metrics.NewBroker(col)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go broker.Start(ctx)

	ch, unsub := broker.Subscribe()
	defer unsub()

	select {
	case data := <-ch:
		var snap metrics.Snapshot
		require.NoError(t, json.Unmarshal([]byte(data), &snap))
		assert.Equal(t, int64(1), snap.TotalRequests)
		assert.Len(t, snap.Rules, 1)
	case <-ctx.Done():
		t.Fatal("timeout waiting for SSE snapshot")
	}
}

func TestBroker_UnsubscribeStopsDelivery(t *testing.T) {
	col := metrics.NewCollector()
	broker := metrics.NewBroker(col)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go broker.Start(ctx)

	ch, unsub := broker.Subscribe()
	unsub() // immediately unsubscribe

	// Drain the channel if one event slipped in, then confirm it's closed.
	select {
	case _, ok := <-ch:
		if ok {
			// One event may have arrived before unsub; that's fine.
			// Now the channel must be closed.
			_, ok = <-ch
			assert.False(t, ok, "channel should be closed after unsubscribe")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel not closed after unsubscribe")
	}
}
