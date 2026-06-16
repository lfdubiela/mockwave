# Matched Request Capture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Capture requests that match a rule into an in-memory buffer plus a write-behind store, exposed via a paginated, filterable GET API so e2e testers can assert the exact request a system-under-test sent to the mock.

**Architecture:** A `matched.Buffer` (per-rule, time-ordered ring) is the query source — immediate, no sync lag. The metrics middleware's matched branch adds each captured request to the buffer (best-effort, never blocks the request). A write-behind `Syncer` goroutine flushes the buffer to a `store.MatchedStore` every N seconds for durability; on startup the buffer is hydrated from the store. Expiration is global-TTL: native (DynamoDB TTL / Mongo+Cosmos TTL index) where available, a background sweep for the JSON store, and lazy-skip in the buffer. New REST endpoints `GET /api/matched/{rule_id}` (paginated, reduced) and `GET /api/matched/{rule_id}/{id}` (full, with bodies) serve queries. Capture is opt-in (default off, zero overhead).

**Tech Stack:** Go 1.21+, `github.com/google/uuid` (UUID v7 for time-ordered IDs — already a dependency, no new module), `testify` for tests, existing store adapters (jsonfile, dynamodb, mongodb, cosmos).

**Design deviations from spec (intentional, for correctness):**
- Capture happens in the existing `internal/metrics` middleware matched branch, not a new pipeline stage — that is where both `pctx.Matched` and the final `pctx.Response` are known, mirroring how `unmatched` is already captured.
- List/Get queries read the in-memory buffer (authoritative for the TTL window on this instance); the store provides write-behind durability + restart hydration. Cross-instance live read consistency is out of scope and documented. This makes the e2e flow (send request → immediately query) work with no sync-lag race.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/matched/request.go` (create) | `Request`, `FullRequest`, `RequestBody`, `ResponseBody` types; `NewID()`. |
| `internal/matched/buffer.go` (create) | Per-rule time-ordered ring buffer; Add, List (filtered+paginated), Get, Clear, Drain, Hydrate, lazy-expire. |
| `internal/matched/query.go` (create) | `Query` (filters + cursor + limit), `Page`, cursor encode/decode, in-memory filter + glob match. |
| `internal/matched/syncer.go` (create) | Write-behind goroutine: periodic Drain→Save, sweep, graceful flush on Close. |
| `store/store.go` (modify) | Add `MatchedStore` optional-capability interface + re-export query/page types via aliases. |
| `internal/adapters/out/jsonfile/matched.go` (create) | JSON store MatchedStore impl + SweepExpired. |
| `internal/adapters/out/dynamodb/matched.go` (create) | DynamoDB MatchedStore impl (native `ttl` attribute). |
| `internal/adapters/out/mongodb/matched.go` (create) | Mongo MatchedStore impl (TTL index on `expireAt`). |
| `internal/adapters/out/cosmos/matched.go` (create) | Cosmos MatchedStore impl (per-item `ttl`). |
| `internal/metrics/middleware.go` (modify) | Add optional matched capture hook in the matched branch. |
| `server/server.go` (modify) | `MatchedConfig` field, buffer/syncer lifecycle, hydrate on start, Close flush, accessor. |
| `server/matched.go` (create) | `MatchedConfig` resolution from env, store-capability detection, defaults. |
| `internal/adapters/cfg/restapi/matched_handlers.go` (create) | `/api/matched/{rule}` + `/{rule}/{id}` + DELETE handlers. |
| `internal/adapters/cfg/restapi/server.go` (modify) | Wire matched buffer + routes; `WithMatched` MuxOption. |
| `cmd/mockwave/*.go` (modify) | `--matched-*` CLI flags → env. |
| `tests/integration/matched_test.go` (create) | E2E: rule → request → assert response + assert captured request. |

---

## Task 1: Matched request types

**Files:**
- Create: `internal/matched/request.go`
- Test: `internal/matched/request_test.go`

- [ ] **Step 1: Write the failing test**

```go
package matched_test

import (
	"testing"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewID_Unique(t *testing.T) {
	a := matched.NewID()
	b := matched.NewID()
	require.NotEmpty(t, a)
	assert.NotEqual(t, a, b)
}

func TestNewID_TimeOrdered(t *testing.T) {
	// UUID v7 is lexicographically time-ordered; later IDs sort after earlier.
	first := matched.NewID()
	second := matched.NewID()
	assert.Less(t, first, second)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/matched/ -run TestNewID -v`
Expected: FAIL — package/`NewID` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// Package matched captures requests that matched a rule so testers can later
// retrieve, via the admin API, the exact request a system-under-test sent to
// the mock. Capture is best-effort and never blocks or fails a request.
package matched

import (
	"time"

	"github.com/google/uuid"
)

// NewID returns a time-ordered unique id (UUID v7). v7 is lexicographically
// sortable by creation time, so ids double as pagination cursors.
func NewID() string {
	// uuid.NewV7 only errors if the system RNG fails; fall back to v4 then.
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

// Request is the reduced shape stored in memory and persisted. Bodies are kept
// out of line (referenced by *BodyID) so list queries stay small.
type Request struct {
	ID       string            `json:"id"`      // UUID v7, time-ordered
	RuleID   string            `json:"rule_id"` // which rule matched
	At       time.Time         `json:"at"`      // capture timestamp

	Protocol string            `json:"protocol"` // http|graphql|soap|grpc
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Headers  map[string]string `json:"headers"`
	Query    map[string]string `json:"query"`

	ResponseStatus  int               `json:"response_status"`
	ResponseHeaders map[string]string `json:"response_headers"`

	RequestBodyID  string `json:"request_body_id,omitempty"`
	ResponseBodyID string `json:"response_body_id,omitempty"`

	// TTL is the epoch-second expiry (At + globalTTL); used by stores with
	// native TTL. Zero means no expiry hint set.
	TTL int64 `json:"ttl,omitempty"`
}

// Expired reports whether the entry's TTL has passed relative to now.
// A zero TTL never expires.
func (r Request) Expired(now time.Time) bool {
	return r.TTL != 0 && now.Unix() >= r.TTL
}

// RequestBody / ResponseBody hold the out-of-line payloads, keyed by the
// matching Request.*BodyID.
type RequestBody struct {
	ID   string `json:"id"`
	Body []byte `json:"body"`
}

type ResponseBody struct {
	ID   string      `json:"id"`
	Body interface{} `json:"body"`
}

// FullRequest is a Request plus its resolved bodies, returned by the detail
// endpoint.
type FullRequest struct {
	Request
	RequestBody  []byte      `json:"request_body,omitempty"`
	ResponseBody interface{} `json:"response_body,omitempty"`
	// BodyWarning is set when a body could not be resolved (lazy-load failure).
	BodyWarning string `json:"body_warning,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/matched/ -run TestNewID -v`
Expected: PASS. (If `uuid.NewV7` is unresolved, run `go get github.com/google/uuid@v1.6.0` to promote it from indirect, then `go mod tidy`.)

- [ ] **Step 5: Commit**

```bash
git add internal/matched/request.go internal/matched/request_test.go go.mod go.sum
git commit -m "feat(matched): request types and time-ordered id"
```

---

## Task 2: Query filters, cursor, and page types

**Files:**
- Create: `internal/matched/query.go`
- Test: `internal/matched/query_test.go`

- [ ] **Step 1: Write the failing test**

```go
package matched_test

import (
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func req(id, method, path string, status int, h map[string]string) matched.Request {
	return matched.Request{ID: id, Method: method, Path: path, ResponseStatus: status, Headers: h, At: time.Unix(1, 0)}
}

func TestQuery_Matches_MethodPathStatus(t *testing.T) {
	q := matched.Query{Method: "POST", Path: "/users/*", Status: 201}
	assert.True(t, q.Matches(req("1", "POST", "/users/42", 201, nil)))
	assert.False(t, q.Matches(req("2", "GET", "/users/42", 201, nil)))   // method
	assert.False(t, q.Matches(req("3", "POST", "/orders/1", 201, nil)))  // path
	assert.False(t, q.Matches(req("4", "POST", "/users/42", 500, nil)))  // status
}

func TestQuery_Matches_HeadersAND(t *testing.T) {
	q := matched.Query{Headers: map[string]string{"x-cid": "abc", "x-foo": "bar"}}
	assert.True(t, q.Matches(req("1", "GET", "/x", 200, map[string]string{"x-cid": "abc", "x-foo": "bar", "x-extra": "y"})))
	assert.False(t, q.Matches(req("2", "GET", "/x", 200, map[string]string{"x-cid": "abc"})))            // missing x-foo
	assert.False(t, q.Matches(req("3", "GET", "/x", 200, map[string]string{"x-cid": "zzz", "x-foo": "bar"}))) // wrong value
}

func TestQuery_HeaderMatch_CaseInsensitiveKey(t *testing.T) {
	q := matched.Query{Headers: map[string]string{"X-CID": "abc"}}
	assert.True(t, q.Matches(req("1", "GET", "/x", 200, map[string]string{"x-cid": "abc"})))
}

func TestQuery_EmptyMatchesAll(t *testing.T) {
	q := matched.Query{}
	assert.True(t, q.Matches(req("1", "GET", "/anything", 999, nil)))
}

func TestCursor_RoundTrip(t *testing.T) {
	c := matched.EncodeCursor("01900000-0000-7000-8000-000000000000")
	got, err := matched.DecodeCursor(c)
	require.NoError(t, err)
	assert.Equal(t, "01900000-0000-7000-8000-000000000000", got)
}

func TestDecodeCursor_Empty(t *testing.T) {
	got, err := matched.DecodeCursor("")
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestDecodeCursor_Invalid(t *testing.T) {
	_, err := matched.DecodeCursor("!!!not-base64!!!")
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/matched/ -run 'TestQuery|TestCursor|TestDecodeCursor' -v`
Expected: FAIL — `Query`, `EncodeCursor`, `DecodeCursor` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
package matched

import (
	"encoding/base64"
	"path"
	"strings"
)

// DefaultLimit is the page size used when a query omits Limit.
const DefaultLimit = 20

// MaxLimit caps the page size a caller may request.
const MaxLimit = 100

// Query filters a rule's captured requests and controls pagination.
type Query struct {
	Cursor  string            // opaque; "" starts from the newest page
	Limit   int               // 0 → DefaultLimit; capped at MaxLimit
	Method  string            // exact, case-insensitive; "" = any
	Path    string            // glob (path.Match); "" = any
	Status  int               // exact response status; 0 = any
	Headers map[string]string // AND-matched, exact value, case-insensitive key
}

// EffectiveLimit resolves Limit against the default and cap.
func (q Query) EffectiveLimit() int {
	if q.Limit <= 0 {
		return DefaultLimit
	}
	if q.Limit > MaxLimit {
		return MaxLimit
	}
	return q.Limit
}

// Matches reports whether r satisfies every filter in q. A zero-value Query
// matches everything.
func (q Query) Matches(r Request) bool {
	if q.Method != "" && !strings.EqualFold(q.Method, r.Method) {
		return false
	}
	if q.Path != "" {
		ok, err := path.Match(q.Path, r.Path)
		if err != nil || !ok {
			return false
		}
	}
	if q.Status != 0 && q.Status != r.ResponseStatus {
		return false
	}
	for k, v := range q.Headers {
		if headerLookup(r.Headers, k) != v {
			return false
		}
	}
	return true
}

// headerLookup finds key case-insensitively, returning "" when absent.
func headerLookup(h map[string]string, key string) string {
	if hv, ok := h[key]; ok {
		return hv
	}
	for hk, hv := range h {
		if strings.EqualFold(hk, key) {
			return hv
		}
	}
	return ""
}

// Page is one slice of list results.
type Page struct {
	Items      []Request `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// EncodeCursor wraps an id as an opaque cursor token.
func EncodeCursor(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

// DecodeCursor reverses EncodeCursor. An empty cursor decodes to "".
func DecodeCursor(c string) (string, error) {
	if c == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/matched/ -run 'TestQuery|TestCursor|TestDecodeCursor' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/matched/query.go internal/matched/query_test.go
git commit -m "feat(matched): query filters, cursor, page types"
```

---

## Task 3: In-memory per-rule buffer

**Files:**
- Create: `internal/matched/buffer.go`
- Test: `internal/matched/buffer_test.go`

The buffer holds captured requests grouped by rule, newest-last, bounded by a
global capacity (oldest overall evicted when full). List walks a rule's entries
newest→oldest, skips expired, applies the query filter, and paginates by id
cursor. Bodies are stored in side maps keyed by `*BodyID`.

- [ ] **Step 1: Write the failing test**

```go
package matched_test

import (
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuffer_AddAndListNewestFirst(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	b.Add(matched.Request{ID: "b", RuleID: "r1", At: time.Unix(2, 0)}, nil, nil)
	page := b.List("r1", matched.Query{})
	require.Len(t, page.Items, 2)
	assert.Equal(t, "b", page.Items[0].ID) // newest first
	assert.Equal(t, "a", page.Items[1].ID)
}

func TestBuffer_ListScopedToRule(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	b.Add(matched.Request{ID: "b", RuleID: "r2", At: time.Unix(2, 0)}, nil, nil)
	assert.Len(t, b.List("r1", matched.Query{}).Items, 1)
	assert.Len(t, b.List("r2", matched.Query{}).Items, 1)
	assert.Empty(t, b.List("nope", matched.Query{}).Items)
}

func TestBuffer_ListAppliesFilter(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Add(matched.Request{ID: "a", RuleID: "r1", Method: "GET", At: time.Unix(1, 0)}, nil, nil)
	b.Add(matched.Request{ID: "b", RuleID: "r1", Method: "POST", At: time.Unix(2, 0)}, nil, nil)
	page := b.List("r1", matched.Query{Method: "POST"})
	require.Len(t, page.Items, 1)
	assert.Equal(t, "b", page.Items[0].ID)
}

func TestBuffer_Pagination(t *testing.T) {
	b := matched.NewBuffer(10)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		b.Add(matched.Request{ID: id, RuleID: "r1", At: time.Unix(int64(i+1), 0)}, nil, nil)
	}
	first := b.List("r1", matched.Query{Limit: 2})
	require.Len(t, first.Items, 2)
	assert.Equal(t, "e", first.Items[0].ID) // newest
	assert.Equal(t, "d", first.Items[1].ID)
	require.NotEmpty(t, first.NextCursor)

	second := b.List("r1", matched.Query{Limit: 2, Cursor: first.NextCursor})
	require.Len(t, second.Items, 2)
	assert.Equal(t, "c", second.Items[0].ID)
	assert.Equal(t, "b", second.Items[1].ID)

	third := b.List("r1", matched.Query{Limit: 2, Cursor: second.NextCursor})
	require.Len(t, third.Items, 1)
	assert.Equal(t, "a", third.Items[0].ID)
	assert.Empty(t, third.NextCursor) // last page
}

func TestBuffer_GlobalCapacityEvictsOldest(t *testing.T) {
	b := matched.NewBuffer(2)
	b.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	b.Add(matched.Request{ID: "b", RuleID: "r1", At: time.Unix(2, 0)}, nil, nil)
	b.Add(matched.Request{ID: "c", RuleID: "r1", At: time.Unix(3, 0)}, nil, nil) // evicts "a"
	page := b.List("r1", matched.Query{})
	require.Len(t, page.Items, 2)
	assert.Equal(t, "c", page.Items[0].ID)
	assert.Equal(t, "b", page.Items[1].ID)
}

func TestBuffer_ListSkipsExpired(t *testing.T) {
	b := matched.NewBuffer(10)
	now := time.Unix(100, 0)
	b.SetClock(func() time.Time { return now })
	b.Add(matched.Request{ID: "live", RuleID: "r1", At: time.Unix(99, 0), TTL: 200}, nil, nil)
	b.Add(matched.Request{ID: "dead", RuleID: "r1", At: time.Unix(1, 0), TTL: 50}, nil, nil)
	page := b.List("r1", matched.Query{})
	require.Len(t, page.Items, 1)
	assert.Equal(t, "live", page.Items[0].ID)
}

func TestBuffer_GetFull(t *testing.T) {
	b := matched.NewBuffer(10)
	r := matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0), RequestBodyID: "rb", ResponseBodyID: "sb"}
	b.Add(r, []byte(`{"in":1}`), map[string]any{"out": 2})
	full, ok := b.Get("r1", "a")
	require.True(t, ok)
	assert.Equal(t, "a", full.ID)
	assert.JSONEq(t, `{"in":1}`, string(full.RequestBody))
	assert.Equal(t, map[string]any{"out": 2}, full.ResponseBody)
}

func TestBuffer_GetMissing(t *testing.T) {
	b := matched.NewBuffer(10)
	_, ok := b.Get("r1", "nope")
	assert.False(t, ok)
}

func TestBuffer_ClearRule(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	b.Add(matched.Request{ID: "b", RuleID: "r2", At: time.Unix(2, 0)}, nil, nil)
	b.Clear("r1")
	assert.Empty(t, b.List("r1", matched.Query{}).Items)
	assert.Len(t, b.List("r2", matched.Query{}).Items, 1)
}

func TestBuffer_ClearAll(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	b.Clear("")
	assert.Empty(t, b.List("r1", matched.Query{}).Items)
}

func TestBuffer_DrainReturnsAllAndKeeps(t *testing.T) {
	b := matched.NewBuffer(10)
	b.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0), RequestBodyID: "rb"}, []byte("x"), nil)
	reqs, bodies, respBodies := b.Drain()
	require.Len(t, reqs, 1)
	require.Len(t, bodies, 1)
	assert.Equal(t, "rb", bodies[0].ID)
	assert.Len(t, respBodies, 0)
	// Drain is a snapshot, not a clear: entries remain queryable.
	assert.Len(t, b.List("r1", matched.Query{}).Items, 1)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/matched/ -run TestBuffer -v`
Expected: FAIL — `NewBuffer` and methods undefined.

- [ ] **Step 3: Write minimal implementation**

```go
package matched

import (
	"sort"
	"sync"
	"time"
)

// Buffer is a bounded, per-rule, thread-safe store of captured requests plus
// their out-of-line bodies. Newest entries list first. When the total entry
// count exceeds capacity the oldest entry (by At) is evicted.
type Buffer struct {
	mu     sync.Mutex
	cap    int
	byRule map[string][]Request    // rule id → entries, append order (oldest→newest)
	order  []ref                   // global insertion order for eviction
	reqB   map[string][]byte       // RequestBodyID → body
	respB  map[string]interface{}  // ResponseBodyID → body
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

// evictLocked drops oldest entries until within capacity.
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

	// Newest first.
	ordered := make([]Request, len(entries))
	copy(ordered, entries)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID > ordered[j].ID })

	afterID, _ := DecodeCursor(q.Cursor) // invalid cursor → start from top
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
			// Determine whether more matching entries remain after this one.
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
	kept := b.order[:0]
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
	keptOrder := b.order[:0]
	for _, o := range b.order {
		expired := false
		for _, r := range b.byRule[o.rule] {
			if r.ID == o.id && r.Expired(now) {
				expired = true
				break
			}
		}
		if expired {
			b.removeEntryLocked(o.rule, o.id)
			removed++
			continue
		}
		keptOrder = append(keptOrder, o)
	}
	b.order = keptOrder
	return removed
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/matched/ -run TestBuffer -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/matched/buffer.go internal/matched/buffer_test.go
git commit -m "feat(matched): per-rule in-memory buffer with pagination and ttl"
```

---

## Task 4: MatchedStore interface

**Files:**
- Modify: `store/store.go`
- Test: `store/matched_store_test.go`

- [ ] **Step 1: Write the failing test**

```go
package store_test

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
	"github.com/stretchr/testify/assert"
)

// fakeMatched verifies the interface is satisfiable and exercises the aliases.
type fakeMatched struct{ saved []matched.Request }

func (f *fakeMatched) SaveMatched(_ context.Context, reqs []matched.Request, _ []matched.RequestBody, _ []matched.ResponseBody) error {
	f.saved = append(f.saved, reqs...)
	return nil
}
func (f *fakeMatched) ListMatched(_ context.Context, ruleID string, _ store.MatchedQuery) (store.MatchedPage, error) {
	return store.MatchedPage{}, nil
}
func (f *fakeMatched) GetMatched(_ context.Context, _, _ string) (*matched.FullRequest, error) {
	return nil, nil
}
func (f *fakeMatched) DeleteMatched(_ context.Context, _ string) error { return nil }
func (f *fakeMatched) SweepExpired(_ context.Context, _ int64) (int, error) { return 0, nil }

func TestMatchedStore_Satisfiable(t *testing.T) {
	var s store.MatchedStore = &fakeMatched{}
	err := s.SaveMatched(context.Background(), []matched.Request{{ID: "a"}}, nil, nil)
	assert.NoError(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./store/ -run TestMatchedStore -v`
Expected: FAIL — `MatchedStore`, `MatchedQuery`, `MatchedPage` undefined.

- [ ] **Step 3: Write minimal implementation**

Append to `store/store.go`:

```go
import (
	"context"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/matched"
)

// MatchedQuery and MatchedPage are re-exported from the matched package so
// embedders implementing MatchedStore need not import an internal package for
// these value types.
type MatchedQuery = matched.Query
type MatchedPage = matched.Page

// MatchedStore is an optional capability for stores that persist matched
// requests (write-behind durability for the in-memory capture buffer).
// Implementations apply native TTL where available; SweepExpired is a no-op
// for those and an explicit delete for stores without native TTL (JSON).
type MatchedStore interface {
	// SaveMatched upserts a batch of requests and their out-of-line bodies.
	SaveMatched(ctx context.Context, reqs []matched.Request, reqBodies []matched.RequestBody, respBodies []matched.ResponseBody) error
	// ListMatched returns a filtered, paginated page for a rule, newest first.
	ListMatched(ctx context.Context, ruleID string, q MatchedQuery) (MatchedPage, error)
	// GetMatched returns the full request (with bodies), or (nil, nil) when absent.
	GetMatched(ctx context.Context, ruleID, id string) (*matched.FullRequest, error)
	// DeleteMatched removes a rule's captures, or all when ruleID == "".
	DeleteMatched(ctx context.Context, ruleID string) error
	// SweepExpired deletes entries whose TTL epoch-seconds is < before.
	// Native-TTL stores return (0, nil).
	SweepExpired(ctx context.Context, before int64) (int, error)
}
```

Note: the existing `store.go` import block has only `domain`. Merge the new imports (`context`, `matched`) into the existing block rather than adding a second `import`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./store/ -run TestMatchedStore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add store/store.go store/matched_store_test.go
git commit -m "feat(store): MatchedStore optional-capability interface"
```

---

## Task 5: JSON store MatchedStore + sweep

**Files:**
- Create: `internal/adapters/out/jsonfile/matched.go`
- Test: `internal/adapters/out/jsonfile/matched_test.go`

The JSON store keeps matched captures in memory only (the config file stays
rules+sims). It has no native TTL, so `SweepExpired` actively deletes. This impl
backs durability across the process lifetime and gives the JSON backend a
working MatchedStore for tests and embedders.

- [ ] **Step 1: Write the failing test**

```go
package jsonfile_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStore(t *testing.T) *jsonfile.Store {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cfg.json")
	require.NoError(t, jsonfile.WriteEmpty(p))
	s, err := jsonfile.NewStore(p)
	require.NoError(t, err)
	return s
}

func TestJSONMatched_SaveListGet(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	reqs := []matched.Request{
		{ID: "a", RuleID: "r1", Method: "GET", Path: "/x", RequestBodyID: "rb"},
		{ID: "b", RuleID: "r1", Method: "POST", Path: "/y"},
	}
	require.NoError(t, s.SaveMatched(ctx, reqs, []matched.RequestBody{{ID: "rb", Body: []byte("hi")}}, nil))

	page, err := s.ListMatched(ctx, "r1", store.MatchedQuery{})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	assert.Equal(t, "b", page.Items[0].ID) // newest first

	full, err := s.GetMatched(ctx, "r1", "a")
	require.NoError(t, err)
	require.NotNil(t, full)
	assert.Equal(t, []byte("hi"), full.RequestBody)
}

func TestJSONMatched_GetAbsent(t *testing.T) {
	s := newStore(t)
	full, err := s.GetMatched(context.Background(), "r1", "nope")
	require.NoError(t, err)
	assert.Nil(t, full)
}

func TestJSONMatched_Delete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	require.NoError(t, s.SaveMatched(ctx, []matched.Request{{ID: "a", RuleID: "r1"}}, nil, nil))
	require.NoError(t, s.DeleteMatched(ctx, "r1"))
	page, err := s.ListMatched(ctx, "r1", store.MatchedQuery{})
	require.NoError(t, err)
	assert.Empty(t, page.Items)
}

func TestJSONMatched_SweepExpired(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	require.NoError(t, s.SaveMatched(ctx, []matched.Request{
		{ID: "old", RuleID: "r1", TTL: 50},
		{ID: "new", RuleID: "r1", TTL: 200},
	}, nil, nil))
	n, err := s.SweepExpired(ctx, 100) // remove TTL < 100
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	page, _ := s.ListMatched(ctx, "r1", store.MatchedQuery{})
	require.Len(t, page.Items, 1)
	assert.Equal(t, "new", page.Items[0].ID)
}
```

If `jsonfile.WriteEmpty` does not exist, replace `newStore` with the existing
test helper used in `internal/adapters/out/jsonfile/store_test.go` for creating
a store from a temp file (read that file first to match its pattern), or write
a minimal `{"rules":[],"simulations":[]}` file with `os.WriteFile` before
`NewStore`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/out/jsonfile/ -run TestJSONMatched -v`
Expected: FAIL — matched methods undefined on `*Store`.

- [ ] **Step 3: Write minimal implementation**

```go
package jsonfile

import (
	"context"
	"sort"
	"sync"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
)

// matchedMem is the in-memory matched-capture store backing the JSON file
// backend. It is independent of the rules/sims config file.
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
	entries := make([]matched.Request, len(m.byRule[ruleID]))
	copy(entries, m.byRule[ruleID])
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
		delete(m.reqBodies, r.RequestBodyID)
		delete(m.respBodies, r.ResponseBodyID)
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
		kept := entries[:0]
		for _, r := range entries {
			if r.TTL != 0 && r.TTL < before {
				delete(m.reqBodies, r.RequestBodyID)
				delete(m.respBodies, r.ResponseBodyID)
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
```

Add the two fields to the `Store` struct in `internal/adapters/out/jsonfile/store.go` (inside the existing struct definition):

```go
type Store struct {
	mu     sync.RWMutex
	// ... existing fields ...

	matchedOnce  sync.Once
	matchedStore *matchedMem
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/out/jsonfile/ -run TestJSONMatched -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/out/jsonfile/matched.go internal/adapters/out/jsonfile/matched_test.go internal/adapters/out/jsonfile/store.go
git commit -m "feat(jsonfile): MatchedStore in-memory impl with sweep"
```

---

## Task 6: Write-behind syncer

**Files:**
- Create: `internal/matched/syncer.go`
- Test: `internal/matched/syncer_test.go`

- [ ] **Step 1: Write the failing test**

```go
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
	sy := matched.NewSyncer(buf, sink, time.Hour) // never ticks in test window

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
	assert.NoError(t, sy.Close()) // error logged/swallowed, Close still succeeds
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/matched/ -run TestSyncer -v`
Expected: FAIL — `NewSyncer` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
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

// Logger is the minimal logging surface the syncer uses; nil disables logging.
type Logger interface {
	Warn(ctx context.Context, msg string, fields ...interface{})
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/matched/ -run TestSyncer -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/matched/syncer.go internal/matched/syncer_test.go
git commit -m "feat(matched): write-behind syncer with periodic flush and sweep"
```

---

## Task 7: Capture hook in metrics middleware

**Files:**
- Modify: `internal/metrics/middleware.go`
- Test: `internal/metrics/middleware_matched_test.go`

The middleware gains an optional `MatchedCapturer` invoked in the matched
branch. It extracts the reduced request/response and adds to the buffer.

- [ ] **Step 1: Write the failing test**

```go
package metrics_test

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/internal/metrics"
	"github.com/mockwave/mockwave/internal/unmatched"
	"github.com/mockwave/mockwave/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMiddleware_CapturesMatched(t *testing.T) {
	buf := matched.NewBuffer(10)
	exec := &stubExecutor{matchedRule: &domain.Rule{ID: "r1", Name: "R"}}
	mw := metrics.NewMiddleware(exec, metrics.NewCollector(), unmatched.NewBuffer(10), observability.NoopTracer{}, observability.NoopMetrics{})
	mw.SetMatchedCapture(buf, 3600)

	pctx := &pipeline.PipelineContext{
		Request: pipeline.NormalizedRequest{
			Method:  "POST",
			Path:    "/users",
			Protocol: "http",
			Headers: map[string]string{"x-cid": "abc"},
			Body:    []byte(`{"name":"x"}`),
		},
		Response: &pipeline.MockResponse{Status: 201, Headers: map[string]string{"content-type": "application/json"}, Body: map[string]any{"id": 1}},
	}
	require.NoError(t, mw.Execute(context.Background(), pctx))

	page := buf.List("r1", matched.Query{})
	require.Len(t, page.Items, 1)
	got := page.Items[0]
	assert.Equal(t, "r1", got.RuleID)
	assert.Equal(t, "POST", got.Method)
	assert.Equal(t, "/users", got.Path)
	assert.Equal(t, 201, got.ResponseStatus)
	assert.Equal(t, "abc", got.Headers["x-cid"])
	assert.NotZero(t, got.TTL) // expiry hint set from ttl seconds

	full, ok := buf.Get("r1", got.ID)
	require.True(t, ok)
	assert.JSONEq(t, `{"name":"x"}`, string(full.RequestBody))
	assert.Equal(t, map[string]any{"id": 1}, full.ResponseBody)
}

func TestMiddleware_NoCaptureWhenDisabled(t *testing.T) {
	exec := &stubExecutor{matchedRule: &domain.Rule{ID: "r1"}}
	mw := metrics.NewMiddleware(exec, metrics.NewCollector(), unmatched.NewBuffer(10), observability.NoopTracer{}, observability.NoopMetrics{})
	// no SetMatchedCapture call → capture disabled, must not panic
	pctx := &pipeline.PipelineContext{Request: pipeline.NormalizedRequest{Method: "GET", Path: "/x", Protocol: "http"}}
	require.NoError(t, mw.Execute(context.Background(), pctx))
}
```

Check `internal/metrics/middleware_obs_test.go` for the existing `stubExecutor`
definition; reuse it (same package `metrics_test`). If `stubExecutor` does not
set `pctx.Matched`, confirm it does — `TestMiddleware_CallsRecorderOnHit`
already relies on `matchedRule` being applied, so the stub sets `pctx.Matched`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/metrics/ -run 'TestMiddleware_CapturesMatched|TestMiddleware_NoCaptureWhenDisabled' -v`
Expected: FAIL — `SetMatchedCapture` undefined.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/metrics/middleware.go`:

```go
import (
	// existing imports plus:
	"time"

	"github.com/mockwave/mockwave/internal/matched"
)

// add to Middleware struct:
//   matchedBuf *matched.Buffer // may be nil → capture disabled
//   matchedTTL int            // seconds

// SetMatchedCapture enables matched-request capture into buf with the given
// global TTL in seconds. Pass a nil buf to leave capture disabled.
func (m *Middleware) SetMatchedCapture(buf *matched.Buffer, ttlSeconds int) {
	m.matchedBuf = buf
	m.matchedTTL = ttlSeconds
}

// captureMatched records a matched request into the buffer (best-effort).
func (m *Middleware) captureMatched(pctx *pipeline.PipelineContext) {
	if m.matchedBuf == nil || pctx.Matched == nil {
		return
	}
	now := time.Now()
	r := matched.Request{
		ID:              matched.NewID(),
		RuleID:          pctx.Matched.ID,
		At:              now,
		Protocol:        pctx.Request.Protocol,
		Method:          pctx.Request.Method,
		Path:            pctx.Request.Path,
		Headers:         pctx.Request.Headers,
		Query:           pctx.Request.Query,
	}
	if m.matchedTTL > 0 {
		r.TTL = now.Add(time.Duration(m.matchedTTL) * time.Second).Unix()
	}
	var reqBody []byte
	if len(pctx.Request.Body) > 0 {
		r.RequestBodyID = matched.NewID()
		reqBody = pctx.Request.Body
	}
	var respBody interface{}
	if pctx.Response != nil {
		r.ResponseStatus = pctx.Response.Status
		r.ResponseHeaders = pctx.Response.Headers
		if pctx.Response.Body != nil {
			r.ResponseBodyID = matched.NewID()
			respBody = pctx.Response.Body
		}
	}
	m.matchedBuf.Add(r, reqBody, respBody)
}
```

Then call `m.captureMatched(pctx)` inside the existing `if pctx.Matched != nil {`
branch of `Execute`, after `m.recorder.RecordRequest(...)`.

Add the two fields to the `Middleware` struct definition.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/metrics/ -v`
Expected: PASS (existing middleware tests unaffected — capture is nil by default).

- [ ] **Step 5: Commit**

```bash
git add internal/metrics/middleware.go internal/metrics/middleware_matched_test.go
git commit -m "feat(metrics): optional matched-request capture in middleware"
```

---

## Task 8: MatchedConfig resolution

**Files:**
- Create: `server/matched.go`
- Test: `server/matched_test.go`

- [ ] **Step 1: Write the failing test**

```go
package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResolveMatchedConfig_Defaults(t *testing.T) {
	c := resolveMatchedConfig(MatchedConfig{Enabled: true})
	assert.True(t, c.Enabled)
	assert.Equal(t, time.Hour, c.TTL)
	assert.Equal(t, 10000, c.BufferSize)
	assert.Equal(t, 30*time.Second, c.SyncInterval)
}

func TestResolveMatchedConfig_RespectsExplicit(t *testing.T) {
	in := MatchedConfig{Enabled: true, TTL: 5 * time.Minute, BufferSize: 50, SyncInterval: 2 * time.Second}
	c := resolveMatchedConfig(in)
	assert.Equal(t, 5*time.Minute, c.TTL)
	assert.Equal(t, 50, c.BufferSize)
	assert.Equal(t, 2*time.Second, c.SyncInterval)
}

func TestResolveMatchedConfig_EnvFallback(t *testing.T) {
	t.Setenv("MOCKWAVE_MATCHED_CAPTURE", "true")
	t.Setenv("MOCKWAVE_MATCHED_TTL", "120")
	t.Setenv("MOCKWAVE_MATCHED_BUFFER_SIZE", "7")
	t.Setenv("MOCKWAVE_MATCHED_SYNC_INTERVAL", "3")
	c := resolveMatchedConfig(MatchedConfig{})
	assert.True(t, c.Enabled)
	assert.Equal(t, 120*time.Second, c.TTL)
	assert.Equal(t, 7, c.BufferSize)
	assert.Equal(t, 3*time.Second, c.SyncInterval)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server/ -run TestResolveMatchedConfig -v`
Expected: FAIL — `MatchedConfig` / `resolveMatchedConfig` undefined.

- [ ] **Step 3: Write minimal implementation**

```go
package server

import (
	"os"
	"strconv"
	"time"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
)

// MatchedConfig configures matched-request capture. Disabled by default; when
// off there is zero capture overhead. Explicit non-zero fields win; otherwise
// env vars fill in; otherwise built-in defaults apply.
type MatchedConfig struct {
	Enabled      bool
	TTL          time.Duration       // global expiry; default 1h
	BufferSize   int                 // in-memory capacity; default 10000
	SyncInterval time.Duration       // write-behind cadence; default 30s
	Store        store.MatchedStore  // BYO override; nil → derived from backend
}

func resolveMatchedConfig(in MatchedConfig) MatchedConfig {
	out := in
	if !out.Enabled && os.Getenv("MOCKWAVE_MATCHED_CAPTURE") == "true" {
		out.Enabled = true
	}
	if out.TTL <= 0 {
		if v := envInt("MOCKWAVE_MATCHED_TTL", 0); v > 0 {
			out.TTL = time.Duration(v) * time.Second
		} else {
			out.TTL = time.Hour
		}
	}
	if out.BufferSize <= 0 {
		out.BufferSize = envInt("MOCKWAVE_MATCHED_BUFFER_SIZE", 10000)
	}
	if out.SyncInterval <= 0 {
		if v := envInt("MOCKWAVE_MATCHED_SYNC_INTERVAL", 0); v > 0 {
			out.SyncInterval = time.Duration(v) * time.Second
		} else {
			out.SyncInterval = 30 * time.Second
		}
	}
	return out
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// ttlSeconds returns the TTL as whole seconds for capture hints.
func (c MatchedConfig) ttlSeconds() int {
	return int(c.TTL / time.Second)
}

// matchedSink picks the MatchedStore: explicit override, else the backend if it
// implements store.MatchedStore, else nil (memory-only).
func matchedSink(cfg MatchedConfig, backend store.DataStore) matched.Sink {
	if cfg.Store != nil {
		return cfg.Store
	}
	if ms, ok := backend.(store.MatchedStore); ok {
		return ms
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./server/ -run TestResolveMatchedConfig -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/matched.go server/matched_test.go
git commit -m "feat(server): MatchedConfig resolution with env fallback"
```

---

## Task 9: Wire buffer + syncer lifecycle into Server

**Files:**
- Modify: `server/server.go`
- Test: `server/server_matched_test.go`

- [ ] **Step 1: Write the failing test**

```go
package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_MatchedBufferExposed(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		Rules: []domain.Rule{{
			ID:    "r1",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/ping"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: "simulate", SimulationID: "s1"}},
		}},
		Simulations: []domain.Simulation{{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200, Body: map[string]any{"ok": true}}}},
	})
	srv, err := server.New(server.Config{
		Store:   st,
		Matched: server.MatchedConfig{Enabled: true, BufferSize: 100, SyncInterval: time.Hour},
	})
	require.NoError(t, err)
	defer srv.Close()

	require.NotNil(t, srv.MatchedBuffer())

	// Drive one matched request through the proxy.
	h := srv.MockHandler([]string{"http"}, srv.NewProxy())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))
	assert.Equal(t, 200, rec.Code)

	page := srv.MatchedBuffer().List("r1", matched.Query{})
	require.Len(t, page.Items, 1)
	assert.Equal(t, "/ping", page.Items[0].Path)
}

func TestServer_MatchedDisabledByDefault(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{})
	srv, err := server.New(server.Config{Store: st})
	require.NoError(t, err)
	defer srv.Close()
	assert.Nil(t, srv.MatchedBuffer())
}

var _ = context.Background
```

If `Server` has no `Close` method yet, this task adds one. If `jsonfile.NewMemStore`
signature differs, match the real signature (read `internal/adapters/out/jsonfile/store.go:21`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server/ -run TestServer_Matched -v`
Expected: FAIL — `Config.Matched`, `MatchedBuffer`, `Close` undefined.

- [ ] **Step 3: Write minimal implementation**

In `server/server.go`:

1. Add to `Config`:
```go
	// Matched configures matched-request capture (default disabled).
	Matched MatchedConfig
```

2. Add to `Server` struct:
```go
	matchedBuf    *matched.Buffer // nil when capture disabled
	matchedSyncer *matched.Syncer // nil when no sink / disabled
	matchedSweep  context.CancelFunc
```

3. Add import `"github.com/mockwave/mockwave/internal/matched"`.

4. In `New`, after the server struct is built and `rebuild()` succeeds, before
   `startAdmin`:
```go
	if mc := resolveMatchedConfig(cfg.Matched); mc.Enabled {
		s.cfg.Matched = mc
		s.matchedBuf = matched.NewBuffer(mc.BufferSize)
		if sink := matchedSink(mc, s.cfg.Store); sink != nil {
			// Hydrate recent entries so queries survive restart.
			if ms, ok := sink.(store.MatchedStore); ok {
				if page, err := ms.ListMatched(context.Background(), "", store.MatchedQuery{Limit: mc.BufferSize}); err == nil {
					s.matchedBuf.Hydrate(page.Items)
				}
			}
			s.matchedSyncer = matched.NewSyncer(s.matchedBuf, sink, mc.SyncInterval)
			go s.matchedSyncer.Run(context.Background())
		} else {
			// memory-only: run a sweep loop so TTL still applies.
			sctx, scancel := context.WithCancel(context.Background())
			s.matchedSweep = scancel
			go s.runMatchedSweep(sctx, mc.SyncInterval)
		}
	}
```

Note: hydration with `ruleID == ""` requires the store's `ListMatched` to treat
an empty rule id as "all rules". The JSON impl from Task 5 keys by rule, so add
this branch to its `ListMatched`: when `ruleID == ""`, flatten all rules'
entries before sorting. Add a test for it in Task 5's file if not already
covered — update `jsonfile.ListMatched` accordingly:

```go
	var entries []matched.Request
	if ruleID == "" {
		for _, e := range m.byRule {
			entries = append(entries, e...)
		}
	} else {
		entries = append(entries, m.byRule[ruleID]...)
	}
```
(Replace the earlier `entries := make(...)` copy line with this.)

5. Add the sweep loop and accessors/Close:
```go
func (s *Server) runMatchedSweep(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.matchedBuf != nil {
				s.matchedBuf.SweepExpired()
			}
		}
	}
}

// MatchedBuffer returns the matched-capture buffer, or nil when capture is off.
func (s *Server) MatchedBuffer() *matched.Buffer { return s.matchedBuf }

// Close releases background resources: broker, reloader, and the matched syncer
// (with a final flush). Safe to call once.
func (s *Server) Close() error {
	if s.brokerCancel != nil {
		s.brokerCancel()
	}
	if s.reloadCancel != nil {
		s.reloadCancel()
	}
	if s.matchedSyncer != nil {
		_ = s.matchedSyncer.Close()
	}
	if s.matchedSweep != nil {
		s.matchedSweep()
	}
	return nil
}
```

If a `Close`/`Shutdown` method already exists, fold the matched cleanup into it
instead of adding a duplicate (read the file's existing shutdown path first).

6. In `NewProxy`, enable capture on the middleware when the buffer exists:
```go
func (s *Server) NewProxy() Executor {
	mw := metrics.NewMiddleware(
		&pipelineProxy{server: s},
		s.collector,
		s.buffer,
		s.cfg.Tracer,
		s.cfg.Metrics,
	)
	if s.matchedBuf != nil {
		mw.SetMatchedCapture(s.matchedBuf, s.cfg.Matched.ttlSeconds())
	}
	return mw
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./server/ -run TestServer_Matched -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/server.go server/server_matched_test.go internal/adapters/out/jsonfile/matched.go
git commit -m "feat(server): matched buffer + syncer lifecycle and hydration"
```

---

## Task 10: REST handlers — list, detail, delete

**Files:**
- Create: `internal/adapters/cfg/restapi/matched_handlers.go`
- Modify: `internal/adapters/cfg/restapi/server.go`
- Test: `internal/adapters/cfg/restapi/matched_handlers_test.go`

- [ ] **Step 1: Write the failing test**

```go
package restapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMatchedAPI(buf *matched.Buffer) *adminAPI {
	return &adminAPI{matchedBuf: buf}
}

func TestMatched_List(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "a", RuleID: "r1", Method: "GET", Path: "/x", At: time.Unix(1, 0)}, nil, nil)
	buf.Add(matched.Request{ID: "b", RuleID: "r1", Method: "POST", Path: "/x", At: time.Unix(2, 0)}, nil, nil)
	api := newMatchedAPI(buf)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/matched/r1", nil)
	api.matchedByRule(rec, req)
	require.Equal(t, 200, rec.Code)

	var page matched.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Items, 2)
	assert.Equal(t, "b", page.Items[0].ID)
}

func TestMatched_List_Filters(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "a", RuleID: "r1", Method: "GET", Path: "/x", At: time.Unix(1, 0)}, nil, nil)
	buf.Add(matched.Request{ID: "b", RuleID: "r1", Method: "POST", Path: "/x", Headers: map[string]string{"x-cid": "uuid-1"}, At: time.Unix(2, 0)}, nil, nil)
	api := newMatchedAPI(buf)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/matched/r1?method=POST&headers=x-cid:uuid-1", nil)
	api.matchedByRule(rec, req)
	require.Equal(t, 200, rec.Code)

	var page matched.Page
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Items, 1)
	assert.Equal(t, "b", page.Items[0].ID)
}

func TestMatched_Detail(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "a", RuleID: "r1", Method: "POST", Path: "/x", RequestBodyID: "rb", At: time.Unix(1, 0)}, []byte(`{"k":1}`), nil)
	api := newMatchedAPI(buf)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/matched/r1/a", nil)
	api.matchedByRule(rec, req)
	require.Equal(t, 200, rec.Code)

	var full matched.FullRequest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &full))
	assert.Equal(t, "a", full.ID)
	assert.JSONEq(t, `{"k":1}`, string(full.RequestBody))
}

func TestMatched_Detail_NotFound(t *testing.T) {
	api := newMatchedAPI(matched.NewBuffer(10))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/matched/r1/missing", nil)
	api.matchedByRule(rec, req)
	assert.Equal(t, 404, rec.Code)
}

func TestMatched_DeleteRule(t *testing.T) {
	buf := matched.NewBuffer(10)
	buf.Add(matched.Request{ID: "a", RuleID: "r1", At: time.Unix(1, 0)}, nil, nil)
	api := newMatchedAPI(buf)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/matched/r1", nil)
	api.matchedByRule(rec, req)
	assert.Equal(t, 204, rec.Code)
	assert.Empty(t, buf.List("r1", matched.Query{}).Items)
}

func TestMatched_Disabled(t *testing.T) {
	api := newMatchedAPI(nil) // capture off
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/matched/r1", nil)
	api.matchedByRule(rec, req)
	assert.Equal(t, 404, rec.Code) // feature off → treated as not found
}
```

This test is in package `restapi` (white-box) to construct `adminAPI` directly;
confirm `server_test.go` uses the same package, otherwise place the helper
accordingly. The `adminAPI` struct must gain a `matchedBuf *matched.Buffer`
field (Step 3).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/cfg/restapi/ -run TestMatched -v`
Expected: FAIL — `matchedBuf` field and `matchedByRule` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/adapters/cfg/restapi/matched_handlers.go`:

```go
package restapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mockwave/mockwave/internal/matched"
)

// matchedByRule routes /api/matched/{rule_id} and /api/matched/{rule_id}/{id}.
//   GET  /api/matched/{rule}        → paginated list (reduced)
//   GET  /api/matched/{rule}/{id}   → full detail (with bodies)
//   DELETE /api/matched/{rule}      → clear a rule's captures
//   DELETE /api/matched             → clear all
func (a *adminAPI) matchedByRule(w http.ResponseWriter, r *http.Request) {
	if a.matchedBuf == nil {
		writeError(w, 404, "matched capture is disabled")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/matched/")
	rest = strings.Trim(rest, "/")
	parts := []string{}
	if rest != "" {
		parts = strings.Split(rest, "/")
	}

	switch r.Method {
	case http.MethodGet:
		switch len(parts) {
		case 1:
			a.matchedList(w, r, parts[0])
		case 2:
			a.matchedDetail(w, parts[0], parts[1])
		default:
			writeError(w, 404, "not found")
		}
	case http.MethodDelete:
		ruleID := ""
		if len(parts) >= 1 {
			ruleID = parts[0]
		}
		a.matchedBuf.Clear(ruleID)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, 405, "method not allowed")
	}
}

func (a *adminAPI) matchedList(w http.ResponseWriter, r *http.Request, ruleID string) {
	q := r.URL.Query()
	mq := matched.Query{
		Cursor:  q.Get("cursor"),
		Method:  q.Get("method"),
		Path:    q.Get("path"),
		Headers: parseHeaderFilters(q["headers"]),
	}
	if l := q.Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			mq.Limit = n
		}
	}
	if s := q.Get("status"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			mq.Status = n
		}
	}
	page := a.matchedBuf.List(ruleID, mq)
	if page.Items == nil {
		page.Items = []matched.Request{}
	}
	// List response is reduced: drop bodies' presence is already implicit (no
	// body fields on Request); headers/query stay for filter transparency.
	writeJSON(w, 200, page)
}

func (a *adminAPI) matchedDetail(w http.ResponseWriter, ruleID, id string) {
	full, ok := a.matchedBuf.Get(ruleID, id)
	if !ok {
		writeError(w, 404, "matched request not found")
		return
	}
	writeJSON(w, 200, full)
}

// parseHeaderFilters turns repeated "key:value" params into a map. Values may
// contain ':' (only the first is the separator). Malformed entries are skipped.
func parseHeaderFilters(vals []string) map[string]string {
	if len(vals) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, v := range vals {
		i := strings.Index(v, ":")
		if i <= 0 {
			continue
		}
		out[v[:i]] = v[i+1:]
	}
	return out
}
```

In `internal/adapters/cfg/restapi/server.go`:

1. Add field to `adminAPI`:
```go
	matchedBuf *matched.Buffer // may be nil — capture disabled
```
2. Add import `"github.com/mockwave/mockwave/internal/matched"`.
3. Add MuxOption:
```go
// WithMatched wires the matched-request capture buffer into the admin API.
func WithMatched(buf *matched.Buffer) MuxOption {
	return func(a *adminAPI) { a.matchedBuf = buf }
}
```
4. Register routes in `NewMux`:
```go
	mux.HandleFunc("/api/matched", api.matchedByRule)
	mux.HandleFunc("/api/matched/", api.matchedByRule)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/cfg/restapi/ -run TestMatched -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/cfg/restapi/matched_handlers.go internal/adapters/cfg/restapi/server.go internal/adapters/cfg/restapi/matched_handlers_test.go
git commit -m "feat(restapi): matched request list/detail/delete endpoints"
```

---

## Task 11: Wire WithMatched into admin server startup

**Files:**
- Modify: `server/server.go` (the `startAdmin` path that calls `NewMux`)
- Test: covered by extending `server/server_matched_test.go`

- [ ] **Step 1: Write the failing test**

Add to `server/server_matched_test.go`:

```go
func TestServer_MatchedAdminEndpoint(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		Rules: []domain.Rule{{
			ID:    "r1",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/ping"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: "simulate", SimulationID: "s1"}},
		}},
		Simulations: []domain.Simulation{{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200, Body: map[string]any{"ok": true}}}},
	})
	srv, err := server.New(server.Config{
		Store:   st,
		Matched: server.MatchedConfig{Enabled: true, BufferSize: 100, SyncInterval: time.Hour},
	})
	require.NoError(t, err)
	defer srv.Close()

	// capture one request
	h := srv.MockHandler([]string{"http"}, srv.NewProxy())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ping", nil))

	// query through a mux built the same way startAdmin builds it
	mux := srv.AdminMux()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/matched/r1", nil))
	require.Equal(t, 200, rec.Code)
	assert.Contains(t, rec.Body.String(), `"/ping"`)
}
```

If the server has no test seam for building the admin mux, this task adds an
`AdminMux()` method (or, if `startAdmin` already builds the mux in a helper,
make that helper exported-for-test). Read the existing `startAdmin`
implementation first to find where `restapi.NewMux` is called and thread the
option through.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server/ -run TestServer_MatchedAdminEndpoint -v`
Expected: FAIL — option not wired / `AdminMux` undefined.

- [ ] **Step 3: Write minimal implementation**

Find the `restapi.NewMux(...)` call in `server/server.go` (inside `startAdmin`
or a mux-building helper). Append the matched option to its `opts`:

```go
	opts := []restapi.MuxOption{ /* existing options */ }
	if s.matchedBuf != nil {
		opts = append(opts, restapi.WithMatched(s.matchedBuf))
	}
	mux := restapi.NewMux(s.cfg.Store, s.Rebuild, s.collector, s.buffer, s.broker, s.engine, opts...)
