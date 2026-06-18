# Capture Field Filters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `body` (JSONPath) and `attr`/`query` field filters to the capture LIST endpoints (`/api/matched/{rule}` and `/api/event-captures/{rule}`), so a user can pinpoint a captured request/event by a body field (e.g. `correlation_id`) or an event attribute.

**Architecture:** Both endpoints read the shared in-memory `matched.Buffer` via `Buffer.List` and share `matched.Query`. Add a `Query` map filter (metadata, in `Query.Matches`) and a `Body` map filter (JSONPath → value, applied in `Buffer.List` against the out-of-line body, reusing `internal/domain/jsonpath`). Handlers parse the new params; the body filter is in-memory-buffer only (the list endpoint never hits the store).

**Tech Stack:** Go, `encoding/json`, `internal/domain/jsonpath`. Tests: `make test`; coverage ≥80%.

**Spec:** [`docs/specs/2026-06-18-capture-field-filters-design.md`](../specs/2026-06-18-capture-field-filters-design.md)

---

## Context (verified current code)

- `matched.Query` (`internal/matched/query.go`) has `Cursor, Limit, Method, Path, Status int, Headers map[string]string`. `Matches(r Request) bool` checks method/path/status/headers (headers via case-insensitive `headerLookup`).
- `Buffer.List` (`internal/matched/buffer.go`) iterates `ordered` newest-first, skips `r.Expired(now)` and `!q.Matches(r)`, paginates via `nextCursorLocked` (which ALSO checks `!r.Expired(now) && q.Matches(r)`). The buffer holds bodies in `b.reqB map[string][]byte` keyed by `r.RequestBodyID`.
- Event captures store attributes + metadata in `Request.Query` as `attr.<name>`, `source`, `detail_type`, `subject`, `group_id`, `dedup_id` (via `server.eventQuery`). HTTP captures store URL query params in `Request.Query`.
- `restapi/matched_handlers.go` `matchedList` parses `cursor/method/path/headers/limit/status` via `matched.Query{...}` + `parseHeaderFilters(q["headers"])` (splits each `key:value` on first `:`, skips `i<=0`).
- `restapi/event_handlers.go` `eventCaptureList` parses only `cursor/method/path/limit` (no status/headers).
- `internal/domain/jsonpath` exports `Resolve(root interface{}, expr string) (interface{}, bool)` and `LeafToString(v interface{}) string`.

## File Structure

- `internal/matched/query.go` — MODIFY: add `Query` + `Body` fields; add Query-map check to `Matches`.
- `internal/matched/buffer.go` — MODIFY: `matchesBody` helper; wire into `List` + `nextCursorLocked`.
- `internal/adapters/cfg/restapi/matched_handlers.go` — MODIFY: `parseKVFilters` + shared `parseCaptureQuery`; use in `matchedList`.
- `internal/adapters/cfg/restapi/event_handlers.go` — MODIFY: `eventCaptureList` uses `parseCaptureQuery` (gains status/headers/query/attr/body).
- `tests/integration/event_capture_test.go` — MODIFY: e2e body/attr filter test.
- `docs/event-capture.md`, `docs/matched-capture.md` — MODIFY: filter reference.

---

## Task 1: Query-map filter on matched.Query

**Files:**
- Modify: `internal/matched/query.go`
- Test: `internal/matched/query_test.go`

- [ ] **Step 1: Write the failing test.** Append to `internal/matched/query_test.go` (create if absent — package `matched`):

```go
func TestQueryMapFilter(t *testing.T) {
	r := Request{Query: map[string]string{"attr.correlation_id": "abc", "source": "billing"}}

	// Single match.
	if !(Query{Query: map[string]string{"attr.correlation_id": "abc"}}).Matches(r) {
		t.Fatal("expected match on attr.correlation_id")
	}
	// AND of two.
	if !(Query{Query: map[string]string{"attr.correlation_id": "abc", "source": "billing"}}).Matches(r) {
		t.Fatal("expected match on both")
	}
	// Value mismatch.
	if (Query{Query: map[string]string{"attr.correlation_id": "xyz"}}).Matches(r) {
		t.Fatal("value mismatch should not match")
	}
	// Missing key.
	if (Query{Query: map[string]string{"tenant": "acme"}}).Matches(r) {
		t.Fatal("missing key should not match")
	}
	// Case-sensitive key (unlike headers).
	if (Query{Query: map[string]string{"Source": "billing"}}).Matches(r) {
		t.Fatal("query keys are case-sensitive")
	}
	// Zero-value query matches all.
	if !(Query{}).Matches(r) {
		t.Fatal("zero-value query must match all")
	}
}
```

