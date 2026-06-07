package reload_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/reload"
	"github.com/mockwave/mockwave/observability"
	"github.com/stretchr/testify/assert"
)

type fakeVersioned struct {
	mu      sync.Mutex
	version int64
	err     error
}

func (f *fakeVersioned) ConfigVersion() (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version, f.err
}
func (f *fakeVersioned) set(v int64)   { f.mu.Lock(); f.version = v; f.mu.Unlock() }

func TestReloader_ReloadsOnFirstTickAndOnChangeOnly(t *testing.T) {
	fv := &fakeVersioned{version: 1}
	var mu sync.Mutex
	calls := 0
	r := reload.New(fv, 10*time.Millisecond, func() error {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil
	}, observability.NoopLogger{})

	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)

	time.Sleep(45 * time.Millisecond) // several ticks, version stable
	mu.Lock()
	first := calls
	mu.Unlock()
	assert.Equal(t, 1, first, "reload only on first tick when version is stable")

	fv.set(2)
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	second := calls
	mu.Unlock()
	cancel()
	assert.Equal(t, 2, second, "reload again after version changed")
}

func TestReloader_SkipsTickOnVersionError(t *testing.T) {
	fv := &fakeVersioned{version: 1, err: errors.New("boom")}
	var mu sync.Mutex
	calls := 0
	r := reload.New(fv, 10*time.Millisecond, func() error {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil
	}, observability.NoopLogger{})

	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	time.Sleep(45 * time.Millisecond)
	cancel()
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 0, calls, "no reload while ConfigVersion errors")
}