```

Extract the mux construction into a method so the test can call it:

```go
// AdminMux builds the admin HTTP mux (exported primarily for tests; the live
// admin server uses the same construction path).
func (s *Server) AdminMux() http.Handler {
	opts := s.adminMuxOptions()
	return restapi.NewMux(s.cfg.Store, s.Rebuild, s.collector, s.buffer, s.broker, s.engine, opts...)
}
```

Refactor `startAdmin` to call `s.AdminMux()` (or a shared `adminMuxOptions()`
helper) so production and test share one path. Match the real `NewMux`
signature and the real existing option list (import/export, kill switch,
scenario control) — read `startAdmin` and replicate every option it already
passes; only ADD the matched option.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./server/ -run TestServer_Matched -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/server.go server/server_matched_test.go
git commit -m "feat(server): wire matched endpoints into admin mux"
```

---

## Task 12: CLI flags

**Files:**
- Modify: `cmd/mockwave/*.go` (the `start` command)
- Test: manual smoke (CLI flag plumbing is verified by build + run)

- [ ] **Step 1: Locate the start command flag definitions**

Run: `grep -rn "admin-port\|StringVar\|IntVar\|BoolVar\|server.Config{" cmd/mockwave/`
Expected: the file and function where `start` flags are registered and a
`server.Config{...}` (or `server.New`) is constructed.