- [ ] **Step 2: Run test to verify it fails.**

Run: `go test ./internal/matched/ -run TestQueryMapFilter -v`
Expected: FAIL — `unknown field Query`.

- [ ] **Step 3: Implement.** In `internal/matched/query.go`, add two fields to the `Query` struct (after `Headers`):

```go
	Query map[string]string // NEW: matched against Request.Query, exact key (case-sensitive) + value
	Body  map[string]string // NEW: JSONPath expr → expected scalar value (applied in Buffer.List)
```

In `Matches`, add the Query-map check after the `Headers` loop (before `return true`):

```go
	for k, v := range q.Query {
		if r.Query[k] != v {
			return false
		}
	}
```

(`Body` is intentionally NOT checked in `Matches` — it needs the out-of-line body, applied in `Buffer.List`.)

- [ ] **Step 4: Run test to verify it passes.**

Run: `go test ./internal/matched/ -run TestQueryMapFilter -v` then `go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/matched/query.go internal/matched/query_test.go
git commit -m "feat(capture): query-map field filter on matched.Query"
```
No Co-Authored-By footer.

---

## Task 2: Body (JSONPath) filter in Buffer.List

**Files:**
- Modify: `internal/matched/buffer.go`
- Test: `internal/matched/buffer_test.go`

- [ ] **Step 1: Write the failing test.** Append to `internal/matched/buffer_test.go` (package `matched`):

```go
func TestListBodyFilter(t *testing.T) {
	b := NewBuffer(100)
	add := func(id, body string) {
		b.Add(Request{ID: id, RuleID: "r", Protocol: "aws-sns", RequestBodyID: id}, []byte(body), nil)
	}
	add("3", `{"correlation_id":"abc","total":42}`)
	add("2", `{"correlation_id":"xyz","total":7}`)
	add("1", `not json`)

	// Match a body field.
	page := b.List("r", Query{Body: map[string]string{"$.correlation_id": "abc"}})
	if len(page.Items) != 1 || page.Items[0].ID != "3" {
		t.Fatalf("body filter = %+v", page.Items)
	}
	// Nested + AND with another body field.
	if got := b.List("r", Query{Body: map[string]string{"$.correlation_id": "abc", "$.total": "42"}}); len(got.Items) != 1 {
		t.Fatalf("AND body filter = %+v", got.Items)
	}
	// Value mismatch → none.
	if got := b.List("r", Query{Body: map[string]string{"$.correlation_id": "nope"}}); len(got.Items) != 0 {
		t.Fatalf("mismatch should be empty, got %+v", got.Items)
	}
	// Missing path → none.
	if got := b.List("r", Query{Body: map[string]string{"$.missing": "x"}}); len(got.Items) != 0 {
		t.Fatalf("missing path should be empty")
	}
	// Non-JSON body never matches a body filter (e.g. id "1").
	if got := b.List("r", Query{Body: map[string]string{"$.correlation_id": "abc"}}); len(got.Items) != 1 || got.Items[0].ID != "3" {
		t.Fatalf("non-json must be skipped: %+v", got.Items)
	}
}

func TestListBodyFilterNoBody(t *testing.T) {
	b := NewBuffer(100)
	// Captured request with no body at all (RequestBodyID empty).
	b.Add(Request{ID: "1", RuleID: "r", Protocol: "http"}, nil, nil)
	if got := b.List("r", Query{Body: map[string]string{"$.x": "1"}}); len(got.Items) != 0 {
		t.Fatalf("no-body request must not match a body filter: %+v", got.Items)
	}
}
```

- [ ] **Step 2: Run test to verify it fails.**

Run: `go test ./internal/matched/ -run TestListBodyFilter -v`
Expected: FAIL — body filter not applied (item "3" plus others returned, or all returned).

- [ ] **Step 3: Implement.** In `internal/matched/buffer.go`:

Add imports `encoding/json` and `"github.com/mockwave/mockwave/internal/domain/jsonpath"` (no cycle — jsonpath has no `matched` dep).

Add the helper (place near `List`):

