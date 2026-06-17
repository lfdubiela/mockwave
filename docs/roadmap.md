# Roadmap

Planned-but-not-yet-built work. Items land here when a shipped feature deliberately defers scope, so the cut is tracked rather than lost. Each entry notes where it came from.

## Outgoing event interception (AWS)

Shipped in v1: SNS / SQS / EventBridge publish interception, capture, protocol-faithful response, and optional re-signed forward. Design: [`docs/specs/2026-06-17-aws-event-interception-design.md`](specs/2026-06-17-aws-event-interception-design.md).

Deferred:

- **GCP Pub/Sub & Azure Service Bus** — additional brokers. The credential resolver already leaves a hook for verbatim bearer-token passthrough (OAuth2 / SAS), which — unlike AWS SigV4 — can reuse the app's original token on forward.
- **Batch publish ops** — `PublishBatch` (SNS), `SendMessageBatch` (SQS). (`PutEvents` is natively batch and already complete.)
- **Consumer side** — SQS `ReceiveMessage` polling and SNS HTTP subscription delivery. v1 covers only the outgoing/publish side.
- **Event fault injection** — simulate broker errors (throttling, `AccessDenied`, 5xx) on intercepted publishes, reusing the existing `FaultProfile` model.
- **Weighted / canary event buckets** — traffic split per event rule (e.g. 90% respond / 10% forward), mirroring HTTP weighted buckets.
- **SNS filter policies & SNS→SQS fan-out** — model subscription filtering and fan-out semantics.
- **Non-Go SDK fixtures** — contract fixtures captured from boto3 and the JS v3 SDK, to harden wire-format fidelity beyond `aws-sdk-go-v2`.