- [ ] **Step 2: Add flag variables and registrations**

In the start command setup (match the existing flag style — `cobra` `Flags()`
or stdlib `flag`), add:

```go
	// matched-request capture
	matchedCapture := startCmd.Flags().Bool("matched-capture", false, "Enable matched request capture")
	matchedTTL := startCmd.Flags().Int("matched-ttl", 3600, "Matched capture global TTL seconds")
	matchedBufferSize := startCmd.Flags().Int("matched-buffer-size", 10000, "Matched capture in-memory ring capacity")
	matchedSyncInterval := startCmd.Flags().Int("matched-sync-interval", 30, "Matched capture write-behind sync seconds")
```

(If the command uses stdlib `flag`, use `flag.Bool`/`flag.Int` with the same
names and defaults.)

- [ ] **Step 3: Thread into server.Config**

Where `server.Config{...}` is built, add:

```go
		Matched: server.MatchedConfig{
			Enabled:      *matchedCapture,
			TTL:          time.Duration(*matchedTTL) * time.Second,
			BufferSize:   *matchedBufferSize,
			SyncInterval: time.Duration(*matchedSyncInterval) * time.Second,
		},
```

Ensure `time` is imported in that file.

- [ ] **Step 4: Build and smoke test**

Run:
```bash
go build ./... && \
go run ./cmd/mockwave start --help 2>&1 | grep matched
```
Expected: the four `--matched-*` flags appear in help output.

