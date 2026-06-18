# AWS Event Interception — Plan Index

Spec: [`docs/specs/2026-06-17-aws-event-interception-design.md`](../specs/2026-06-17-aws-event-interception-design.md)

The feature is large, so it ships in phases — each phase is its own plan and produces working, testable software on its own (mirrors the repo's `chaos-faults-phase*` plans). Out-of-scope items live in [`docs/roadmap.md`](../roadmap.md).

| Phase | Plan | Deliverable |
|-------|------|-------------|
| **1** | [`2026-06-17-aws-event-interception-phase1-plan.md`](2026-06-17-aws-event-interception-phase1-plan.md) | SNS Publish interception on the mock port: parse → match (separate `EventRule` config) → in-memory capture → synthesized XML response. jsonfile store + admin endpoints + `--aws` flag. End-to-end with the real SNS SDK. |
| **2** | [`2026-06-17-aws-event-interception-phase2-plan.md`](2026-06-17-aws-event-interception-phase2-plan.md) | **Delivered.** SQS `SendMessage` (JSON protocol) + EventBridge `PutEvents` (JSON, batch) parsers and responders; faithful SQS MD5 checksums (body + attributes); e2e tests with real AWS SDKs for all three services. |
| **3** | **Delivered.** [`2026-06-17-aws-event-interception-phase3-plan.md`](2026-06-17-aws-event-interception-phase3-plan.md) | Optional re-signed forward via `aws-sdk-go-v2` (`internal/adapters/out/awsforward/`), credential resolver (`default`/`profile:`/`static:`), real-response propagation, FIFO passthrough. |
| **4** | **Delivered.** [`2026-06-17-aws-event-interception-phase4-plan.md`](2026-06-17-aws-event-interception-phase4-plan.md) | `EventRuleStore` + event-capture persistence on DynamoDB / MongoDB / Cosmos with native TTL; event captures written behind to the matched store (distinguished by `aws-*` protocol prefix); restart hydration; per-backend integration tests. |

Each subsequent phase is planned with the writing-plans skill once the previous one is merged, so the plan reflects the code as it actually landed.

---

The four-phase AWS event-interception arc is complete. All further work (GCP Pub/Sub, Azure Service Bus, batch ops, consumer side, fault injection, weighted buckets, SNS fan-out, non-Go SDK fixtures, and the Phase 3 forward-fidelity items) is tracked in [`docs/roadmap.md`](../roadmap.md).
