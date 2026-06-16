package jsonfile

import (
	"context"
	"sort"
	"sync"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
)

type matchedMem struct {
	mu         sync.Mutex
	byRule     map[string][]matched.Request
	reqBodies  map[string][]byte
	respBodies map[string]interface{}
}

func (s *Store) matched() *matchedMem {
	s.matchedOnce.Do(func() {
		s.matchedStore = &matchedMem{
			byRule:     map[string][]matched.Request{},
			reqBodies:  map[string][]byte{},
			respBodies: map[string]interface{}{},
		}
	})
	return s.matchedStore
}

func (s *Store) SaveMatched(_ context.Context, reqs []matched.Request, reqBodies []matched.RequestBody, respBodies []matched.ResponseBody) error {
	m := s.matched()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range reqs {
		existing := m.byRule[r.RuleID]
		replaced := false
		for i := range existing {
			if existing[i].ID == r.ID {
				existing[i] = r
				replaced = true
				break
			}
		}
		if !replaced {
			m.byRule[r.RuleID] = append(existing, r)
		}
	}
	for _, rb := range reqBodies {
		m.reqBodies[rb.ID] = rb.Body
	}
	for _, sb := range respBodies {
		m.respBodies[sb.ID] = sb.Body
	}
	return nil
}

func (s *Store) ListMatched(_ context.Context, ruleID string, q store.MatchedQuery) (store.MatchedPage, error) {
	m := s.matched()
	m.mu.Lock()
	defer m.mu.Unlock()
	var entries []matched.Request
	if ruleID == "" {
		for _, e := range m.byRule {
			entries = append(entries, e...)
		}
	} else {
		entries = append(entries, m.byRule[ruleID]...)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].ID > entries[j].ID })

	afterID, _ := matched.DecodeCursor(q.Cursor)
	limit := q.EffectiveLimit()
	out := make([]matched.Request, 0, limit)
	started := afterID == ""
	for idx, r := range entries {
		if !started {
			if r.ID == afterID {
				started = true
			}
			continue
		}
		if !q.Matches(r) {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			if hasMoreMatching(entries[idx+1:], q) {
				return store.MatchedPage{Items: out, NextCursor: matched.EncodeCursor(r.ID)}, nil
			}
			break
		}
	}
	return store.MatchedPage{Items: out}, nil
}

func hasMoreMatching(rest []matched.Request, q store.MatchedQuery) bool {
	for _, r := range rest {
		if q.Matches(r) {
			return true
		}
	}
	return false
}

func (s *Store) GetMatched(_ context.Context, ruleID, id string) (*matched.FullRequest, error) {
	m := s.matched()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.byRule[ruleID] {
		if r.ID == id {
			full := &matched.FullRequest{Request: r}
			if r.RequestBodyID != "" {
				full.RequestBody = m.reqBodies[r.RequestBodyID]
			}
			if r.ResponseBodyID != "" {
				full.ResponseBody = m.respBodies[r.ResponseBodyID]
			}
			return full, nil
		}
	}
	return nil, nil
}

func (s *Store) DeleteMatched(_ context.Context, ruleID string) error {
	m := s.matched()
	m.mu.Lock()
	defer m.mu.Unlock()
	if ruleID == "" {
		m.byRule = map[string][]matched.Request{}
		m.reqBodies = map[string][]byte{}
		m.respBodies = map[string]interface{}{}
		return nil
	}
	for _, r := range m.byRule[ruleID] {
		if r.RequestBodyID != "" {
			delete(m.reqBodies, r.RequestBodyID)
		}
		if r.ResponseBodyID != "" {
			delete(m.respBodies, r.ResponseBodyID)
		}
	}
	delete(m.byRule, ruleID)
	return nil
}

func (s *Store) SweepExpired(_ context.Context, before int64) (int, error) {
	m := s.matched()
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := 0
	for rule, entries := range m.byRule {
		kept := make([]matched.Request, 0, len(entries))
		for _, r := range entries {
			if r.TTL != 0 && r.TTL < before {
				if r.RequestBodyID != "" {
					delete(m.reqBodies, r.RequestBodyID)
				}
				if r.ResponseBodyID != "" {
					delete(m.respBodies, r.ResponseBodyID)
				}
				removed++
				continue
			}
			kept = append(kept, r)
		}
		if len(kept) == 0 {
			delete(m.byRule, rule)
		} else {
			m.byRule[rule] = kept
		}
	}
	return removed, nil
}