Then a live smoke (uses the Quick Start config from README):
```bash
cat > /tmp/mw.json <<'EOF'
{"rules":[{"id":"hello","name":"Hello","match":{"method":"GET","path":"/hello"},"buckets":[{"weight":100,"action":"simulate","simulation_id":"hello-sim"}]}],"simulations":[{"id":"hello-sim","protocol":"http","response":{"status":200,"body":{"message":"hi"}}}]}
EOF
go run ./cmd/mockwave start -f /tmp/mw.json --matched-capture --matched-sync-interval 60 &
sleep 2
curl -s -H 'x-cid: test-123' http://localhost:8080/hello >/dev/null
curl -s http://localhost:9090/api/matched/hello | head
curl -s http://localhost:9090/api/matched/hello | python3 -c 'import sys,json; d=json.load(sys.stdin); print("ID:", d["items"][0]["id"])'
kill %1
```
Expected: list returns one item with an `id`; detail by that id returns the
request with `x-cid: test-123` header.

- [ ] **Step 5: Commit**

```bash
git add cmd/mockwave
git commit -m "feat(cli): --matched-* flags for matched request capture"
```

---

## Task 13: E2E integration test (the regressive flow)

**Files:**
- Create: `tests/integration/matched_test.go`
- Reference: read an existing test in `tests/integration/` (e.g. `metrics_test.go`)
  for the server/handler bootstrapping helpers and import paths.

