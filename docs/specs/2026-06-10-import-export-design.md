# Import/Export Rules & Simulations — Design

**Date:** 2026-06-10
**Status:** Approved pending review

## Goal

Export rules (and their simulations) to a JSON file from the admin API/UI, and import such a file back with explicit conflict handling. The exported file uses the existing `domain.Config` shape, so it doubles as a runnable config: `mockwave start --store=json --config exported.json`.

## Scope & gating

Import/export is a **remote-store feature** (dynamodb, mongo, cosmos). With `--store=json` the config file *is* the store, so the endpoints return `403` and the UI hides the buttons. No new CLI command: running an exported file via `start --config` already works.

Gating mechanism: `main.go` passes `ImportExport: storeType != "json"` through `server.Config` into `restapi.NewMux`. Endpoints are always registered; when disabled they return `403` with a message explaining that the json store's config file is already the import/export format. `/api/health` gains `"import_export": bool` so the UI can show/hide controls.

## API

### Export

```
GET /api/export?rules=id1,id2
```

- `rules` omitted or empty → export all rules.
- Response: `200`, body is `domain.Config{Rules, Simulations}`.
- Simulations are auto-collected from the selected rules' simulate buckets (`ruleSimIDs`); only sims referenced by exported rules are included.
- Headers: `Content-Type: application/json`, `Content-Disposition: attachment; filename="mockwave-export.json"`.
- Unknown rule IDs in the `rules` param → `404` listing the missing IDs.

### Import — two-phase

**Phase 1, preview (dry run, no writes):**

```
POST /api/import/preview
Body: domain.Config
```

Response `200`:

```json
{
  "importable": 3,
  "conflicts": [
    {
      "reason": "match",
      "incoming": {"id": "r-new", "name": "New rule"},
      "existing": {"id": "r-old", "name": "Old rule"}
    },
    {
      "reason": "id",
      "incoming": {"id": "r-7", "name": "Renamed"},
      "existing": {"id": "r-7", "name": "Original"}
    }
  ]
}
```

**Phase 2, commit:**

```
POST /api/import?override=id1,id2
Body: domain.Config
```

- `override` lists **incoming rule IDs** the user chose to replace their conflicting counterparts.
- Response `200`: `{"imported": [...], "skipped": [...], "overridden": [...]}` (rule IDs).
- One `reload()` after all writes.

### Conflict definition

Two categories, both shown in preview and both overridable via the same `override` list:

- `reason: "match"` — incoming rule's match criteria equal a stored rule's with a different ID. Uses the existing `domain.FindDuplicateRule` / `MatchCriteria.Equal` (all six fields: protocol, method, path, headers, query, body) — identical to the dup check admin `POST /api/rules` already enforces with `409`.
- `reason: "id"` — incoming rule ID exists in the store but match criteria differ. Rule ID is never part of criteria comparison.

A rule with the same ID **and** equal match criteria reports a single conflict with `reason: "id"` (identity collision is the more specific fact).

### Import semantics

- **Validate first.** Every incoming rule passes `rule.Validate()`; every simulate bucket's `simulation_id` must resolve in the payload or the store. Any failure → `422` with details, nothing written. Commit is not transactional across remote stores, but validate-first removes most partial-write risk and the response reports exactly what landed.
- **Override path:** delete the existing conflicting rule and its owned sims (cascade via `ruleSimIDs` + `deleteSimulations`), then save the incoming rule and its payload sims.
- **Skip path:** conflicting rules without override are skipped entirely — neither the rule nor its payload-only sims are written; listed in `skipped`.
- **Sim ID collision without rule conflict:** incoming sim whose ID exists in the store but whose rule is non-conflicting is silently overwritten (`SaveSimulation` is upsert). Rationale: rules own sims; a sim alone never matches traffic. Documented behavior, not a conflict.
- **Internal payload duplicates** (two incoming rules with the same ID or equal match) → `422`.

## Admin UI

- **Export:** toolbar button on rules view → select-rules mode with checkboxes (or "export all") → triggers download of `GET /api/export`.
- **Import:** toolbar button → file picker → `POST /api/import/preview` → if conflicts, modal lists each (incoming vs existing name/id, reason badge, per-row override checkbox) → commit with chosen override list → toast summarizing imported/skipped/overridden counts. No conflicts → commit immediately.
- Both buttons hidden when `/api/health` reports `import_export: false`.
- Modal/buttons follow existing macOS theme components (vibrancy modal, pill buttons, capsule badges).

## Components

| Unit | Responsibility |
|---|---|
| `domain` | Already has `Config`, `FindDuplicateRule`, `MatchCriteria.Equal` — no changes expected beyond possibly a helper for ID lookup. |
| `restapi/server.go` (or new `restapi/transfer.go`) | `exportHandler`, `importPreviewHandler`, `importHandler`; shared conflict-detection helper used by both import phases. |
| `restapi/static/index.html` | Export selection mode, import modal, health-flag gating. |
| `server.Config` / `restapi.NewMux` | Thread `ImportExport bool`. |
| `cmd/mockwave/main.go` | Set flag from `storeType`. |

## Error handling

| Case | Response |
|---|---|
| json store | `403` + explanation |
| invalid JSON body | `400` |
| validation failure / payload-internal duplicates / unresolvable sim ref | `422` + details |
| unknown rule IDs in export param | `404` + missing IDs |
| store error | `500` |

## Testing

Table-driven handler tests alongside existing `restapi` tests:

- Export: all rules, subset, sims auto-collected (only referenced ones), unknown ID → 404, attachment headers.
- Preview: match conflict, id conflict, id+match → single `id` conflict, clean payload → empty conflicts.
- Commit: skip without override, override replaces rule + cascades old sims, mixed batch report correctness, single reload.
- Validation: invalid rule → 422 nothing written; dangling sim ref → 422; internal dup → 422.
- Gating: 403 on json store for all three endpoints; health flag present.
- Round-trip: export → import into empty store → identical rules/sims.
