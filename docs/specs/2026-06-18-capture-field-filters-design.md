# Capture Field Filters — Body (JSONPath) + Attribute/Query Filtering

**Date:** 2026-06-18
**Status:** Approved (design)
**Scope:** Add body-field (JSONPath) and attribute/query filtering to the capture LIST endpoints (`GET /api/matched/{rule}` and `GET /api/event-captures/{rule}`), across all captured protocols. Touches: `internal/matched/query.go`, `internal/matched/buffer.go`, `internal/adapters/cfg/restapi/matched_handlers.go`, `internal/adapters/cfg/restapi/event_handlers.go`, docs.

## Goal

Let a user pinpoint a captured request/event by a field inside its body (e.g. `correlation_id`) or by an event message attribute, directly from the list endpoint — instead of listing everything and scanning client-side. This makes assert-after-publish e2e flows precise: publish an SNS/SQS/EventBridge event (or send an HTTP request) carrying a correlation id, then `GET …?body=$.correlation_id:abc-123` to retrieve exactly that capture.

## Decisions (from brainstorming)

- **Two filters, one shared query type.** Both `/api/matched` (HTTP/GraphQL/SOAP/gRPC) and `/api/event-captures` (aws-sns/sqs/eventbridge) read the same in-memory `matched.Buffer` and share `matched.Query`, so both endpoints gain both filters with one change.
- **Query/attribute filter is metadata-level** — matches the capture's `Query` map (cheap, AND-matched, exact value). For events the `Query` map already holds attributes as `attr.<name>` plus `source`/`detail_type`/`subject`/`group_id`/`dedup_id` (via `server.eventQuery`); for HTTP it holds URL query params.
- **Body filter is JSONPath → value**, reusing the shared `internal/domain/jsonpath` engine (the same one the event-rule matcher uses). Applied in `Buffer.List` by resolving the out-of-line request body.
- **Body filter is in-memory-buffer only.** The admin LIST endpoint is already buffer-only (it calls `Buffer.List`, never the store). Bodies live in the buffer (`b.reqB`); in cloud stores they're out-of-line. This matches the assert-right-after-publish use case (the capture you just made is in the buffer, newest-first). Store-backed body filtering is explicitly out of scope (roadmap).
- **JSON bodies only** for the body filter: SNS Message / SQS body / EventBridge detail / HTTP JSON / GraphQL / gRPC-proto-JSON. SOAP (XML) bodies will not match a JSONPath filter (documented caveat — a non-JSON body simply matches nothing).
- **AND semantics.** All filters (`method`/`path`/`status`/`headers`/`query`/`attr`/`body`) AND-combine. A zero-value query matches everything (unchanged).

## Architecture

### `matched.Query` (internal/matched/query.go)

Two new fields:

```go
type Query struct {
	Cursor  string
	Limit   int
	Method  string
	Path    string
	Status  int
	Headers map[string]string // existing: case-insensitive key, exact value
	Query   map[string]string // NEW: exact key (case-sensitive), exact value — matched against Request.Query
	Body    map[string]string // NEW: JSONPath expr → expected scalar value
}
```

`Query.Matches(r Request)` gains the `Query`-map check, mirroring the existing `Headers` loop but with **exact (case-sensitive) key lookup** (attribute/source/param keys are case-sensitive):

```go
for k, v := range q.Query {
	if r.Query[k] != v {
		return false
	}
}
```

`Matches` does **not** handle `Body` — it operates on a `Request` (metadata only, no body). The body filter is applied separately where the body is available.

### Body filter in `Buffer.List` (internal/matched/buffer.go)

After a candidate passes `q.Matches(r)`, apply the body filter when `len(q.Body) > 0`:

1. Resolve the out-of-line body: `body := b.reqB[r.RequestBodyID]` (the buffer holds it). Absent body → no match.
2. `json.Unmarshal(body, &parsed)` — non-JSON (e.g. SOAP XML) → no match.
3. For each `expr → want` in `q.Body`: `leaf, ok := jsonpath.Resolve(parsed, expr)`; require `ok && jsonpath.LeafToString(leaf) == want`. Any miss → skip the candidate.

