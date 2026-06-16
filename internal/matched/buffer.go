package matched

import (
	"sort"
	"sync"
	"time"
)

// Buffer is a bounded, per-rule, thread-safe store of captured requests plus
// their out-of-line bodies. Newest entries list first. When the total entry
// count exceeds capacity the oldest entry (by insertion order) is evicted.
type Buffer struct {
	mu     sync.Mutex
	cap    int
	byRule map[string][]Request
	order  []ref
	reqB   map[string][]byte
	respB  map[string]interface{}
	now    func() time.Time
}

type ref struct {
	rule string
	id   string
}

// NewBuffer creates a Buffer holding at most capacity entries across all rules.
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = 1
	}
	return &Buffer{
		cap:    capacity,
		byRule: map[string][]Request{},
		reqB:   map[string][]byte{},
		respB:  map[string]interface{}{},
		now:    time.Now,
	}
}

// SetClock overrides the time source (tests only).
func (b *Buffer) SetClock(f func() time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.now = f
}

// Add inserts a captured request and its optional bodies.
func (b *Buffer) Add(r Request, reqBody []byte, respBody interface{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.byRule[r.RuleID] = append(b.byRule[r.RuleID], r)
	b.order = append(b.order, ref{rule: r.RuleID, id: r.ID})
	if r.RequestBodyID != "" && reqBody != nil {
		b.reqB[r.RequestBodyID] = reqBody
	}
	if r.ResponseBodyID != "" && respBody != nil {
		b.respB[r.ResponseBodyID] = respBody
	}
	b.evictLocked()
}

func (b *Buffer) evictLocked() {
	for len(b.order) > b.cap {
		old := b.order[0]
		b.order = b.order[1:]
		b.removeEntryLocked(old.rule, old.id)
	}
}

func (b *Buffer) removeEntryLocked(rule, id string) {
	entries := b.byRule[rule]
	for i := range entries {
		if entries[i].ID == id {
			if entries[i].RequestBodyID != "" {
				delete(b.reqB, entries[i].RequestBodyID)
			}
			if entries[i].ResponseBodyID != "" {
				delete(b.respB, entries[i].ResponseBodyID)
			}
			b.byRule[rule] = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	if len(b.byRule[rule]) == 0 {
		delete(b.byRule, rule)
	}
}

// List returns a filtered, paginated page of a rule's entries, newest first.
func (b *Buffer) List(ruleID string, q Query) Page {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	entries := b.byRule[ruleID]

	ordered := make([]Request, len(entries))
	copy(ordered, entries)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID > ordered[j].ID })

	afterID, _ := DecodeCursor(q.Cursor)
	limit := q.EffectiveLimit()

	out := make([]Request, 0, limit)
	started := afterID == ""
	for _, r := range ordered {
		if !started {
			if r.ID == afterID {
				started = true
			}
			continue
		}
		if r.Expired(now) {
			continue
		}
		if !q.Matches(r) {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			return Page{Items: out, NextCursor: b.nextCursorLocked(ordered, r.ID, q, now)}
		}
	}
	return Page{Items: out}
}

// nextCursorLocked returns a cursor if any non-expired matching entry exists
// strictly after lastID in ordered; "" otherwise.
func (b *Buffer) nextCursorLocked(ordered []Request, lastID string, q Query, now time.Time) string {
	seen := false
	for _, r := range ordered {
		if !seen {
			if r.ID == lastID {
				seen = true
			}
			continue
		}
		if !r.Expired(now) && q.Matches(r) {
			return EncodeCursor(lastID)
		}
	}
	return ""
}

// Get returns the full request (with bodies) for a rule+id.
func (b *Buffer) Get(ruleID, id string) (FullRequest, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range b.byRule[ruleID] {
		if r.ID == id {
			if r.Expired(b.now()) {
				return FullRequest{}, false
			}
			full := FullRequest{Request: r}
			if r.RequestBodyID != "" {
				full.RequestBody = b.reqB[r.RequestBodyID]
			}
			if r.ResponseBodyID != "" {
				full.ResponseBody = b.respB[r.ResponseBodyID]
			}
			return full, true
		}
	}
	return FullRequest{}, false
}

// Clear removes a rule's entries, or all entries when ruleID == "".
func (b *Buffer) Clear(ruleID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ruleID == "" {
		b.byRule = map[string][]Request{}
		b.order = nil
		b.reqB = map[string][]byte{}
		b.respB = map[string]interface{}{}
		return
	}
	kept := make([]ref, 0, len(b.order))
	for _, o := range b.order {
		if o.rule != ruleID {
			kept = append(kept, o)
		}
	}
	b.order = kept
	for _, r := range b.byRule[ruleID] {
		delete(b.reqB, r.RequestBodyID)
		delete(b.respB, r.ResponseBodyID)
	}
	delete(b.byRule, ruleID)
}

// Drain returns a snapshot of all entries and bodies for write-behind. It does
// NOT remove them — the buffer remains the query source.
func (b *Buffer) Drain() ([]Request, []RequestBody, []ResponseBody) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var reqs []Request
	for _, entries := range b.byRule {
		reqs = append(reqs, entries...)
	}
	reqBodies := make([]RequestBody, 0, len(b.reqB))
	for id, body := range b.reqB {
		reqBodies = append(reqBodies, RequestBody{ID: id, Body: body})
	}
	respBodies := make([]ResponseBody, 0, len(b.respB))
	for id, body := range b.respB {
		respBodies = append(respBodies, ResponseBody{ID: id, Body: body})
	}
	return reqs, reqBodies, respBodies
}

// Hydrate seeds the buffer from persisted entries (e.g. on startup). Bodies are
// not loaded eagerly; detail lookups fall back to the store for missing bodies.
func (b *Buffer) Hydrate(reqs []Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range reqs {
		b.byRule[r.RuleID] = append(b.byRule[r.RuleID], r)
		b.order = append(b.order, ref{rule: r.RuleID, id: r.ID})
	}
	b.evictLocked()
}

// SweepExpired drops entries whose TTL has passed; returns the count removed.
func (b *Buffer) SweepExpired() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	removed := 0
	// Collect expired refs first to avoid mutating byRule while scanning order.
	var toRemove []ref
	for _, o := range b.order {
		for _, r := range b.byRule[o.rule] {
			if r.ID == o.id && r.Expired(now) {
				toRemove = append(toRemove, o)
				break
			}
		}
	}
	for _, o := range toRemove {
		b.removeEntryLocked(o.rule, o.id)
		removed++
	}
	// Rebuild order without the removed refs.
	if removed > 0 {
		kept := make([]ref, 0, len(b.order)-removed)
		for _, o := range b.order {
			found := false
			for _, rm := range toRemove {
				if rm.rule == o.rule && rm.id == o.id {
					found = true
					break
				}
			}
			if !found {
				kept = append(kept, o)
			}
		}
		b.order = kept
	}
	return removed
}