- [ ] **Step 1: Write the failing test**

```go
package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_MatchedRequestCapture exercises the regressive validation flow:
// create rule → drive a request at the mock → assert the mock's response AND
// assert the exact request the caller sent, retrieved via the admin API.
func TestE2E_MatchedRequestCapture(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		Rules: []domain.Rule{{
			ID:    "create-user",
			Name:  "Create User",
			Match: domain.MatchCriteria{Protocol: "http", Method: "POST", Path: "/users"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: "simulate", SimulationID: "user-sim"}},
		}},
		Simulations: []domain.Simulation{{
			ID: "user-sim", Protocol: "http",
			Response: domain.HTTPResponse{Status: 201, Body: map[string]any{"id": "u-1"}},
		}},
	})
	srv, err := server.New(server.Config{
		Store:   st,
		Matched: server.MatchedConfig{Enabled: true, BufferSize: 100, SyncInterval: time.Hour},
	})
	require.NoError(t, err)
	defer srv.Close()

	mock := httptest.NewServer(srv.MockHandler([]string{"http"}, srv.NewProxy()))
	defer mock.Close()
	admin := httptest.NewServer(srv.AdminMux())
	defer admin.Close()

	// --- caller (system under test) sends a request to the mock ---
	body := `{"name":"Ada","email":"ada@example.com"}`
	req, _ := http.NewRequest(http.MethodPost, mock.URL+"/users", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CID", "req-uuid-9")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// --- tester validates the mock's RESPONSE ---
	assert.Equal(t, 201, resp.StatusCode)

	// --- tester validates the REQUEST sent to the mock ---
	// List filtered by the unique correlation id header.
	listResp, err := http.Get(admin.URL + "/api/matched/create-user?headers=X-CID:req-uuid-9")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, 200, listResp.StatusCode)

	var page matched.Page
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&page))
	require.Len(t, page.Items, 1)
	id := page.Items[0].ID
	assert.Equal(t, "POST", page.Items[0].Method)
	assert.Equal(t, "/users", page.Items[0].Path)

	// Detail returns the full captured request body.
	detResp, err := http.Get(admin.URL + "/api/matched/create-user/" + id)
	require.NoError(t, err)
	defer detResp.Body.Close()
	require.Equal(t, 200, detResp.StatusCode)

	var full matched.FullRequest
	require.NoError(t, json.NewDecoder(detResp.Body).Decode(&full))
	assert.Equal(t, "req-uuid-9", headerCI(full.Headers, "x-cid"))
	assert.JSONEq(t, body, string(full.RequestBody))
}

func headerCI(h map[string]string, key string) string {
	for k, v := range h {
		if len(k) == len(key) && equalFoldASCII(k, key) {
			return v
		}
	}
	return ""
}

func equalFoldASCII(a, b string) bool {
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
```