```go
// matchesBody reports whether r's request body satisfies every JSONPath filter
// in q.Body. Empty q.Body always matches. Buffer-only: resolves the out-of-line
// body from b.reqB; an absent or non-JSON body matches nothing. Call with b.mu held.
func (b *Buffer) matchesBody(r Request, q Query) bool {
	if len(q.Body) == 0 {
		return true
	}
	raw := b.reqB[r.RequestBodyID]
	if raw == nil {
		return false
	}
	var parsed interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return false
	}
	for expr, want := range q.Body {
		leaf, ok := jsonpath.Resolve(parsed, expr)
		if !ok || jsonpath.LeafToString(leaf) != want {
			return false
		}
	}
	return true
}
```

In `List`, add the body check right after the existing `if !q.Matches(r) { continue }`:

```go
		if !b.matchesBody(r, q) {
			continue
		}
```

In `nextCursorLocked`, extend the predicate so the cursor stays consistent with the body filter — change:

```go
		if !r.Expired(now) && q.Matches(r) {
			return EncodeCursor(lastID)
		}
```

to:

```go
		if !r.Expired(now) && q.Matches(r) && b.matchesBody(r, q) {
			return EncodeCursor(lastID)
		}
```

- [ ] **Step 4: Run tests to verify they pass.**

Run: `go test ./internal/matched/ -v` then `go build ./...`
Expected: PASS (new body-filter tests + all existing buffer/query tests).

- [ ] **Step 5: Commit.**

```bash
git add internal/matched/buffer.go internal/matched/buffer_test.go
git commit -m "feat(capture): body JSONPath filter in Buffer.List"
```
No Co-Authored-By footer.

---

## Task 3: Handler wiring (shared query parser, both endpoints)

**Files:**
- Modify: `internal/adapters/cfg/restapi/matched_handlers.go`
- Modify: `internal/adapters/cfg/restapi/event_handlers.go`
- Test: `internal/adapters/cfg/restapi/event_handlers_test.go` (and/or matched_handlers_test.go)

- [ ] **Step 1: Write the failing test.** Append to `internal/adapters/cfg/restapi/event_handlers_test.go`:

```go
func TestEventCaptureListBodyAndAttrFilter(t *testing.T) {
	buf := matched.NewBuffer(100)
	buf.Add(matched.Request{ID: "2", RuleID: "orders", At: time.Now(), Protocol: "aws-sns",
		Query: map[string]string{"attr.tenant": "acme"}, RequestBodyID: "2"},
		[]byte(`{"correlation_id":"abc"}`), nil)
	buf.Add(matched.Request{ID: "1", RuleID: "orders", At: time.Now(), Protocol: "aws-sns",
		Query: map[string]string{"attr.tenant": "other"}, RequestBodyID: "1"},
		[]byte(`{"correlation_id":"xyz"}`), nil)
	api := &adminAPI{eventCaptureBuf: buf}

	get := func(qs string) matched.Page {
		rec := httptest.NewRecorder()
		api.eventCaptures(rec, httptest.NewRequest("GET", "/api/event-captures/orders?"+qs, nil))
		var p matched.Page
		_ = json.NewDecoder(rec.Body).Decode(&p)
		return p
	}

	// Body filter.
	if p := get("body=$.correlation_id:abc"); len(p.Items) != 1 || p.Items[0].ID != "2" {
		t.Fatalf("body filter = %+v", p.Items)
	}
	// Attr filter (maps to attr.tenant).
	if p := get("attr=tenant:acme"); len(p.Items) != 1 || p.Items[0].ID != "2" {
		t.Fatalf("attr filter = %+v", p.Items)
	}
	// Combined, no match.
	if p := get("attr=tenant:acme&body=$.correlation_id:xyz"); len(p.Items) != 0 {
		t.Fatalf("combined should be empty: %+v", p.Items)
	}
}
```

(Ensure the test file imports `time`, `encoding/json`, `net/http/httptest`, `matched`.)

- [ ] **Step 2: Run test to verify it fails.**

Run: `go test ./internal/adapters/cfg/restapi/ -run TestEventCaptureListBodyAndAttrFilter -v`
Expected: FAIL — filters not parsed (both items returned).

- [ ] **Step 3: Implement.** In `internal/adapters/cfg/restapi/matched_handlers.go`:

Replace `parseHeaderFilters` with a generic `parseKVFilters` (same logic) and add a shared `parseCaptureQuery`:

```go
// parseKVFilters parses repeated "key:value" query params into a map, splitting
// each on the first ':'. Entries without a non-empty key are skipped.
func parseKVFilters(vals []string) map[string]string {
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

// parseCaptureQuery builds a matched.Query from the URL query params shared by
// the matched- and event-capture list endpoints.
func parseCaptureQuery(q url.Values) matched.Query {
	mq := matched.Query{
		Cursor:  q.Get("cursor"),
		Method:  q.Get("method"),
		Path:    q.Get("path"),
		Headers: parseKVFilters(q["headers"]),
		Query:   parseKVFilters(q["query"]),
		Body:    parseKVFilters(q["body"]),
	}
	for k, v := range parseKVFilters(q["attr"]) {
		if mq.Query == nil {
			mq.Query = map[string]string{}
		}
		mq.Query["attr."+k] = v
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
	return mq
}
```

Add `"net/url"` to the imports if not present. Delete the old `parseHeaderFilters` function (now replaced).

Rewrite `matchedList`'s body to use the shared parser:

```go
func (a *adminAPI) matchedList(w http.ResponseWriter, r *http.Request, ruleID string) {
	page := a.matchedBuf.List(ruleID, parseCaptureQuery(r.URL.Query()))
	if page.Items == nil {
		page.Items = []matched.Request{}
	}
	writeJSON(w, 200, page)
}
```

In `internal/adapters/cfg/restapi/event_handlers.go`, rewrite `eventCaptureList` the same way:

```go
func (a *adminAPI) eventCaptureList(w http.ResponseWriter, r *http.Request, ruleID string) {
	page := a.eventCaptureBuf.List(ruleID, parseCaptureQuery(r.URL.Query()))
	if page.Items == nil {
		page.Items = []matched.Request{}
	}
	writeJSON(w, 200, page)
}
```

(`event_handlers.go` may no longer need `strconv`/`net/url` imports directly — run goimports / `go build` and remove any now-unused imports from that file.)

- [ ] **Step 4: Run tests to verify they pass.**

Run: `go test ./internal/adapters/cfg/restapi/ -v` then `go build ./...`
Expected: PASS (new filter test + existing handler tests, including the matched-capture header-filter test which now routes through `parseKVFilters`).

- [ ] **Step 5: Commit.**

```bash
git add internal/adapters/cfg/restapi/matched_handlers.go internal/adapters/cfg/restapi/event_handlers.go internal/adapters/cfg/restapi/event_handlers_test.go
git commit -m "feat(capture): parse body/attr/query filters on both list endpoints"
```
No Co-Authored-By footer.

---

## Task 4: End-to-end body/attr filter (real SNS SDK)

**Files:**
- Modify: `tests/integration/event_capture_test.go`

- [ ] **Step 1: Write the test.** Append to `tests/integration/event_capture_test.go` (it already imports `context`, `encoding/json`, `net/http`, `net/http/httptest`, `testing`, `time`, `aws`, `awscfg`, `credentials`, `sns`, testify, `domain`, `jsonfile`, `matched`, `server`):

```go
func TestE2E_SNSEventCaptureBodyFilter(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		EventRules: []domain.EventRule{{
			ID:    "orders",
			Match: domain.EventMatch{Service: domain.EventServiceSNS},
		}},
	})
	srv, err := server.New(server.Config{
		Store: st,
		Event: server.EventConfig{Enabled: true, BufferSize: 100, SyncInterval: time.Hour},
	})
	require.NoError(t, err)
	defer srv.Close()

	mock := httptest.NewServer(srv.MockHandler([]string{"http", "aws"}, srv.NewProxy()))
	defer mock.Close()
	admin := httptest.NewServer(srv.AdminMux())
	defer admin.Close()

	cfg, err := awscfg.LoadDefaultConfig(context.Background(),
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "")),
	)
	require.NoError(t, err)
	client := sns.NewFromConfig(cfg, func(o *sns.Options) { o.BaseEndpoint = aws.String(mock.URL) })

	// Two events with different correlation ids in the body.
	for _, cid := range []string{"abc-123", "zzz-999"} {
		_, err := client.Publish(context.Background(), &sns.PublishInput{
			TopicArn: aws.String("arn:aws:sns:us-east-1:1:orders"),
			Message:  aws.String(`{"correlation_id":"` + cid + `","total":42}`),
		})
		require.NoError(t, err)
	}

	// Body filter returns exactly the one with correlation_id abc-123.
	resp, err := http.Get(admin.URL + "/api/event-captures/orders?body=$.correlation_id:abc-123")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	var page matched.Page
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	require.Len(t, page.Items, 1)

	// Confirm via detail it is the abc-123 event.
	det, err := http.Get(admin.URL + "/api/event-captures/orders/" + page.Items[0].ID)
	require.NoError(t, err)
	defer det.Body.Close()
	var full matched.FullRequest
	require.NoError(t, json.NewDecoder(det.Body).Decode(&full))
	assert.JSONEq(t, `{"correlation_id":"abc-123","total":42}`, string(full.RequestBody))

	// Unfiltered list returns both.
	all, err := http.Get(admin.URL + "/api/event-captures/orders")
	require.NoError(t, err)
	defer all.Body.Close()
	var allPage matched.Page
	require.NoError(t, json.NewDecoder(all.Body).Decode(&allPage))
	require.Len(t, allPage.Items, 2)
}
```

