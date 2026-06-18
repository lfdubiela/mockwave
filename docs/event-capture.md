# Outgoing Event Capture (AWS)

Mockwave can intercept your application's AWS SNS, SQS, and EventBridge publishes
on the mock port, capture them to state for assertion, and return a valid SDK
response so the publish call succeeds. This extends Mockwave's regressive e2e
model to the **outgoing event** side: after your system-under-test completes a
flow, assert not just the HTTP response it returned, but also the exact event it
emitted — target ARN / queue URL / event bus, message payload, attributes, and
publisher identity.

Capture is **opt-in** (default off, zero overhead when disabled).

Phases 1 and 2 cover SNS, SQS, and EventBridge interception with synthesized
responses. Phase 3 adds optional **re-signed forwarding**: a matched rule with a
`forward` block re-emits the event to the real broker via the AWS SDK, relays
the broker's real id to the app, and records the outcome in the capture. Phase 4
adds **cloud persistence**: event rules and captures survive restarts on
DynamoDB, MongoDB, and Cosmos backends.

- [Why this exists](#why-this-exists)
- [Pointing your AWS SDK at Mockwave](#pointing-your-aws-sdk-at-mockwave)
- [Enabling event capture](#enabling-event-capture)
- [Config — event\_rules](#config--event_rules)
- [EventMatch fields](#eventmatch-fields)
- [Forwarding](#forwarding)
- [Persistence](#persistence)
- [Service-specific behaviour](#service-specific-behaviour)
  - [SNS](#sns)
  - [SQS](#sqs)
  - [EventBridge](#eventbridge)
- [Matcher caveat — target globs and slashes](#matcher-caveat--target-globs-and-slashes)
- [Admin API](#admin-api)
- [Worked example](#worked-example)
- [Limitations and roadmap](#limitations-and-roadmap)

---

## Why this exists

The regressive e2e flow that event capture enables:

1. Define event rules in Mockwave (which publishes to intercept).
2. Point your application's AWS SDK at Mockwave via endpoint override.
3. Trigger the flow under test.
4. Your application publishes to SNS / SQS / EventBridge; Mockwave intercepts it,
   captures it, and returns a valid response so the SDK proceeds normally.
5. Assert the response your application returned to its caller.
6. Assert the event your application emitted — target, message, attributes,
   publisher access key — via `GET /api/event-captures/{ruleID}`.

Without step 6 you only know your application returned the right response given a
mocked broker. With it you also know it published the right event to the right
destination.

---

## Pointing your AWS SDK at Mockwave

Mockwave listens for AWS calls on the same mock port as HTTP (`8080` by default).
Detection is automatic: requests are identified as AWS messaging by the SigV4
`Authorization` credential scope (`sns`, `sqs`, or `events` service component) or
by the `X-Amz-Target` header (`AmazonSQS.*` / `AWSEvents.*`).

Override the endpoint in your application before the flow starts.

### Environment variable (any language)

```bash
export AWS_ENDPOINT_URL=http://localhost:8080
```

The AWS SDK picks this up automatically (Go v2, boto3, JS v3, and all SDKs that
honour the standard endpoint override env var).

### Go — per-client override

```go
import (
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/sns"
)

client := sns.NewFromConfig(cfg, func(o *sns.Options) {
    o.BaseEndpoint = aws.String("http://localhost:8080")
})
```

A single Mockwave endpoint serves all three services — you do not need separate
ports for SNS, SQS, and EventBridge.

---

## Enabling event capture

### CLI

Add `--protocols http,aws --event-capture` to the `mockwave start` command. The
other flags are optional; defaults are shown.

```bash
mockwave start -f config.json \
  --protocols http,aws \
  --event-capture \
  --event-ttl 3600 \
  --event-buffer-size 10000 \
  --event-sync-interval 30
```

| Flag | Default | Description |
|---|---|---|
| `--event-capture` | false | Enable AWS event interception and capture. |
| `--event-ttl` | `3600` | Capture TTL in seconds. |
| `--event-buffer-size` | `10000` | In-memory ring buffer capacity. |
| `--event-sync-interval` | `30` | Write-behind sync interval in seconds. |

`--protocols aws` registers the AWS messaging adapter on the mock port. Without
it, AWS calls are handled as plain HTTP.

### Environment variables

```bash
MOCKWAVE_EVENT_CAPTURE=true
MOCKWAVE_EVENT_TTL=3600
MOCKWAVE_EVENT_BUFFER_SIZE=10000
MOCKWAVE_EVENT_SYNC_INTERVAL=30
```

---

## Config — event_rules

Event rules live in the `event_rules` array of your JSON config file. They are
separate from HTTP `rules`.

```json
{
  "rules": [ ],
  "simulations": [ ],
  "event_rules": [
    {
      "id": "order-placed-sns",
      "name": "Order placed notification",
      "match": {
        "service": "sns",
        "target": "arn:aws:sns:us-east-1:*:order-placed",
        "message": {
          "$.eventType": "ORDER_PLACED"
        }
      }
    }
  ]
}
```

Rules are evaluated in order; the first match wins. Unmatched publishes are
recorded for debugging and still receive a synthesized success response so the
SDK is never blocked.

---

## EventMatch fields

| Field | Required | Description |
|---|---|---|
| `service` | yes | `sns`, `sqs`, or `eventbridge`. |
| `operation` | no | Exact operation name, e.g. `Publish`, `SendMessage`, `PutEvents`. Omit to match any. |
| `target` | no | Glob against the topic ARN (SNS), queue URL (SQS), or event bus name (EventBridge). Uses Go `path.Match`; see [caveat below](#matcher-caveat--target-globs-and-slashes). |
| `source` | no | EventBridge `source` field. Glob. |
| `detail_type` | no | EventBridge `detail-type` field. Glob. |
| `attributes` | no | Map of message attribute key → expected value. Exact match; all specified attributes must be present. |
| `message` | no | Map of JSONPath expression → expected scalar value, applied to the published message body. Reuses the same JSONPath matcher as HTTP rule body matching. |

The `forward` field on an event rule (sibling of `match`) is optional. When
present, Mockwave re-emits the event to the real broker instead of synthesizing
a response — see [Forwarding](#forwarding) below. Omit it or leave it null to
receive a synthesized response.

---

## Forwarding

An event rule may carry an optional `forward` block (sibling of `match`). When
Mockwave matches an event to such a rule, it re-emits the event to the real
broker via `aws-sdk-go-v2`, relays the broker's real message / event id to the
app in the synthesized response, and records the outcome in the capture
(`forwarded: true`, `forward_target`, and the response status).

### Why re-signing is required

The app's outgoing SigV4 signature is bound to the exact `host` header it sent
(i.e. Mockwave's address). The AWS signature algorithm commits to the host in
the canonical request, and the app's secret key never travels over the wire —
only the HMAC'd signature does. Replaying the intercepted request verbatim
against a real AWS endpoint would yield a 403 because the `host` header would
not match the signature. Mockwave therefore signs the forwarded request from
scratch using its own configured credential.

### Forward block fields

| Field | Default | Description |
|---|---|---|
| `endpoint` | `""` | Real broker endpoint URL. Empty uses the AWS SDK default for the region. |
| `region` | `us-east-1` | AWS region used for endpoint selection and request signing. |
| `credential` | `""` | Credential reference (see below). |
| `delay_ms` | `0` | Extra latency injected after the forward call returns (additive; runs even on error). |

### Credential references

| Value | Resolution |
|---|---|
| `""` or `"default"` | AWS SDK default credential chain (env vars, EC2/ECS metadata, shared credentials file, etc.). |
| `"profile:<name>"` | Named profile from the shared AWS config / credentials file. |
| `"static:<name>"` | Environment variables `MOCKWAVE_AWS_STATIC_<NAME>_KEY`, `MOCKWAVE_AWS_STATIC_<NAME>_SECRET`, and optionally `MOCKWAVE_AWS_STATIC_<NAME>_TOKEN`, where `<NAME>` is the profile name upper-cased. |

### Example — forwarding to real SNS

```json
{
  "event_rules": [
    {
      "id": "orders",
      "match": { "service": "sns" },
      "forward": {
        "endpoint": "https://sns.us-east-1.amazonaws.com",
        "region": "us-east-1",
        "credential": "static:prod"
      }
    }
  ]
}
```

With `credential: "static:prod"`, Mockwave reads
`MOCKWAVE_AWS_STATIC_PROD_KEY` and `MOCKWAVE_AWS_STATIC_PROD_SECRET` from the
environment.

### Forwarding semantics

- **Forward failure → HTTP 502.** If the real broker rejects the call (network
  error, invalid credentials, throttled, etc.) Mockwave returns HTTP 502 to the
  app. A synthesized success is never returned on a forward error.
- **Broker id relayed.** On success, Mockwave uses the broker's real
  `MessageId` / `EventId` in the response it returns to the app (instead of a
  generated id).
- **Capture always recorded.** Regardless of success or failure, the event is
  captured with `forwarded: true`, `forward_target` (endpoint or `aws:<region>`
  when no explicit endpoint is set), and the HTTP status that was returned (200
  or 502).
- **`delay_ms` is additive.** The delay is injected after the forward call
  returns (total observed latency ≈ upstream RTT + `delay_ms`). It runs even
  when the forward call failed.
- **FIFO pass-through.** `MessageGroupId` and `MessageDeduplicationId` are
  forwarded to SNS and SQS as-is when present.

### Known limitations (v1)

- **Message attributes are forwarded as `String` type only.** The normalized
  `Event` model does not retain the original data type of each attribute;
  Number and Binary attributes are deferred to a future roadmap item.
- **EventBridge per-entry partial failure is not modeled.** `PutEvents` accepts
  a batch of entries, and individual entries can fail independently. Mockwave
  treats any per-entry error as a full-request failure and returns 502.
- **SQS FIFO `SequenceNumber` is not relayed.** The real broker assigns a
  `SequenceNumber` to FIFO messages; Mockwave does not surface it in the
  response.

---

## Persistence

### Event rules

Event rules persist on the same store backends as HTTP rules:

| Backend | Where rules live |
|---|---|
| `--store dynamodb` | Dedicated DynamoDB table. Flag `--dynamo-event-rules-table` (default `mockwave-event-rules`); env `MOCKWAVE_DYNAMO_EVENT_RULES_TABLE`. |
| `--store mongo` / `--store cosmos` | Collection `event_rules` in the configured database. Created automatically. |
| `--store json` | The `event_rules` array in the JSON config file. Captured events stay in-memory (no separate persistence). |

Admin CRUD operations against a remote store (`POST /api/event-rules`, `PUT /api/event-rules/{id}`, `DELETE /api/event-rules/{id}`) trigger a config-version bump so every replica hot-reloads without a restart.

### Event captures

Event captures persist via write-behind to the same store table / collection that
the matched-capture feature uses. The two capture streams share the store and are
distinguished by protocol prefix: HTTP/GraphQL/SOAP/gRPC captures have no `aws-`
prefix; event captures always begin with `aws-` (`aws-sns`, `aws-sqs`,
`aws-eventbridge`). On hydration at startup each stream loads only its own
records.

Native TTL expiry applies (DynamoDB TTL attribute, MongoDB TTL index). Captures
older than `--event-ttl` (default 3600 s) are purged automatically by the
backend.

On restart the server hydrates up to `--event-buffer-size` (default 10 000) of
the newest captures back into memory. Under very heavy mixed HTTP + event load on
a shared store, hydration is best-effort: the server loads the newest records up
to the buffer limit. The store remains the authoritative source of truth and is
queryable per-rule at any scale via `GET /api/event-captures/{ruleID}`.

With `--store json`, event rules live in the config file's `event_rules` array,
but event captures are held only in the in-memory matched store — they are not
flushed to the config file and do not survive a restart.

---

## Service-specific behaviour

### SNS

- **Wire format:** query-string over HTTP POST (AWS SDK default).
- **Detection:** SigV4 credential scope `sns`.
- **Captured as:** protocol `aws-sns`, method `Publish`, path = topic ARN.
- **Synthesized response:** valid XML `PublishResponse` with a generated
  `MessageId`. The SDK asserts no checksum on SNS `Publish`, so the XML alone is
  sufficient.

### SQS

- **Wire format:** JSON body over HTTP POST (`X-Amz-Target: AmazonSQS.SendMessage`).
- **Detection:** `X-Amz-Target` header prefix `AmazonSQS.` (also confirmed by
  SigV4 credential scope `sqs`).
- **Captured as:** protocol `aws-sqs`, method `SendMessage`, path = queue URL
  (the `QueueUrl` field from the request body).
- **FIFO metadata:** `MessageGroupId` and `MessageDeduplicationId` are extracted
  and stored in the capture (`group_id` / `dedup_id` on the event).
- **Synthesized response:** JSON with `MD5OfMessageBody` computed faithfully
  from the raw message body bytes, and `MD5OfMessageAttributes` (included only
  when message attributes are present). The Go SDK v2 validates both checksums,
  so both must be correct for the call to succeed.

### EventBridge

- **Wire format:** JSON body over HTTP POST (`X-Amz-Target: AWSEvents.PutEvents`).
- **Detection:** `X-Amz-Target` header prefix `AWSEvents.` (also confirmed by
  SigV4 credential scope `events`).
- **Batch:** `PutEvents` is a batch operation. Each entry in `Entries` is captured
  as a **separate** event.
- **Captured as:** protocol `aws-eventbridge`, method `PutEvents`, path = event
  bus name (from the entry's `EventBusName` field; defaults to `default` when
  absent). The entry's `Source` and `DetailType` are stored as `source` /
  `detail_type` on the capture; the entry's `Detail` JSON string is stored as
  the request body.
- **Synthesized response:** JSON with `FailedEntryCount: 0` and one `EventId`
  per input entry, in order.

---

## Matcher caveat — target globs and slashes

`target` globs use Go `path.Match`, whose `*` wildcard does **not** cross a `/`
boundary.

- **SNS topic ARNs** contain no `/` (e.g.
  `arn:aws:sns:us-east-1:123456789012:order-placed`), so `*` globs the account
  number or suffix freely:
  `arn:aws:sns:us-east-1:*:order-placed` works as expected.

- **SQS queue URLs** contain slashes (e.g.
  `https://sqs.us-east-1.amazonaws.com/123456789012/my-queue`). A single `*`
  cannot span those slashes. Prefer either an exact `target` string, or leave
  `target` empty and rely on `service: sqs` (with optional `message` / `attributes`
  filters) to match all queues:

  ```json
  { "service": "sqs" }
  ```

- **EventBridge bus names** are plain identifiers without slashes (e.g.
  `default`, `my-bus`), so `*` globs work fine. Match on `source` and
  `detail_type` for finer-grained filtering.

---

## Admin API

When event capture is disabled (`--event-capture` not set), all
`/api/event-captures/` endpoints return `404`.

The admin port is `:9090` in all examples below.

### Event rules

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/event-rules` | List all event rules. |
| `POST` | `/api/event-rules` | Create an event rule. Returns `201`. |
| `PUT` | `/api/event-rules/{id}` | Replace an event rule. Returns `200`. |
| `DELETE` | `/api/event-rules/{id}` | Delete an event rule. Returns `204`. |

### Event captures

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/event-captures/{ruleID}` | List captures for a rule (paginated, reduced). |
| `GET` | `/api/event-captures/{ruleID}/{id}` | Full detail for one capture (includes message body). |
| `DELETE` | `/api/event-captures/{ruleID}` | Clear all captures for a rule. Returns `204`. |

#### `GET /api/event-captures/{ruleID}` — list (paginated)

Returns captures newest first. The list response omits bodies — just the metadata
needed to identify and filter.

**Query parameters:**

| Param | Default | Description |
|---|---|---|
| `limit` | `20` | Max items per page (max 100). |
| `cursor` | — | Opaque pagination cursor from a previous response. |
| `method` | — | Filter by operation name (`Publish`, `SendMessage`, `PutEvents`). |
| `path` | — | Glob match against the captured target ARN / queue URL / bus name. |

**Response `200`:**

```json
{
  "items": [
    {
      "id": "01J8XKZP3RQVWN1G2H5M7T9E00",
      "rule_id": "order-placed-sns",
      "at": "2026-06-17T10:00:00Z",
      "protocol": "aws-sns",
      "method": "Publish",
      "path": "arn:aws:sns:us-east-1:123456789012:order-placed",
      "response_status": 200
    }
  ],
  "next_cursor": "eyJhdCI6IjIwMjYtMDYtMTd..."
}
```

`next_cursor` is absent when there are no more pages.

#### `GET /api/event-captures/{ruleID}/{id}` — full detail

Returns the full capture including the published message as the request body.
`404` if the id does not exist or has expired.

```json
{
  "id": "01J8XKZP3RQVWN1G2H5M7T9E00",
  "rule_id": "order-placed-sns",
  "at": "2026-06-17T10:00:00Z",
  "protocol": "aws-sns",
  "method": "Publish",
  "path": "arn:aws:sns:us-east-1:123456789012:order-placed",
  "identity": "AKIAIOSFODNN7EXAMPLE",
  "response_status": 200,
  "request_body": "{\"eventType\":\"ORDER_PLACED\",\"orderId\":\"abc-123\"}",
  "response_body": {
    "messageId": "d1e7b8c0-1234-5678-9abc-def012345678"
  }
}
```

Captured fields:

| Field | Description |
|---|---|
| `protocol` | `aws-sns`, `aws-sqs`, or `aws-eventbridge`. |
| `method` | The AWS operation: `Publish` (SNS), `SendMessage` (SQS), or `PutEvents` (EventBridge). |
| `path` | The target: topic ARN (SNS), queue URL (SQS), or event bus name (EventBridge). |
| `identity` | The access key ID of the publisher (extracted from the SigV4 `Authorization` header). |
| `request_body` | The published message payload (SNS `Message`, SQS `MessageBody`, EventBridge entry `Detail`). |
| `response_body` | The synthesized response Mockwave returned to the SDK. |

---

## Worked example

This mirrors the e2e test. Start Mockwave with an event rule, publish via the Go
SNS SDK, then assert what was published.

**1. Config (`config.json`):**

```json
{
  "rules": [],
  "simulations": [],
  "event_rules": [
    {
      "id": "order-placed-sns",
      "name": "Order placed",
      "match": {
        "service": "sns",
        "target": "arn:aws:sns:us-east-1:*:order-placed"
      }
    }
  ]
}
```

**2. Start Mockwave:**

```bash
mockwave start -f config.json --protocols http,aws --event-capture
```

**3. Publish from your application (or a test):**

```go
cfg, _ := config.LoadDefaultConfig(ctx,
    config.WithCredentialsProvider(
        credentials.NewStaticCredentialsProvider("test", "test", ""),
    ),
    config.WithRegion("us-east-1"),
)
client := sns.NewFromConfig(cfg, func(o *sns.Options) {
    o.BaseEndpoint = aws.String("http://localhost:8080")
})

_, err := client.Publish(ctx, &sns.PublishInput{
    TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:order-placed"),
    Message:  aws.String(`{"eventType":"ORDER_PLACED","orderId":"abc-123"}`),
})
```

**4. Assert via the admin API:**

```bash
# List captures for the rule
curl -s http://localhost:9090/api/event-captures/order-placed-sns

# Full detail for one capture (copy the id from the list)
curl -s http://localhost:9090/api/event-captures/order-placed-sns/<id>
```

**5. Assert in a Go test:**

```go
resp, err := http.Get("http://localhost:9090/api/event-captures/order-placed-sns")
// ... decode and assert resp body contains the expected message and topic ARN
```

---

## Limitations and roadmap

**Shipped (Phases 1 – 4):**

- SNS `Publish` interception and synthesized `PublishResponse` (valid XML, with
  a generated `MessageId` the SDK accepts).
- SQS `SendMessage` interception (JSON protocol) and synthesized response with
  faithful `MD5OfMessageBody` and `MD5OfMessageAttributes` checksums.
- EventBridge `PutEvents` interception (JSON, batch) — each entry captured
  separately, synthesized response with one `EventId` per entry.
- In-memory capture, paginated query, and identity recording (publisher access
  key ID) for all three services.
- Re-signed forward via `aws-sdk-go-v2` with `default` / `profile:` / `static:`
  credential resolution; real broker id relayed to the app; forward outcome
  captured with `forwarded`, `forward_target`, and response status.
- Cloud persistence: `EventRuleStore` on DynamoDB / MongoDB / Cosmos with native
  TTL; event captures written behind to the matched store (distinguished by
  `aws-*` protocol prefix); restart hydration.

**Known constraints:**

- **No batch variants.** `PublishBatch` (SNS) and `SendMessageBatch` (SQS) are
  not yet supported. (`PutEvents` is natively batch and is complete.)
- **No consumer side.** SQS `ReceiveMessage` polling and SNS HTTP subscription
  delivery are deferred.
- **No fault injection.** Simulating broker errors (throttling, `AccessDenied`)
  on intercepted publishes is deferred.
- **No weighted buckets.** Traffic splitting per event rule is deferred.
- **Forward: `String`-type attributes only.** Number and Binary message
  attribute types are not preserved on forward (deferred).
- **Forward: EventBridge per-entry partial failure not modeled.** Any entry
  error fails the whole request with 502 (deferred).
- **Forward: SQS FIFO `SequenceNumber` not relayed.** The broker-assigned
  sequence number is not surfaced in the response (deferred).

See [`docs/roadmap.md`](roadmap.md) for the full list of deferred items
including batch ops, consumer-side polling, non-Go SDK fixtures, and GCP/Azure
brokers.
