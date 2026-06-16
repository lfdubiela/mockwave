package matched

import (
	"context"
	"sync"
	"time"
)

// Sink is the subset of store.MatchedStore the syncer needs. Declared here to
// avoid importing the store package (which would be a cycle).
type Sink interface {
	SaveMatched(ctx context.Context, reqs []Request, reqBodies []RequestBody, respBodies []ResponseBody) error
	SweepExpired(ctx context.Context, before int64) (int, error)
}

// Syncer periodically flushes a Buffer to a Sink (write-behind) and sweeps
// expired entries from both the buffer and the sink.
type Syncer struct {
	buf      *Buffer
	sink     Sink
	interval time.Duration
	now      func() time.Time

	closeOnce sync.Once
	done      chan struct{}
}

// NewSyncer creates a Syncer. interval <= 0 defaults to 30s.
func NewSyncer(buf *Buffer, sink Sink, interval time.Duration) *Syncer {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Syncer{buf: buf, sink: sink, interval: interval, now: time.Now, done: make(chan struct{})}
}

// Run flushes on each tick until ctx is cancelled, then performs a final flush.
func (s *Syncer) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.flush(context.Background())
			return
		case <-s.done:
			return
		case <-t.C:
			s.flush(ctx)
		}
	}
}

// Close stops the syncer and performs one final flush. Safe to call once.
func (s *Syncer) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		s.flush(context.Background())
	})
	return nil
}

func (s *Syncer) flush(ctx context.Context) {
	reqs, reqBodies, respBodies := s.buf.Drain()
	if len(reqs) > 0 {
		_ = s.sink.SaveMatched(ctx, reqs, reqBodies, respBodies)
	}
	before := s.now().Unix()
	_, _ = s.sink.SweepExpired(ctx, before)
	s.buf.SweepExpired()
}