- [ ] **Step 2: Run it.**

Run: `go test ./tests/integration/ -run TestE2E_SNSEventCaptureBodyFilter -v`
Expected: PASS — the body filter narrows two published events to the one carrying `correlation_id=abc-123`.

- [ ] **Step 3: Full suite + coverage.**

Run: `make test && make coverage`
Expected: all pass; coverage ≥80%. Report the final %.

- [ ] **Step 4: Commit.**

```bash
git add tests/integration/event_capture_test.go
git commit -m "test(capture): e2e body-field filter narrows SNS captures by correlation_id"
```
No Co-Authored-By footer.

---

## Task 5: Docs

**Files:**
- Modify: `docs/event-capture.md`
- Modify: `docs/matched-capture.md`

- [ ] **Step 1: Update `docs/event-capture.md`.** In the admin-API / event-captures section, add a **Filtering captures** subsection documenting the query params on `GET /api/event-captures/{ruleID}` (and noting the same params work on `/api/matched/{ruleID}`):
  - `body=<jsonpath>:<value>` — match a field in the captured body (JSONPath leaf == value); repeatable, AND-combined; **in-memory buffer only** (recently captured); **JSON bodies only** (SNS Message / SQS body / EventBridge detail / HTTP JSON / GraphQL / gRPC — not SOAP/XML).
  - `attr=<name>:<value>` — match an event message attribute.
  - `query=<key>:<value>` — match a raw capture-query key (`source`, `detail_type`, `subject`, `group_id`, `dedup_id`, or HTTP URL param).
  - `method`/`path`/`status`/`headers`/`limit`/`cursor` — unchanged.
  - Give the worked example: publish with a `correlation_id` in the body, then `GET .../api/event-captures/orders?body=$.correlation_id:abc-123`.

- [ ] **Step 2: Update `docs/matched-capture.md`.** Add the same `body`/`query` filter params to the matched-capture list-endpoint docs (note `attr` is event-specific but `body` and `query` apply to HTTP captures too — e.g. `?body=$.email:ada@x.com`).

- [ ] **Step 3: Verify + commit.**

Run: `grep -n "body=\|attr=\|query=" docs/event-capture.md docs/matched-capture.md | head`

```bash
git add docs/event-capture.md docs/matched-capture.md
git commit -m "docs(capture): document body/attr/query capture filters"
```
No Co-Authored-By footer.

---

## Self-Review

**Spec coverage:**
- Query-map (attribute/metadata) filter via `Query.Matches` → Task 1. ✓
- Body JSONPath filter in `Buffer.List` (buffer-only, reuses jsonpath) → Task 2. ✓
- Cursor consistency with the body filter (`nextCursorLocked`) → Task 2. ✓
- Both endpoints (matched + event), shared parser; event handler gains status/headers parity → Task 3. ✓
- Query-string syntax (`body`/`attr`/`query`, first-colon split, attr→`attr.` prefix) → Task 3. ✓
- e2e: narrow by `correlation_id` → Task 4. ✓
- Docs (both guides, JSON-only + buffer-only caveats) → Task 5. ✓
- Out of scope (store-backed body filter, SOAP/XML, operators) — not implemented; noted in docs. ✓

**Type consistency:** `matched.Query.Query`/`Query.Body` (both `map[string]string`); `Buffer.matchesBody(Request, Query) bool`; `parseKVFilters([]string) map[string]string`; `parseCaptureQuery(url.Values) matched.Query`; `jsonpath.Resolve`/`LeafToString`. Names used identically across tasks. Task 3 deletes `parseHeaderFilters` and replaces both call sites with `parseCaptureQuery`/`parseKVFilters` — the existing matched header-filter test still passes through the renamed helper.

**Placeholder scan:** every code step is complete; the only "remove now-unused imports" note (Task 3) is a real mechanical instruction (goimports/`go build` will flag them), not a missing implementation.
