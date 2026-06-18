# Roadmap

Planned-but-not-yet-built work. Items land here when a shipped feature deliberately defers scope, so the cut is tracked rather than lost. Each entry notes where it came from.

## Outgoing event interception (AWS)

Shipped (Phases 1 – 3): SNS `Publish`, SQS `SendMessage` (JSON protocol, faithful MD5 checksums), and EventBridge `PutEvents` (JSON, batch — each entry captured separately) interception, in-memory capture, protocol-faithful synthesized responses, identity recording, and optional re-signed forward via `aws-sdk-go-v2` (`default`/`profile:`/`static:` credential resolution, real broker id relayed, forward outcome captured). Design: [`docs/specs/2026-06-17-aws-event-interception-design.md`](specs/2026-06-17-aws-event-interception-design.md). Phase 3 plan: [`docs/plans/2026-06-17-aws-event-interception-phase3-plan.md`](plans/2026-06-17-aws-event-interception-phase3-plan.md).

Deferred:

- **Capture persistence (Phase 4)** — `EventRuleStore` + event-capture storage on DynamoDB / MongoDB / Cosmos with native TTL and restart hydration. Currently in-memory only.
- **Forward: Number/Binary message attribute types** — forwarded attributes are sent as `String` only; the original data type is not preserved.
- **Forward: EventBridge per-entry partial failure** — any entry error in `PutEvents` fails the whole request with 502; per-entry partial-failure fidelity is not modeled.
- **Forward: SQS FIFO `SequenceNumber` relay** — the broker-assigned sequence number is not surfaced in the forwarded response.
- **GCP Pub/Sub & Azure Service Bus** — additional brokers. The credential resolver already leaves a hook for verbatim bearer-token passthrough (OAuth2 / SAS), which — unlike AWS SigV4 — can reuse the app's original token on forward.
- **Batch publish ops** — `PublishBatch` (SNS), `SendMessageBatch` (SQS). (`PutEvents` is natively batch and already shipped.)
- **Consumer side** — SQS `ReceiveMessage` polling and SNS HTTP subscription delivery. Shipped phases cover only the outgoing/publish side.
- **Event fault injection** — simulate broker errors (throttling, `AccessDenied`, 5xx) on intercepted publishes, reusing the existing `FaultProfile` model.
- **Weighted / canary event buckets** — traffic split per event rule (e.g. 90% respond / 10% forward), mirroring HTTP weighted buckets.
- **SNS filter policies & SNS→SQS fan-out** — model subscription filtering and fan-out semantics.
- **Non-Go SDK fixtures** — contract fixtures captured from boto3 and the JS v3 SDK, to harden wire-format fidelity beyond `aws-sdk-go-v2`.
