# AWS Event Interception — Outgoing Event Capture, Mock & Forward

**Date:** 2026-06-17
**Status:** Approved (design)
**Scope:** New inbound adapter for AWS messaging publish APIs (SNS / SQS / EventBridge), a separate event-rule config + matcher, capture into the existing MatchedStore infrastructure, protocol-faithful response synthesis, and optional re-signed forward to the real broker.

Touches: `domain/model.go`, `store/store.go`, `internal/adapters/in/awsmsg/` (new), `internal/domain/eventroute/` (new), `internal/adapters/out/awsforward/` (new), `internal/matched/`, `internal/metrics/`, `internal/adapters/cfg/restapi/`, `internal/adapters/out/{jsonfile,dynamodb,mongodb,cosmos}/`, `server/`, `cmd/mockwave/main.go`.

## Goal

Extend Mockwave's mock + proxy + capture model to the **outgoing event** side. In an e2e flow the application under test publishes events to a message broker (AWS SNS / SQS / EventBridge). Today Mockwave only intercepts inbound HTTP / GraphQL / SOAP / gRPC — it cannot observe what the app *emits*. This feature lets the app point its AWS SDK client at Mockwave via endpoint override, so Mockwave:

1. Intercepts the publish call on the existing mock port (8080).
2. Parses it into a normalized event and captures it to state for assertion.
3. Either acts as the broker (synthesizes a valid AWS response so the SDK succeeds) or forwards to the real broker (re-signed with Mockwave's own credentials), per event rule.

This enables regressive e2e on the publish side: assert exactly which event the system emitted (target, message, attributes, publisher identity), optionally with the real downstream still wired.

## Decisions (from brainstorming)

- **Direction:** Intercept the app's outgoing publishes → capture → forward. Mockwave is a fake broker *endpoint*, not an event producer.
- **First scope:** AWS SNS + SQS + EventBridge (all HTTP + SigV4). Design left extensible to GCP Pub/Sub / Azure Service Bus.
- **Forward:** Optional per rule, **re-signed** with Mockwave's own credentials. Default = Mockwave is the broker (capture + synthesize valid response). Verbatim forward to real AWS is impossible — see *Why verbatim forward 403s*.
- **Config model:** Separate `EventRule` type + `EventRuleStore` interface + dedicated admin endpoints, isolated from HTTP rules. Implemented by the **same** storage backends (no duplicated infra).
- **Capture:** Reuse the MatchedStore buffer / syncer / TTL / pagination machinery by mapping the normalized event onto the existing `matched.Request` shape with `aws-*` protocol values; separation lives at the admin-view level.
- **Port topology:** Reuse `protocolMux` on the mock port (8080). AWS calls are HTTP/1.1 POST and add one detection branch. gRPC stays on its own port (`:50051`); admin stays on `:9090`. Existing HTTP traffic is unaffected.
- **No weighted buckets and no fault injection on events in v1** (both deferred to roadmap).
- **Success responses only in v1** (error / throttle simulation deferred).
- **Single-op first:** `Publish` / `SendMessage` / `PutEvents`. Batch SNS/SQS deferred (PutEvents is natively batch and ships complete).

Out-of-scope items are tracked in [`docs/roadmap.md`](../roadmap.md).

## Why verbatim forward 403s (the SigV4 constraint)

SigV4 has the client compute an HMAC signature over a *canonical request* and send it in the `Authorization` header. The canonical request commits to:

- method + path + query
- the signed headers — and **`host` is always signed**
- the hash of the **body**
- the credential scope: `<date>/<region>/<service>/aws4_request`

AWS recomputes the signature from what it receives and compares. Any divergence → `SignatureDoesNotMatch` (403). Consequences for a verbatim forward to the real broker:

1. **`host` is signed.** Reaching real AWS means connecting to `sns.<region>.amazonaws.com`. Rewriting `host` invalidates the signature; keeping the original `host` means AWS does not serve that host (and TLS SNI/cert mismatch). The signature is bound to the destination.
2. **The secret key never travels.** The `Authorization` header carries only the signature + access key id + signed-header list — not the secret. Re-signing for a new host is impossible from the forwarded request alone; it requires Mockwave to hold **its own** credentials.
3. **The body is signed.** Parsing and re-serializing the body (even cosmetically) changes its hash → 403. Byte-for-byte passthrough conflicts with capturing/normalizing.
4. **Requests expire** (`x-amz-date`, ~5–15 min). Buffering/delay can trigger `RequestExpired` (403).

"Same token" passthrough therefore **only** applies to bearer-token brokers (GCP Pub/Sub OAuth2, Azure SAS) — not AWS SigV4. The credential resolver (below) leaves a hook for verbatim bearer passthrough when those providers land.

## Architecture

```
app (AWS SDK, endpoint → mockwave:8080)
        │  HTTP/1.1 POST  (SigV4)
        ▼
 protocolMux (mock port)
        │  detect AWS (SigV4 scope service ∈ {sns,sqs,events} or X-Amz-Target)
        ▼
 in/awsmsg.Handler
   ├─ detect service           (SigV4 credential scope / X-Amz-Target / Action)
   ├─ parse wire → Event        (normalized)
   ├─ eventroute.Match(Event)   (first-match over []EventRule)
   ├─ capture → MatchedStore    (buffer + syncer, aws-* protocol)
   └─ action:
        ├─ respond  → synthesize XML/JSON (MessageId / EventId / correct MD5)
        └─ forward  → awsforward (re-sign via aws-sdk-go-v2) → real broker → return real response
```

### Entry & service detection

The app sets `AWS_ENDPOINT_URL` (or a per-client `endpoint`) to Mockwave's mock port. A single endpoint serves all three services. `protocolMux` (`server/server.go`) gains one branch before the HTTP fallthrough: a request is AWS messaging when it carries a SigV4 `Authorization` whose credential scope service is `sns`, `sqs`, or `events`, or an `X-Amz-Target` of `AmazonSQS.*` / `AWSEvents.*`.

Service resolution inside the adapter:

- **Primary:** SigV4 credential scope service (`.../<region>/<service>/aws4_request`) — always present.
- **Secondary confirmation:**
  - SNS (query/form protocol): `Action=Publish` in the form body.
  - SQS (JSON protocol, modern SDKs): `X-Amz-Target: AmazonSQS.SendMessage`. (Legacy SQS = `Action=SendMessage` form.)
  - EventBridge: `X-Amz-Target: AWSEvents.PutEvents`, JSON body.

### Normalized event

Produced by the per-service parser; used for both matching and capture.

```go
type Event struct {
    Service    string            // sns | sqs | eventbridge
    Operation  string            // Publish | SendMessage | PutEvents
    Target     string            // topic ARN | queue URL | event bus name
    Source     string            // EventBridge source (empty for sns/sqs)
    DetailType string            // EventBridge detail-type
    Subject    string            // SNS subject (optional)
    Message    []byte            // payload: SNS Message / SQS MessageBody / EB Detail
    Attributes map[string]string // message attributes / EB resources
    GroupID    string            // FIFO MessageGroupId (SQS/SNS FIFO)
    DedupID    string            // FIFO MessageDeduplicationId
    Region     string            // from SigV4 scope
    Identity   string            // access key id of the publisher (who sent it)
    RawBody    []byte            // original wire body
}
```

### Event rule model & matching

Separate from HTTP `Rule`. Its own `EventRuleStore` interface, its own admin endpoints; backed by the same stores.

```go
type EventRule struct {
    ID       string
    Name     string
    Disabled bool
    Match    EventMatch
    Forward  *EventForward // nil = capture + synthesized response (default)
}

type EventMatch struct {
    Service    string            // required: sns | sqs | eventbridge
    Operation  string            // optional
    Target     string            // glob: arn:aws:sns:*:*:my-topic | https://sqs.*/my-queue | bus
    Source     string            // EventBridge, glob
    DetailType string            // EventBridge, glob
    Attributes map[string]string // exact
    Message    map[string]string // JSONPath → expected scalar (reuses existing matcher)
}

type EventForward struct {
    Endpoint   string // real AWS endpoint ("" = default endpoint for Region)
    Region     string // override
    Credential string // "" | "default" | "profile:<name>" | "static:<name>"
    DelayMs    int    // optional injected latency, mirrors HTTP forward
}
```

Matching reuses the glob (target/source/detail-type) and JSONPath matcher from `internal/domain/matching`. Rules are sorted by specificity; first match wins. `Service` must equal the detected service.

### Capture to state

Reuse the MatchedStore machinery (in-memory buffer + write-behind syncer + native TTL on Dynamo/Mongo/Cosmos + cursor pagination + admin API). The normalized event maps onto the existing `matched.Request`:

| Existing field   | Event value                                       |
|------------------|---------------------------------------------------|
| `RuleID`         | event rule id                                     |
| `Protocol`       | `aws-sns` \| `aws-sqs` \| `aws-eventbridge`       |
| `Method`         | Operation (Publish / SendMessage / PutEvents)     |
| `Path`           | Target (topic ARN / queue URL / bus)              |
| `Query`          | source, detail-type, subject, attributes          |
| `RequestBodyID`  | Message payload (out-of-line, as today)           |
| `ResponseBodyID` | response (MessageId / EventId, or real forward response) |
| `Status`         | 200 / forward status                              |
| **new fields**   | `Identity`, `Forwarded bool`, `ForwardTarget`     |

`SaveMatched` / `ListMatched` / `GetMatched` / TTL on all four backends work essentially unchanged — only new `Protocol` values plus 2–3 columns. The event admin view is an endpoint that filters `protocol IN (aws-*)`. The fully-separate `EventCaptureStore` alternative was rejected: it triples storage code across backends with no functional gain.

### Response synthesis (Mockwave as broker)

When no forward is configured, Mockwave must return a response the SDK accepts, in the **same wire protocol** the SDK used (detected via `Content-Type` / `X-Amz-Target`).

- **SNS Publish** (query → XML):

  ```xml
  <PublishResponse xmlns="https://sns.amazonaws.com/doc/2010-03-31/">
    <PublishResult><MessageId>{uuid}</MessageId></PublishResult>
    <ResponseMetadata><RequestId>{uuid}</RequestId></ResponseMetadata>
  </PublishResponse>
  ```

- **SQS SendMessage** (JSON, `X-Amz-Target: AmazonSQS.SendMessage`):

  ```json
  {"MD5OfMessageBody":"{md5}","MessageId":"{uuid}","SequenceNumber":null}
  ```

- **EventBridge PutEvents** (JSON, `X-Amz-Target: AWSEvents.PutEvents`):

  ```json
  {"FailedEntryCount":0,"Entries":[{"EventId":"{uuid}"}]}
  ```

Correctness requirements (else the SDK errors):

1. **MD5 is mandatory on SQS.** The SDK validates `MD5OfMessageBody` (and `MD5OfMessageAttributes` when attributes are present); a wrong MD5 throws a checksum mismatch. Attributes use AWS's specific encoding algorithm — implemented faithfully in `awsmsg/md5.go`.
2. **Protocol must match** the SDK's wire format (XML vs `application/x-amz-json`).
3. **PutEvents is an array** — the response `Entries` count equals the request event count, one `EventId` each.
4. **IDs** (MessageId / EventId / RequestId) are UUIDs generated by Mockwave and captured to state for correlation.

When a forward is configured, Mockwave does **not** synthesize — it returns the real broker response (already valid).

### Forward (optional, re-signed)

`internal/adapters/out/awsforward/` re-emits via `aws-sdk-go-v2` (already a dependency through the DynamoDB adapter). It takes the normalized `Event`, calls `sns.Publish` / `sqs.SendMessage` / `eventbridge.PutEvents` against the real broker with Mockwave's configured credential + region + endpoint, and returns the real response to the app. Re-signing raw HTTP by hand was rejected as fragile.

Credential resolution (`awsforward/creds.go`), referenced by name — **secrets never live in a rule**:

- `""` / `"default"` → the SDK default credential chain (env, shared config/profile, IRSA/instance role).
- `"profile:<name>"` → shared config profile.
- `"static:<name>"` → a named static credential block defined in server config / env, for test rigs.

`EventForward.Region` / `.Endpoint` override the target; an empty endpoint means real AWS for the region (a set endpoint can point at a specific account, VPC endpoint, or LocalStack). On forward failure Mockwave **propagates the real error/response** to the app (transparent proxy fidelity — never masks with a synthetic success); the capture records the outcome (`Forwarded=true`, status, error). FIFO fields (`GroupID` / `DedupID`) are captured and passed through so FIFO semantics hold.

### Component layout

```
domain/model.go                              MOD  + Event, EventRule, EventMatch, EventForward
store/store.go                               MOD  + EventRuleStore interface

internal/adapters/in/awsmsg/                 NEW  inbound AWS messaging adapter
  detect.go                                       service via SigV4 scope / X-Amz-Target / Action
  parse_sns.go / parse_sqs.go / parse_eb.go       wire → Event
  respond_sns.go / respond_sqs.go / respond_eb.go Event + result → valid response (XML/JSON)
  md5.go                                          SQS MD5 (body + attributes)
  handler.go                                      detect → parse → flow → respond | forward

internal/domain/eventroute/matcher.go        NEW  Event × []EventRule, first-match
                                                  (reuses glob + JSONPath from internal/domain/matching)

internal/adapters/out/awsforward/            NEW  re-emit via aws-sdk-go-v2
  forward.go                                      sns.Publish / sqs.SendMessage / eb.PutEvents
  creds.go                                        resolve credential: default | profile: | static:

internal/matched/request.go                  MOD  + Identity, Forwarded, ForwardTarget; aws-* protocols
internal/matched/ (buffer/syncer)            -    reused unchanged
internal/metrics/                            MOD  capture + metrics hook for events (mirrors HTTP middleware)

internal/adapters/cfg/restapi/               MOD  + /api/event-rules (CRUD)
                                                  + /api/event-captures (list / detail / delete)
internal/adapters/out/{jsonfile,dynamodb,    MOD  implement EventRuleStore
                       mongodb,cosmos}/            jsonfile: "event_rules" key; cloud: new table/collection

server/server.go                             MOD  load EventRuleStore, build matcher, inject awsmsg into protocolMux
server/events.go                             NEW  event config + credential resolution (mirrors matched.go)
cmd/mockwave/main.go                         MOD  protocol flag "aws" (default off → zero impact)
```

Boundaries: `awsmsg` (in) depends on `eventroute` (match) + `awsforward` (out) + `matched` (capture), and knows no backend. `eventroute` is pure domain (reuses the matcher, no I/O). `awsforward` is the only place importing the SNS/SQS/EventBridge SDK clients. Capture/metrics reuse the HTTP infra via `aws-*` values. `protocolMux` gains one branch; HTTP is untouched.

## Data flow (happy path, respond mode)

1. App SDK publishes to `mockwave:8080` with SigV4.
2. `protocolMux` detects AWS markers → `awsmsg.Handler`.
3. `detect` resolves service from the credential scope.
4. The service parser produces a normalized `Event`.
5. `eventroute.Match` finds the first matching `EventRule` (else: unmatched — record for debug, synthesize a generic success so the app is unblocked).
6. The event is appended to the matched buffer (`aws-*` protocol); the syncer flushes to the store.
7. No `Forward` → the responder synthesizes a protocol-faithful response with a generated id (and correct MD5 for SQS).
8. The test asserts via `GET /api/event-captures?rule=...` (target, message, attributes, identity).

Forward mode replaces steps 7: `awsforward` re-signs and emits to the real broker, returns its response, and the capture records `Forwarded=true` + the real status/ids.

## Testing

Mirrors the repo's existing layered + per-backend e2e pattern. Coverage gate ≥80% (`make coverage`).

1. **Unit — parsers:** golden wire fixtures (SNS form, SQS JSON, EventBridge JSON) → assert normalized `Event`.
2. **Unit — detect:** SigV4 scope / `X-Amz-Target` → correct service.
3. **Unit — respond + MD5:** synthesized responses parse; **SQS MD5 matches AWS test vectors** (body + attributes).
4. **Unit — matcher:** Event × EventRule, glob/JSONPath, specificity ordering.
5. **Contract round-trip (highest value):** real `aws-sdk-go-v2` clients pointed at the handler (`httptest`) perform Publish / SendMessage / PutEvents and **accept the response with no checksum/parse error**.
6. **Integration — capture:** publish via SDK → event present in MatchedStore (per backend; reuses the existing Dynamo/Mongo harness).
7. **Integration — forward:** forward enabled → an upstream stub ("real AWS") confirms a re-signed request (Authorization present, body intact, FIFO fields passed through).
8. **e2e — full flow:** app SDK → Mockwave 8080 → capture + respond; and a forward variant → stub. Mirrors the current matched-capture e2e.

**Fidelity risk (noted):** SDK wire format varies by language/version (SQS JSON vs query, SNS XML). v1 tests against `aws-sdk-go-v2` (canonical); boto3 / JS v3 fixtures are a follow-up. Detection by SigV4 scope / target is universal and mitigates this.

## Out of scope (v1)

Tracked in [`docs/roadmap.md`](../roadmap.md): GCP Pub/Sub & Azure Service Bus, batch ops (`PublishBatch` / `SendMessageBatch`), the consumer side (SQS `ReceiveMessage` polling, SNS HTTP subscription delivery), event fault injection, weighted/canary event buckets, SNS filter policies & SNS→SQS fan-out, and non-Go SDK fixtures.