If `srv.MockHandler`/`AdminMux`/`NewMemStore` signatures differ from the above,
adjust to match the real ones (read `server/server.go` and an existing
integration test). The captured `Headers` map's key casing depends on how the
HTTP adapter normalizes headers — `headerCI` tolerates either casing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./tests/integration/ -run TestE2E_MatchedRequestCapture -v`
Expected: FAIL initially if any wiring gap remains; otherwise it should pass
once Tasks 1–12 are in. If it fails on header casing, inspect the actual
captured headers and fix `headerCI` usage, not the production code.

- [ ] **Step 3: Make it pass**

No new production code expected — this validates the assembled feature. If the
body is empty in the capture, verify the HTTP adapter populates
`pctx.Request.Body` for POST (read `internal/adapters/in/httprest`); if the
adapter consumes the body before the pipeline, that is a real gap — capture the
body in the adapter and stamp it onto `NormalizedRequest.Body` (add a focused
unit test in the httprest package for that).

- [ ] **Step 4: Run the full suite**

Run: `go test ./...`
Expected: PASS across the module.

- [ ] **Step 5: Commit**

```bash
git add tests/integration/matched_test.go
git commit -m "test(integration): e2e matched request capture and retrieval"
```

---

## Task 14: Remote store impls (DynamoDB, MongoDB, Cosmos)

**Files:**
- Create: `internal/adapters/out/dynamodb/matched.go` + `_test.go`
- Create: `internal/adapters/out/mongodb/matched.go` + `_test.go`
- Create: `internal/adapters/out/cosmos/matched.go` + `_test.go`
- Reference: read each backend's existing rule/fault impl (e.g.
  `internal/adapters/out/dynamodb/store.go`) for client access, table/collection
  naming, marshalling helpers, and the integration-test gating pattern (most use
  a build tag or an env-guarded local endpoint — match it).

These mirror the JSON impl's semantics over the real backends. Each store sets
the native-TTL field on save and makes `SweepExpired` a no-op.

- [ ] **Step 1: DynamoDB — write the integration test (env-gated)**

Match the existing dynamo integration test gating (the repo already has
`tests` referencing a local dynamo endpoint — read
`internal/adapters/out/dynamodb/store_test.go` and the most recent
`test(dynamodb)` commit for the harness). Write `TestDynamoMatched_SaveListGet`
covering: SaveMatched two rows, ListMatched newest-first + cursor pagination,
GetMatched with body, DeleteMatched, and that the `ttl` attribute is set to
`At + globalTTL`. Use the same local-endpoint skip guard as the existing tests
so CI without dynamo skips cleanly.

- [ ] **Step 2: DynamoDB — implement**

`internal/adapters/out/dynamodb/matched.go`:

- New table (default `mockwave-matched-requests`, env
  `MOCKWAVE_DYNAMO_MATCHED_TABLE`) added to `dynamostore.Config` and the
  factory in `server/store.go` (mirror how `FaultsTable`/`ScenariosTable` are
  threaded).
- Item key: PK `rule_id` (S), SK `id` (S, UUID v7 — lexical = time order).
- Attributes: the `matched.Request` fields; bodies stored as separate items
  with PK `rule_id`, SK `body#<bodyID>` (so they live in the same partition and
  delete with the rule), or in a sibling table — choose same-partition for
  single-call cascade and document it.
