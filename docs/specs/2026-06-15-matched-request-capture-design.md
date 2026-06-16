# Matched Request Capture — Design

**Date:** 2026-06-15
**Status:** Approved pending review

## Goal

Capture requests that **match** a rule so testers can later retrieve, via a GET
API, the exact request a system-under-test sent to the mock. This enables
regressive e2e validation: a test creates rules, points API X at the mock,
drives traffic through API X, then asserts both (a) the response API X returned
and (b) the request API X actually sent to the mock.

E2E test flow this serves:
1. create rules
2. repoint API X at the mock
3. send request to API X
4. API X calls the mock
5. tester validates API X's returned result
6. tester validates the request sent to the mock

## Scope

- Capture is **opt-in** (default off, zero overhead when disabled).
- Captured data lives in **both** an in-memory ring buffer and the configured
  store (DynamoDB / MongoDB / Cosmos / JSON), synced **write-behind**.
- **Global TTL** expiration (no per-rule TTL). Defaults to 1h.
- Capture is **best-effort**: it must never block or fail a request.

## Architecture

```
Request hits rule → PipelineContext.Matched populated
                 ↓
         CaptureMatchedStage (after simulation/forward — needs final response)
         - extracts reduced request + response metadata
         - generates ULID (time-ordered, good for cursor)
         - separates bodies into RequestBodyID / ResponseBodyID
         - Add to matched.Buffer (non-blocking, ring overwrite when full)
                 ↓
         matched.Buffer (in-memory ring, thread-safe)
                 ↓
         WriteBehindSyncer (goroutine, ticks every SyncInterval)
         - drains buffer → SaveMatched(batch)
         - on error: log, keep in buffer for next tick
         - sets TTL / expireAt = at + globalTTL on save
                 ↓
         Store (DynamoDB / Cosmos / Mongo / JSON)
         - native TTL auto-cleanup where available; JSON uses sweep job
```

### Components

| Unit | Responsibility |
|---|---|
| `internal/matched/buffer.go` | In-memory ring buffer (mirrors `internal/unmatched`); thread-safe; lazy-expire on List. |
| `internal/matched/syncer.go` | Write-behind goroutine: periodic flush, retry-on-error, graceful flush on Close. |
| `internal/domain/pipeline/capture_stage.go` | Capture stage; runs only when `Enabled && pctx.Matched != nil`. |
| `internal/adapters/out/{dynamodb,mongodb,cosmos,jsonfile}/matched.go` | Per-store SaveMatched/ListMatched/GetMatched/DeleteMatched/SweepExpired. |
| `internal/adapters/cfg/restapi/matched_handlers.go` | `/api/matched/*` endpoints. |
| `store/store.go` | New `MatchedStore` interface + query/page types. |
| `server` | `MatchedConfig` wiring, CLI/env parsing, syncer lifecycle. |

## Data Model

```go
// matched.Request — reduced shape stored in memory and persisted.
type Request struct {
	ID       string            `json:"id"`        // ULID, time-ordered
	RuleID   string            `json:"rule_id"`   // which rule matched
	At       time.Time         `json:"at"`        // capture timestamp

	// Request (reduced — no body inline)
	Protocol string            `json:"protocol"`  // http|graphql|soap|grpc
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Headers  map[string]string `json:"headers"`
	Query    map[string]string `json:"query"`

	// Response (reduced — no body inline)
	ResponseStatus  int               `json:"response_status"`
	ResponseHeaders map[string]string `json:"response_headers"`

	// Bodies stored separately for lazy load (keeps list responses small)
	RequestBodyID  string `json:"request_body_id,omitempty"`
	ResponseBodyID string `json:"response_body_id,omitempty"`

	// Native-TTL hint (epoch seconds = at + globalTTL). Store-only.
	TTL int64 `json:"ttl,omitempty"`
}

// FullRequest is Request plus resolved bodies, returned by the detail endpoint.
type FullRequest struct {
	Request
	RequestBody  []byte      `json:"request_body,omitempty"`
	ResponseBody interface{} `json:"response_body,omitempty"`
}
```

Body records persisted alongside, keyed by their `*BodyID`, so list queries
never drag large payloads.

**DynamoDB schema (`mockwave-matched-requests`):**
- PK: `rule_id` (partition), SK: `id` (sort, ULID = time order)
- TTL attribute: `ttl`
- List by rule scans partition descending by SK; cursor = last SK.

## API

### List (paginated, reduced)

```
GET /api/matched/{rule_id}?cursor=...&limit=20&method=POST&path=/users*&status=200&headers=x-cid:uuid-123&headers=x-foo:bar
```

