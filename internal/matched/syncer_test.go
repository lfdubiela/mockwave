package matched_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingSink struct {
	mu        sync.Mutex
	saveCalls int
	saved     []matched.Request
	sweptWith []int64
	saveErr   error
}

func (s *recordingSink) SaveMatched(_ context.Context, reqs []matched.Request, _ []matched.RequestBody, _ []matched.ResponseBody) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls++
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, reqs...)
	return nil
}
func (s *recordingSink) SweepExpired(_ context.Context, before int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweptWith = append(s.sweptWith, before)
	return 0, nil
}

func TestSyncer_FlushOnTick(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	sink := &recordingSink{}
	sy := matched.NewSyncer(buf, sink, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sy.Run(ctx)
	assert.Eventually(t, func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return sink.saveCalls >= 1 && len(sink.saved) == 1
	}, time.Second, 5*time.Millisecond)
}

func TestSyncer_FlushOnClose(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	sink := &recordingSink{}
	sy := matched.NewSyncer(buf, sink, time.Hour)
	require.NoError(t, sy.Close())
	sink.mu.Lock()
	defer sink.mu.Unlock()
	assert.Equal(t, 1, sink.saveCalls)
	require.Len(t, sink.saved, 1)
}

func TestSyncer_SaveErrorDoesNotPanic(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	sink := &recordingSink{saveErr: errors.New("boom")}
	sy := matched.NewSyncer(buf, sink, time.Hour)
	assert.NoError(t, sy.Close())
}