- `ttl` Number attribute = `Request.TTL`; enable table TTL on `ttl` once
  (document in `docs/` and the table-creation helper).
- `SaveMatched`: `BatchWriteItem` for requests + bodies.
- `ListMatched`: `Query` on PK=ruleID, `ScanIndexForward=false` (newest first),
  `Limit`, `ExclusiveStartKey` from the decoded cursor; apply `q.Matches`
  filter client-side over the page, set `NextCursor` from the last evaluated
  key (encode the SK). For `ruleID == ""`, `Scan` with limit (hydration only).
- `GetMatched`: `GetItem` PK+SK, then fetch body items.
- `DeleteMatched`: `Query` keys then `BatchWriteItem` deletes (or `DeleteItem`
  per key); `ruleID == ""` → scan-and-delete.
- `SweepExpired`: `return 0, nil` (native TTL).

- [ ] **Step 3: DynamoDB — run test**

Run (with local dynamo, matching existing harness):
`go test ./internal/adapters/out/dynamodb/ -run TestDynamoMatched -v`
Expected: PASS (or SKIP when no endpoint).

- [ ] **Step 4: MongoDB — implement + test**

`internal/adapters/out/mongodb/matched.go`:
- Collection `matched_requests`; documents = `matched.Request` plus an
  `expireAt` BSON `time.Time` = `At + globalTTL`, and bodies in
  `matched_request_bodies` keyed by body id.
