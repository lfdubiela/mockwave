# Dashboard Per-Rule TPS — Design

**Date:** 2026-06-06
**Status:** Approved (design)

## Goal

Rework the admin dashboard's live metrics so traffic is shown as throughput
(requests per second) broken down per rule, with the ability to focus on the
busiest rules, and surface the current TPS in the Rule Hit Distribution panel.

## Motivation

The current dashboard plots a single line of total requests-per-minute and a
Rule Hit Distribution of cumulative hits. It cannot answer "which rule is
driving load right now and at what rate." Operators manually testing weighted
rules (the recent 50/50 work) want a live, per-rule throughput view.

## Requirements

1. **Multi-line throughput chart** — one line per rule, y-axis = req/s, x-axis =
   last 30 minutes at per-minute granularity. TPS for a minute = bucket count ÷ 60.
2. **Top-10 selection** — only the 10 rules with the most hits **within the
   visible 30-minute window** are charted. Ranking is computed server-side.
3. **Hover tooltip** — hovering a data point shows `requests: <count> · <rule-name>`
   for that minute/rule.
4. **Filter chips** — below the chart, one chip per charted rule (top 10). All on
   by default; clicking a chip toggles that rule's line. Each chip shows the rule
   name and its color swatch.
5. **Current TPS in Rule Hit Distribution** — each distribution row shows the
   rule's current TPS next to its hit count. Current TPS = the most recent
   **completed** minute bucket's count ÷ 60.

## Non-Goals

- Per-second resolution / sub-minute live charting (per-minute is sufficient).
- A Misses/Total line on the chart (rules only).
- Persisting metrics across restarts (in-memory only, unchanged).
- Configurable window length (fixed 30 min, matching today).

## Architecture

### Backend — `internal/metrics`

**Approach A: per-rule minute ring.**

`Collector` gains a per-rule history map alongside the existing total ring:

```go
type Collector struct {
    // ...existing fields...
    hist      histRing                 // total per-minute (unchanged, kept)
    ruleHist  map[string]*histRing     // ruleID -> per-minute ring
}
```

- `RecordHit(ruleID, ruleName, latencyMs)` additionally records into
  `ruleHist[ruleID]` (lazily created). `RecordMiss` does not touch rule rings.
- The existing total `hist` ring and `History()` method are retained for
  backward compatibility (and any other consumer), but the dashboard switches to
  the new per-rule endpoint.

**New types:**

```go
// RuleSeries is a per-rule per-minute time series.
type RuleSeries struct {
    RuleID   string         `json:"rule_id"`
    RuleName string         `json:"rule_name"`
    Buckets  []MinuteBucket `json:"buckets"`
}
```

**New collector method:**

```go
// RuleHistory returns per-rule minute series for the top-N rules ranked by
// total hits within the retained window, newest-busiest first. N<=0 means all.
func (c *Collector) RuleHistory(topN int) []RuleSeries
```

Ranking: sum each rule's retained bucket counts (the 30-min window), sort
descending, take the first `topN`. Ties broken by ruleID for determinism.

**Current TPS** is added to the existing per-rule snapshot:

```go
type RuleStats struct {
    // ...existing fields...
    CurrentTPS float64 `json:"current_tps"`
}
```

`Snapshot()` computes `CurrentTPS` for each rule = (most recent **completed**
minute bucket count) ÷ 60. "Completed" means a bucket whose timestamp is strictly
before the current minute; if the only bucket is the in-progress minute,
CurrentTPS = 0.

### Transport — `internal/adapters/cfg/restapi`

- `GET /api/metrics/history` response changes from a flat `[]MinuteBucket` to:

  ```json
  {
    "rules": [
      { "rule_id": "r1", "rule_name": "duedates-17194",
        "buckets": [ { "ts": "...", "count": 25 }, ... ] }
    ]
  }
  ```

  Server calls `collector.RuleHistory(10)`. Empty `rules` array when no history.

- SSE `/api/metrics/stream` is unchanged structurally; each `RuleStats` now
  carries `current_tps` automatically via the Snapshot change.

### Frontend — `static/index.html`

- **Chart:** replace the single fill+line path with N `<path>` elements, one per
  rule, drawn from a fixed color palette (cycled). Shared x-axis (last 30
  minutes), shared y-axis scaled to the max req/s across visible rules. The
  fill-gradient area is dropped (multi-line clarity over area aesthetics).
- **Tooltip:** an absolutely-positioned div; on `mousemove` over the chart, find
  the nearest minute column and hovered rule, show `requests: <count> · <name>`.
- **Chips:** render below the chart from the returned `rules`. Each chip = color
  swatch + rule name; `aria-pressed`/active class toggles a `hidden` set; toggling
  re-renders visible paths and rescales y.
- **Distribution panel:** existing per-rule bars (from SSE Snapshot) gain a
  `· N.N tps` suffix sourced from `current_tps`.
- Refresh cadence unchanged: chart polled every 10s; distribution via SSE.

## Data Flow

```
request → middleware → Collector.RecordHit(ruleID,name,latency)
                         ├─ total hist ring (unchanged)
                         └─ ruleHist[ruleID] ring (new)

dashboard chart  ──poll 10s──▶ GET /api/metrics/history
                                 └─ Collector.RuleHistory(10) → {rules:[...]}
dashboard distrib ──SSE──────▶ Snapshot.Rules[].CurrentTPS
```

## Error Handling

- `RuleHistory` on an empty collector returns an empty slice → endpoint emits
  `{"rules":[]}` → UI shows existing "No request history yet." state.
- Unknown/zero buckets are skipped exactly as the current ring snapshot does.
- Frontend tolerates an empty `rules` array and rules with fewer than 2 points
  (a single point renders as a dot, not a path).

## Testing

- **Collector unit tests:** per-rule ring isolation; `RuleHistory` ranking +
  topN truncation + tie-break; `CurrentTPS` uses last completed minute (and is 0
  when only the in-progress minute exists); misses excluded from rule rings.
- **History endpoint test:** new JSON shape, top-10 cap, empty case.
- **UI smoke test:** asserts presence of chip container, per-rule path rendering
  hook, tooltip element, and the `current_tps`/tps label in the distribution
  renderer.

## Backward Compatibility

`GET /api/metrics/history` response shape changes (breaking for any external
consumer). Acceptable: it is an internal admin endpoint consumed only by the
bundled UI. The total `hist` ring and `Collector.History()` remain for other
callers.

## File Touch List

- `internal/metrics/collector.go` — ruleHist map, RecordHit wiring, RuleSeries,
  RuleHistory, RuleStats.CurrentTPS, Snapshot change.
- `internal/metrics/history.go` — possibly a small helper to sum a ring's buckets
  and fetch the last completed bucket (or compute in collector).
- `internal/metrics/collector_test.go`, `history_test.go` — new tests.
- `internal/adapters/cfg/restapi/server.go` — history handler returns per-rule
  shape.
- `internal/adapters/cfg/restapi/server_test.go` — endpoint + UI smoke asserts.
- `internal/adapters/cfg/restapi/static/index.html` — multi-line chart, chips,
  tooltip, distribution TPS.
- `internal/adapters/cfg/restapi/openapi.yaml` — document new history response.