This is a small helper (e.g. `matchesBody(b, r, q)`) invoked inside the existing `List` loop, under the buffer lock (the body maps are buffer-internal). Imports added to `buffer.go`: `encoding/json` and `internal/domain/jsonpath` (no import cycle — `jsonpath` has no deps on `matched`).

The cursor/pagination logic is unchanged: the body filter just skips non-matching candidates exactly like `q.Matches` does.

### Store `ListMatched` (no change)

The admin LIST is buffer-only, so no backend change is required. The new `Query`-map filter lives in `Query.Matches`, which the stores already call when post-filtering, so they pick it up for free; the `Body` filter is buffer-only by design and stores ignore it (they never resolve out-of-line bodies during a list). Hydration and the direct-store e2e use empty `Query`/`Body`, so nothing regresses.

### Handlers (restapi)

Add a generic colon-split helper (generalize the existing `parseHeaderFilters`, which splits each `key:value` on the first `:`):

```go
// parseKVFilters parses repeated "key:value" query params into a map (first colon splits).
func parseKVFilters(vals []string) map[string]string { /* same logic as parseHeaderFilters */ }
```

**`matchedList`** (matched_handlers.go) already parses `method`/`path`/`status`/`headers`/`limit`/`cursor`. Add:

```go
mq.Query = parseKVFilters(q["query"])
for k, v := range parseKVFilters(q["attr"]) { // attr.<name> convenience
	if mq.Query == nil { mq.Query = map[string]string{} }
	mq.Query["attr."+k] = v
}
mq.Body = parseKVFilters(q["body"])
```

**`eventCaptureList`** (event_handlers.go) currently parses only `cursor`/`method`/`path`/`limit`. Bring it to full parity: also parse `status`, `headers`, `query`, `attr`, `body` (same code as `matchedList`). This closes the pre-existing gap where event captures couldn't be filtered by status/headers.

`?body=$.x:v` splits on the first `:`; JSONPath expressions contain `.`/`$`/digits but no `:`, so a value containing `:` after the path is preserved. `attr` maps `name → attr.name`; `query` uses verbatim keys.

## Query-string reference (both endpoints)

| Param | Meaning | Example |
|-------|---------|---------|
| `body` | JSONPath leaf == value (buffer-only, JSON bodies) | `?body=$.correlation_id:abc-123` |
| `attr` | event message attribute == value | `?attr=tenant:acme` |
| `query` | raw Query-map key (`source`,`detail_type`,`subject`,`group_id`,`dedup_id`, or HTTP URL param) == value | `?query=source:billing` |
| `method`/`path`/`status`/`headers`/`limit`/`cursor` | unchanged | — |

All repeatable and AND-combined.

## Error handling

- Malformed `key:value` (no colon, or empty key) is skipped (consistent with the current header-filter behavior — lenient parsing, not a 400).
- A `body` filter whose JSONPath doesn't resolve, or whose capture body is absent/non-JSON, simply matches nothing for that capture (no error).
- Body filter never reaches the store; an evicted capture is not body-filterable (returns no match), which is the documented buffer-only behavior.

## Testing

- **`query.go` unit:** `Matches` with `Query`-map filter — single/multiple AND, key mismatch, value mismatch, case-sensitive key; zero-value still matches all.
- **`buffer.go` unit:** `List` with `Body` filter — JSONPath match, value mismatch, missing path, non-JSON body (skips), absent body, multiple AND; combined with `Query` filter; pagination/cursor still correct when the body filter narrows results.
- **Handler unit (both endpoints):** `matchedList`/`eventCaptureList` parse `body`/`attr`/`query` (and the new `status`/`headers` on the event handler) and return only matching captures; malformed filter skipped.
- **e2e (integration):** publish two SNS events with different `correlation_id` in the body; `GET /api/event-captures/{rule}?body=$.correlation_id:<id>` returns exactly the matching one; `?attr=<name>:<val>` likewise.
- `make test` green; coverage gate ≥80%.

## Out of scope (roadmap)

- Store-backed body filtering (DynamoDB/Mongo/Cosmos) — would require out-of-line body fetches per page or a queryable body projection per backend.
- Body filtering on SOAP/XML bodies (would need an XPath engine).
- Non-equality operators (ranges, contains, regex) — current matcher is exact-scalar-equality only, consistent with the event-rule matcher.
