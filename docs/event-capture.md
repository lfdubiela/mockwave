# Outgoing Event Capture (AWS)

Mockwave can intercept your application's AWS SNS publishes on the mock port,
capture them to state for assertion, and return a valid SDK response so the
publish call succeeds. This extends Mockwave's regressive e2e model to the
**outgoing event** side: after your system-under-test completes a flow, assert
not just the HTTP response it returned, but also the exact event it emitted —
target ARN, message payload, attributes, and publisher identity.

Capture is **opt-in** (default off, zero overhead when disabled).

Phase 1 covers SNS. SQS, EventBridge, re-signed forward, and persistence are on
the [roadmap](roadmap.md).

- [Why this exists](#why-this-exists)
- [Pointing your AWS SDK at Mockwave](#pointing-your-aws-sdk-at-mockwave)
- [Enabling event capture](#enabling-event-capture)
- [Config — event\_rules](#config--event_rules)
- [EventMatch fields](#eventmatch-fields)
- [Admin API](#admin-api)
- [Worked example](#worked-example)
- [Limitations and roadmap](#limitations-and-roadmap)

---

## Why this exists

The regressive e2e flow that event capture enables:

1. Define event rules in Mockwave (which publishes to intercept).
2. Point your application's AWS SDK at Mockwave via endpoint override.
3. Trigger the flow under test.
4. Your application publishes to SNS; Mockwave intercepts it, captures it, and
   returns a valid `PublishResponse` so the SDK proceeds normally.
5. Assert the response your application returned to its caller.
6. Assert the event your application emitted — target ARN, message, attributes,
   publisher access key — via `GET /api/event-captures/{ruleID}`.

Without step 6 you only know your application returned the right response given a
mocked SNS. With it you also know it published the right event to the right topic.

---

## Pointing your AWS SDK at Mockwave

Mockwave listens for AWS calls on the same mock port as HTTP (`8080` by default).
Detection is automatic: requests are identified as AWS messaging by the SigV4
`Authorization` credential scope (`sns`, `sqs`, or `events` service component).

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
| `service` | yes | `sns` — the AWS service. (SQS and EventBridge in a later phase.) |
| `operation` | no | Exact operation name, e.g. `Publish`. Omit to match any. |
| `target` | no | Glob against the topic ARN (SNS), queue URL (SQS), or event bus name (EventBridge). `*` matches any single segment; `**` matches any number. |
| `source` | no | EventBridge `source` field. Glob. (EventBridge only; reserved for a later phase.) |
| `detail_type` | no | EventBridge `detail-type` field. Glob. (Reserved for a later phase.) |
| `attributes` | no | Map of message attribute key → expected value. Exact match; all specified attributes must be present. |
| `message` | no | Map of JSONPath expression → expected scalar value, applied to the published message body. Reuses the same JSONPath matcher as HTTP rule body matching. |

The `forward` field on an event rule (sibling of `match`) is reserved for a
later phase (re-signed forward to the real broker). Omit it or leave it null;
Mockwave will synthesize a valid response in its place.

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
| `path` | — | Glob match against the captured target ARN / queue URL. |

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
| `protocol` | `aws-sns` (SQS and EventBridge protocols in a later phase). |
| `method` | The AWS operation: `Publish` for SNS. |
| `path` | The target topic ARN. |
| `identity` | The access key ID of the publisher (extracted from the SigV4 `Authorization` header). |
| `request_body` | The published message payload. |
| `response_body` | The synthesized response Mockwave returned to the SDK (includes the generated `MessageId`). |

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

**Phase 1 ships:**

- SNS `Publish` interception and synthesized `PublishResponse` (valid XML, with
  a generated `MessageId` the SDK accepts).
- In-memory capture, paginated query, and identity recording (publisher access
  key ID).

**Known constraints:**

- **SNS only.** SQS `SendMessage` and EventBridge `PutEvents` are on the
  [roadmap](roadmap.md).
- **In-memory capture only.** Event captures are held in the ring buffer; they
  do not survive a restart and are not written to the configured store backend
  in v1. Persistence is on the roadmap.
- **No forward.** The `forward` field on an event rule is reserved but not yet
  active. Forward requires Mockwave to re-sign the request with its own AWS
  credentials — it cannot reuse the app's SigV4 token verbatim because the
  signature commits to the `host` header and that host is Mockwave, not the
  real AWS endpoint. Re-signed forward is on the roadmap.
- **No fault injection.** Simulating broker errors (throttling, `AccessDenied`)
  on intercepted publishes is deferred.
- **No weighted buckets.** Traffic splitting per event rule is deferred.

See [`docs/roadmap.md`](roadmap.md) for the full list of deferred items
including SQS/EventBridge, batch ops, consumer-side polling, and non-Go SDK
fixtures.
