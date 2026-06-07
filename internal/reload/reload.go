// Package reload polls a VersionedStore and triggers a reload when the store's
// config version changes, keeping in-memory snapshots fresh across pods.
package reload

import (
	"context"
	"time"

	"github.com/mockwave/mockwave/observability"
	"github.com/mockwave/mockwave/store"
)

// Reloader periodically checks a store's config version and invokes reload when
// it changes (and once on the first tick).
type Reloader struct {
	store    store.VersionedStore
	interval time.Duration
	reload   func() error
	log      observability.Logger
}

// New creates a Reloader. reload is typically server.Rebuild.
func New(s store.VersionedStore, interval time.Duration, reload func() error, log observability.Logger) *Reloader {
	return &Reloader{store: s, interval: interval, reload: reload, log: log}
}

// Run blocks until ctx is cancelled, polling every interval. The first tick
// always reloads; subsequent ticks reload only when the version changed.
func (r *Reloader) Run(ctx context.Context) {
	last := int64(-1)
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			v, err := r.store.ConfigVersion()
			if err != nil {
				r.log.Warn(ctx, "reload: config version read failed")
				continue
			}
			if v == last {
				continue
			}
			if err := r.reload(); err != nil {
				r.log.Error(ctx, "reload: rebuild failed", err)
				continue // do not advance last; retry next tick
			}
			last = v
		}
	}
}