- `limit` default **20**, max 100.
- `cursor` opaque (base64 of last `at`+`id`).
- Filters applied after fetch: `method`, `path` (glob), `status`,
  `headers` (repeated `key:value`, AND-matched, exact value).
- Response omits headers/bodies — metadata only.

Response `200`:
```json
{
  "items": [
    {
      "id": "01J8X...",
      "rule_id": "rule-1234",
      "at": "2026-06-15T10:00:00Z",
      "protocol": "http",
      "method": "POST",
      "path": "/api/users",
      "response_status": 201
    }
  ],
  "next_cursor": "eyJhdCI6..."
}
```

### Detail (full, with bodies)

```
GET /api/matched/{rule_id}/{id}
```

Returns `FullRequest` with headers, query, and lazily-resolved
`request_body` / `response_body`. `404` if rule_id/id missing or expired.
Body lazy-load failure returns the request without that body plus a warning
field.

### Clear (testing convenience)

```
DELETE /api/matched/{rule_id}   # clear one rule's captures
DELETE /api/matched             # clear all
```

## Configuration & Interfaces

### CLI flags (`start`)

```
--matched-capture            Enable capture (default false)
--matched-ttl 3600           Global TTL seconds (default 3600 = 1h)
--matched-buffer-size 10000  In-memory ring capacity (default 10000)
--matched-sync-interval 30   Write-behind sync seconds (default 30)
```

### Env vars

```
MOCKWAVE_MATCHED_CAPTURE=true
MOCKWAVE_MATCHED_TTL=3600
MOCKWAVE_MATCHED_BUFFER_SIZE=10000
MOCKWAVE_MATCHED_SYNC_INTERVAL=30
```

### Go interface (embeddable lib)

```go
// MatchedStore persists matched requests. Implementations use native TTL where
// available (DynamoDB TTL, Cosmos/Mongo TTL index); JSON uses a background sweep.
type MatchedStore interface {
	SaveMatched(ctx context.Context, reqs []matched.Request) error
	ListMatched(ctx context.Context, ruleID string, q MatchedQuery) (MatchedPage, error)
	GetMatched(ctx context.Context, ruleID, id string) (*matched.FullRequest, error)
	DeleteMatched(ctx context.Context, ruleID string) error // "" = all
	SweepExpired(ctx context.Context, before time.Time) (int, error) // native = no-op
}

type MatchedQuery struct {
	Cursor  string
	Limit   int
	Method  string
	Path    string            // glob
	Status  int
	Headers map[string]string // AND match, exact value
}

type MatchedPage struct {
	Items      []matched.Request
	NextCursor string
}

// MatchedConfig allows programmatic override when embedding.
type MatchedConfig struct {
	Enabled      bool
	TTL          time.Duration
	BufferSize   int
	SyncInterval time.Duration
	Store        MatchedStore // BYO override; nil = derived from store backend
}
```

`server.Config` gains a `Matched MatchedConfig` field. Disabled by default.

## TTL — hybrid smart (native where available)

| Store | Mechanism | Setup |
|---|---|---|
| DynamoDB | TTL attribute `ttl` (epoch) | enable TTL on table once; `SweepExpired` no-op |
| Cosmos | container `DefaultTimeToLive` or per-item `ttl` | set on container; `SweepExpired` no-op |
| MongoDB | TTL index on `expireAt` | `createIndex({expireAt:1},{expireAfterSeconds:0})`; no-op sweep |
| JSON | none native | background sweep each `SyncInterval`, removes `at+TTL < now` |

Each store sets `TTL`/`expireAt` = `at + globalTTL` on save. Backend chosen at
factory time; the interface is uniform, the impl differs.

**Memory expiration:** `Buffer.List` skips entries where `at+TTL < now`
(lazy delete); the syncer tick also sweeps expired entries from the ring.

## Error Handling

- Capture failure → log via `observability.Logger`; request proceeds normally.
- Store save failure → retry next tick; eventual ring overwrite (lost data
  acceptable — capture is best-effort).
- Query store failure → `500` with message.
- Body lazy-load failure → return request without that body + warning field.

## Testing

- `matched/buffer_test.go` — ring, lazy expire, concurrent access.
- `matched/syncer_test.go` — tick flush, shutdown flush, retry on error.
- `pipeline/capture_stage_test.go` — capture only when enabled + matched.
- `restapi/matched_handlers_test.go` — list pagination, filters, detail, 404.
- Per-store integration: `{dynamodb,mongodb,cosmos,jsonfile}/matched_test.go` —
  save/list/get/delete/TTL.
- E2E: `tests/integration/matched_test.go` — rule → request → validate response
  + validate captured request via API (the regressive flow above).
