# Matched Request Capture

Mockwave can record every request that matched a rule so you can retrieve
and inspect it later via the admin API. This is the building block for
**regressive e2e testing**: after your system-under-test completes a flow,
assert not just the response it returned, but also the exact request it sent
to the mock.

Capture is **opt-in** (default off, zero overhead when disabled).

- [Why this exists](#why-this-exists)
- [Enabling capture](#enabling-capture)
- [API](#api)
- [Pagination](#pagination)
- [Filters](#filters)
- [TTL and expiry](#ttl-and-expiry)
- [Query model and consistency](#query-model-and-consistency)
- [Custom store backends](#custom-store-backends)
- [Limitations](#limitations)

---

## Why this exists

The regressive e2e flow that matched capture enables:

1. Create rules in Mockwave.
2. Point your API (system-under-test) at the mock instead of the real dependency.
3. Send a request to your API.
4. Your API calls the mock; the mock records the call.
5. Assert the response your API returned to its caller.
6. Assert the request your API sent to the mock — method, path, headers, body,
   query params — via `GET /api/matched/{rule_id}`.

Without step 6 you only know your API returned the right response given a mocked
dependency. With it you also know it called the dependency correctly.

---

## Enabling capture

### CLI

Add `--matched-capture` to the `mockwave start` command. The other flags are
optional; defaults are shown.

```bash
mockwave start -f config.json \
  --matched-capture \
  --matched-ttl 3600 \
  --matched-buffer-size 10000 \
  --matched-sync-interval 30
```

### Environment variables

```bash
MOCKWAVE_MATCHED_CAPTURE=true
MOCKWAVE_MATCHED_TTL=3600
MOCKWAVE_MATCHED_BUFFER_SIZE=10000
MOCKWAVE_MATCHED_SYNC_INTERVAL=30
```

### Go embedding

```go
import "github.com/lfdubiela/mockwave/server"

srv, err := server.New(server.Config{
    // ... other config ...
    Matched: server.MatchedConfig{
        Enabled:      true,
        TTL:          time.Hour,
        BufferSize:   10000,
        SyncInterval: 30 * time.Second,
        Store:        nil, // nil = derived from the configured store backend
    },
})
```

Set `Store` to a custom `store.MatchedStore` implementation to use a different
backend than the one Mockwave is configured with (see
[Custom store backends](#custom-store-backends)).

---

## API

When capture is disabled, all `/api/matched` endpoints return `404`.

The mock port below is `:8080` and the admin port is `:9090`.

### Smoke-test walkthrough

```bash
# 1. Send a request that matches the "hello" rule (mock port)
curl -s -H 'x-cid: test-123' http://localhost:8080/hello

# 2. List captures for that rule (paginated, reduced — no bodies)
curl -s http://localhost:9090/api/matched/hello

# 3. Fetch full detail for one capture (headers, query, request_body, response_body)
curl -s http://localhost:9090/api/matched/hello/<id>

# 4. Filter by header
curl -s 'http://localhost:9090/api/matched/hello?headers=x-cid:test-123&method=GET'
```

### `GET /api/matched/{rule_id}` — list (paginated, reduced)

Returns captures for the rule, newest first. Items in the list response are
reduced: no request or response bodies, no full header maps — just the
metadata needed to identify and filter.

**Query parameters:**

| Param | Default | Description |
|---|---|---|
| `limit` | `20` | Max items per page (max 100). |
| `cursor` | — | Opaque pagination cursor from a previous response. |
| `method` | — | Exact HTTP method filter (`GET`, `POST`, …). |
| `path` | — | Glob match against the captured path (e.g. `/users/*`). |
| `status` | — | Exact response status code filter. |
| `headers` | — | `key:value`; repeat for AND-matching (see [Filters](#filters)). |
| `body` | — | `<jsonpath>:<value>` — match a field in the request body. Repeatable (AND). See [Filters](#filters). |
| `query` | — | `<key>:<value>` — match a URL query parameter from the captured request. Repeatable (AND). |

**Response `200`:**

```json
{
  "items": [
    {
      "id": "01J8XKZP3RQVWN1G2H5M7T9E00",
      "rule_id": "hello",
      "at": "2026-06-15T10:00:00Z",
      "protocol": "http",
      "method": "GET",
      "path": "/hello",
      "response_status": 200
    }
  ],
  "next_cursor": "eyJhdCI6IjIwMjYtMDYtMTV..."
}
```

`next_cursor` is absent (or empty) when there are no more pages.

### `GET /api/matched/{rule_id}/{id}` — full detail

Returns the full capture including headers, query params, request body, and
response body. `404` if the id does not exist or has expired.

```json
{
  "id": "01J8XKZP3RQVWN1G2H5M7T9E00",
  "rule_id": "hello",
  "at": "2026-06-15T10:00:00Z",
  "protocol": "http",
  "method": "GET",
  "path": "/hello",
  "headers": { "x-cid": "test-123", "user-agent": "curl/8.4.0" },
  "query": {},
  "response_status": 200,
  "response_headers": { "content-type": "application/json" },
  "request_body": null,
  "response_body": { "message": "Hello from Mockwave!" }
}
```

If a body cannot be resolved (store read failure), the field is omitted and a
`warning` field is added to the response.

### `DELETE /api/matched/{rule_id}` — clear one rule's captures

Returns `204`. Removes all captures for the rule from the buffer and the store.

### `DELETE /api/matched` — clear all captures

Returns `204`. Removes all captures across all rules.

---

## Pagination

Captures are returned newest first. The cursor is opaque (base64-encoded
`at`+`id`) — do not construct or parse it; just echo it back as `cursor=` on
the next request.

```bash
# Page 1
curl -s 'http://localhost:9090/api/matched/hello?limit=5'
# → {"items":[...],"next_cursor":"eyJh..."}

# Page 2
curl -s 'http://localhost:9090/api/matched/hello?limit=5&cursor=eyJh...'
```

When `next_cursor` is absent there are no more results.

---

## Filters

All filters are applied server-side after the page is fetched from the buffer.

**`method`** — exact match, case-insensitive:

```bash
curl -s 'http://localhost:9090/api/matched/hello?method=GET'
```

**`path`** — glob:

```bash
curl -s 'http://localhost:9090/api/matched/orders?path=/orders/*'
```

**`status`** — exact response status:

```bash
curl -s 'http://localhost:9090/api/matched/hello?status=200'
```

**`headers`** — `key:value`, repeated for AND matching. Keys are
case-insensitive; values are exact:

```bash
# Single header
curl -s 'http://localhost:9090/api/matched/hello?headers=x-cid:test-123'

# AND — both headers must match
curl -s 'http://localhost:9090/api/matched/hello?headers=x-cid:test-123&headers=x-tenant:acme'
```

**`body`** — `<jsonpath>:<value>`, repeated for AND matching. Filters by a
field inside the captured HTTP request body using a JSONPath expression. The
split is on the **first** `:`, so values may contain colons (e.g.
`body=$.url:http://example.com`).

```bash
# Match captures where the JSON body has email == "ada@x.com"
curl -s 'http://localhost:9090/api/matched/signup?body=$.email:ada@x.com'

# AND — both fields must match
curl -s 'http://localhost:9090/api/matched/orders?body=$.status:pending&body=$.region:us-east-1'
```

Constraints:

- **In-memory buffer only.** Body filtering reads from the in-memory ring
  buffer. Captures evicted to the cloud store are not body-filterable via this
  param; fetch individual captures with the detail endpoint instead.
- **JSON bodies only.** The request body is parsed as JSON. GraphQL and gRPC
  bodies are also covered. A non-JSON body (SOAP/XML, form-encoded, binary)
  simply matches nothing when a `body` filter is present.

**`query`** — `<key>:<value>`, repeated for AND matching. Filters by a URL
query parameter from the captured request.

```bash
# Match captures where ?page=2
curl -s 'http://localhost:9090/api/matched/products?query=page:2'

# AND — both query params must match
curl -s 'http://localhost:9090/api/matched/products?query=page:2&query=sort:price'
```

Combine freely:

```bash
curl -s 'http://localhost:9090/api/matched/hello?method=POST&status=201&headers=x-cid:test-123'
curl -s 'http://localhost:9090/api/matched/orders?method=POST&body=$.email:ada@x.com&query=dry_run:true'
```

---

## TTL and expiry

Captured requests expire automatically. The TTL is **global** — it applies to
all captures regardless of rule. Default is 3600 seconds (one hour).

Expiry behaviour depends on the store backend:

| Backend | Mechanism | Setup required |
|---|---|---|
| **DynamoDB** | Native TTL attribute `ttl` (epoch seconds); DynamoDB deletes expired items asynchronously. | Enable TTL on the table (attribute name `ttl`) once, out-of-band. Default table: `mockwave-matched-requests` (override with `MOCKWAVE_DYNAMO_MATCHED_TABLE`). |
| **MongoDB** | TTL index on `expireAt` field; MongoDB deletes expired documents. | `db.matched_requests.createIndex({expireAt:1},{expireAfterSeconds:0})` once. |
| **Cosmos DB** | Per-item `ttl` field; requires `DefaultTimeToLive` on the container. | Set `DefaultTimeToLive` on the container once. |
| **JSON file** | Background sweep: expired entries are removed every `sync-interval` seconds. | No extra setup. |

For DynamoDB, MongoDB, and Cosmos the `SweepExpired` method is a no-op —
expiry is handled natively by the backend. For the JSON store and the in-memory
buffer, Mockwave runs a sweep on each sync tick and also skips expired entries
lazily on list queries.

---

## Query model and consistency

### Buffer-primary reads

The in-memory ring buffer is the **primary query source**. When you hit
`GET /api/matched/{rule_id}`, the handler reads from the buffer — not the
store — so captures appear immediately after the request completes, with no sync
lag.

A write-behind syncer flushes the buffer to the configured store every
`sync-interval` seconds. On startup, the buffer is hydrated from the store, so
captures survive a restart (subject to TTL).

### Single-instance consistency

Queries read **this instance's** buffer. If you run multiple Mockwave instances
behind a load balancer, captures recorded by instance A are not visible on
instance B's API until the next sync interval after both instances have flushed
and rehydrated — which is not a supported pattern for live query consistency.

Cross-instance live reads are out of scope. For multi-instance deployments,
query a single designated instance, or accept eventual consistency after a full
sync cycle.

### Capture is best-effort

A capture failure (buffer full, store save error) is logged and the request
proceeds normally. A full ring buffer overwrites the oldest entries. Store save
errors are retried on the next sync tick.

---

## Custom store backends

Implement `store.MatchedStore` to plug in any backend:

```go
type MatchedStore interface {
    SaveMatched(ctx context.Context, reqs []matched.Request) error
    ListMatched(ctx context.Context, ruleID string, q MatchedQuery) (MatchedPage, error)
    GetMatched(ctx context.Context, ruleID, id string) (*matched.FullRequest, error)
    DeleteMatched(ctx context.Context, ruleID string) error // "" = all rules
    SweepExpired(ctx context.Context, before time.Time) (int, error) // return 0, nil if using native TTL
}
```

Pass the implementation via `MatchedConfig.Store`:

```go
srv, err := server.New(server.Config{
    Matched: server.MatchedConfig{
        Enabled: true,
        TTL:     time.Hour,
        Store:   &myCustomStore{},
    },
})
```

When `Store` is `nil`, Mockwave derives the matched store from the configured
`--store` backend.

---

## Limitations

- **Single-instance live reads.** Queries read the local buffer; cross-instance
  live consistency is not supported (see [Query model](#query-model-and-consistency)).
- **Ring buffer capacity.** When the buffer is full, new captures overwrite the
  oldest entries. Increase `--matched-buffer-size` if you are capturing at high
  volume.
- **DynamoDB native TTL is asynchronous.** DynamoDB may serve expired items for
  up to 48 hours after their TTL attribute is reached. Items are invisible to
  Mockwave queries (the buffer does not return expired items) but may appear if
  you query DynamoDB directly during that window.
- **Capture is best-effort.** A capture failure never blocks or fails the
  request; in high-error conditions some captures may be lost.
