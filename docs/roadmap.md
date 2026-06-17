# Roadmap

Planned-but-not-yet-built work. Items land here when a shipped feature deliberately defers scope, so the cut is tracked rather than lost. Each entry notes where it came from.

## Outgoing event interception (AWS)

Shipped (Phases 1 + 2): SNS `Publish`, SQS `SendMessage` (JSON protocol, faithful MD5 checksums), and EventBridge `PutEvents` (JSON, batch — each entry captured separately) interception, in-memory capture, protocol-faithful synthesized responses, and identity recording. Design: [`docs/specs/2026-06-17-aws-event-interception-design.md`](specs/2026-06-17-aws-event-interception-design.md).

Deferred:

- **Re-signed forward (Phase 3)** — optional forwarding to the real broker, re-signing with Mockwave's own AWS credentials. Deferred because the SigV4 signature commits to the `host` header; verbatim replay is not possible.
- **Capture persistence (Phase 4)** — `EventRuleStore` + event-capture storage on DynamoDB / MongoDB / Cosmos with native TTL and restart hydration. Currently in-memory only.
- **GCP Pub/Sub & Azure Service Bus** — additional brokers. The credential resolver already leaves a hook for verbatim bearer-token passthrough (OAuth2 / SAS), which — unlike AWS SigV4 — can reuse the app's original token on forward.
- **Batch publish ops** — `PublishBatch` (SNS), `SendMessageBatch` (SQS). (`PutEvents` is natively batch and already shipped.)
- **Consumer side** — SQS `ReceiveMessage` polling and SNS HTTP subscription delivery. Shipped phases cover only the outgoing/publish side.
- **Event fault injection** — simulate broker errors (throttling, `AccessDenied`, 5xx) on intercepted publishes, reusing the existing `FaultProfile` model.
- **Weighted / canary event buckets** — traffic split per event rule (e.g. 90% respond / 10% forward), mirroring HTTP weighted buckets.
- **SNS filter policies & SNS→SQS fan-out** — model subscription filtering and fan-out semantics.
- **Non-Go SDK fixtures** — contract fixtures captured from boto3 and the JS v3 SDK, to harden wire-format fidelity beyond `aws-sdk-go-v2`.