- On store init, ensure a TTL index: `createIndex({expireAt:1},
  {expireAfterSeconds:0})` (idempotent) and a compound index `{rule_id:1, id:-1}`.
- `ListMatched`: `find({rule_id})` sort `{id:-1}`, `limit`, cursor via
  `{id: {$lt: afterID}}`; client-side `q.Matches`; `NextCursor` from last id.
- `SweepExpired`: `return 0, nil`.
- Test mirrors the dynamo one, gated by the existing mongo test harness (read
  `internal/adapters/out/mongodb/*_test.go`).

- [ ] **Step 5: Cosmos — implement + test**

`internal/adapters/out/cosmos/matched.go`:
- Cosmos Mongo-API: same as the Mongo impl but set per-item `ttl` (seconds) and
  ensure the container/collection has `DefaultTimeToLive` enabled (or rely on
  per-item `ttl` with container TTL = -1). Cosmos Mongo-API also honours the
  `expireAt` TTL index pattern — prefer reusing the Mongo code path if the
  existing cosmos adapter already shares Mongo driver code (check
  `internal/adapters/out/cosmos/store.go`; the README says Cosmos uses the
  MongoDB API, so most logic is shared — factor a common helper rather than
  duplicating).
- `SweepExpired`: `return 0, nil`.
- Test gated by the existing cosmos harness.

- [ ] **Step 6: Thread tables/collections through the factory**

In `server/store.go` `buildStoreFromEnv`, add the matched table/collection env
defaults to each backend's `Config` (DynamoDB `MatchedTable`) the same way
faults/scenarios are threaded. Mongo/Cosmos derive collection names internally,
so no factory change beyond what already passes the db name.

- [ ] **Step 7: Run all backend tests**

Run: `go test ./internal/adapters/out/... -v`
Expected: PASS or SKIP (when a backend endpoint is unavailable).

- [ ] **Step 8: Commit**

```bash
git add internal/adapters/out/dynamodb/matched.go internal/adapters/out/dynamodb/matched_test.go \
        internal/adapters/out/mongodb/matched.go internal/adapters/out/mongodb/matched_test.go \
        internal/adapters/out/cosmos/matched.go internal/adapters/out/cosmos/matched_test.go \
        server/store.go
git commit -m "feat(stores): MatchedStore impls for dynamodb, mongodb, cosmos with native TTL"
```

---

## Task 15: Docs

**Files:**
- Modify: `README.md` (Features + a Matched Capture section + CLI Reference)
- Create: `docs/matched-capture.md`

- [ ] **Step 1: README feature bullet**

Add under Features:
```markdown
- **Matched request capture** — opt-in capture of requests that matched a rule, retrievable via `GET /api/matched/{rule_id}` (paginated, filterable by method/path/status/header) and `GET /api/matched/{rule_id}/{id}` (full request + bodies). Global TTL with native expiry on DynamoDB/Mongo/Cosmos. Enables regressive e2e: assert both the mock's response and the exact request your system sent.
```

- [ ] **Step 2: CLI Reference flags**

Add the four `--matched-*` flags to the `start` flags block in README, matching
the existing format.

- [ ] **Step 3: Write `docs/matched-capture.md`**

Cover: the e2e flow (the six-step flow from the spec), enabling capture
(CLI/env/Go `MatchedConfig`), the API (list filters incl. repeated
`headers=key:value`, cursor pagination, detail), TTL semantics per store
(native vs JSON sweep), the buffer-primary query model and its single-instance
read-consistency note, and the embeddable `store.MatchedStore` interface for
BYO backends. Use real `curl` examples mirroring the Task 12 smoke test.

- [ ] **Step 4: Verify links and build**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/matched-capture.md
git commit -m "docs: matched request capture guide, README features and CLI"
```

---

## Self-Review

- **Spec coverage:**
  - Capture matched requests → Tasks 1,3,7. ✓
  - Both memory + DB, write-behind → Tasks 3,5,6,9,14. ✓
  - Global TTL, native where available + JSON sweep + memory lazy/sweep → Tasks 1,3,5,6,9,14. ✓
  - CLI + env + Go-package config (interfaces) → Tasks 4,8,12; `MatchedConfig.Store` BYO → Task 8. ✓
  - `GET /api/matched/{rule}` paginated reduced with cursor (limit 20) → Tasks 2,10. ✓
  - Filters: method, path glob, status, repeated `headers=key:value` → Tasks 2,10. ✓
  - `GET /api/matched/{rule}/{id}` full body → Tasks 3,10. ✓
  - E2E regressive flow → Task 13. ✓
- **Placeholder scan:** No TBD/TODO; every code step shows complete code. Remote-store task (14) intentionally references existing harness patterns to read rather than reproducing unknown backend boilerplate — each sub-step states exact semantics, keys, and indexes.
- **Type consistency:** `matched.Request`/`FullRequest`/`Query`/`Page`, `store.MatchedStore` (with `MatchedQuery`/`MatchedPage` aliases), `Buffer` methods (`Add`/`List`/`Get`/`Clear`/`Drain`/`Hydrate`/`SweepExpired`), `Syncer` (`NewSyncer`/`Run`/`Close`), middleware `SetMatchedCapture`, server `MatchedConfig`/`MatchedBuffer`/`Close`/`AdminMux`, handler `matchedByRule` — names are consistent across all tasks.
```
