package chaos

import (
	"sync"
	"time"
)

type retryCounter struct {
	mu    sync.Mutex
	now   func() time.Time
	state map[string]*retryEntry
}

type retryEntry struct {
	count       int
	windowStart time.Time
}

func newRetryCounter(now func() time.Time) *retryCounter {
	return &retryCounter{now: now, state: map[string]*retryEntry{}}
}

func (c *retryCounter) shouldFail(key string, failFirst, windowSec int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.now()
	e, ok := c.state[key]
	if !ok || t.Sub(e.windowStart) >= time.Duration(windowSec)*time.Second {
		e = &retryEntry{windowStart: t}
		c.state[key] = e
	}
	e.count++
	return e.count <= failFirst
}
