# AWS Event Interception — Plan Index

Spec: [`docs/specs/2026-06-17-aws-event-interception-design.md`](../specs/2026-06-17-aws-event-interception-design.md)

The feature is large, so it ships in phases — each phase is its own plan and produces working, testable software on its own (mirrors the repo's `chaos-faults-phase*` plans). Out-of-scope items live in [`docs/roadmap.md`](../roadmap.md).

| Phase | Plan | Deliverable |
|-------|------|-------------|
| **1** | [`2026-06-17-aws-event-interception-phase1-plan.md`](2026-06-17-aws-event-interception-phase1-plan.md) | SNS Publish interception on the mock port: parse → match (separate `EventRule` config) → in-memory capture → synthesized XML response. jsonfile store + admin endpoints + `--aws` flag. End-to-end with the real SNS SDK. |
| **2** | _to be authored after Phase 1 lands_ | SQS `SendMessage` (JSON) + EventBridge `PutEvents` (JSON) parsers/responders, including the SQS MD5 (body + attributes) algorithm. Contract round-trip tests for all three services. |
| **3** | _to be authored after Phase 2 lands_ | Optional re-signed forward via `aws-sdk-go-v2` (`internal/adapters/out/awsforward/`), credential resolver (`default`/`profile:`/`static:`), real-response propagation, FIFO passthrough. |
| **4** | _to be authored after Phase 3 lands_ | `EventRuleStore` + event-capture persistence on DynamoDB / MongoDB / Cosmos with native TTL; restart hydration; per-backend integration tests. |

Each subsequent phase is planned with the writing-plans skill once the previous one is merged, so the plan reflects the code as it actually landed.
