# Match Criteria UI + Tiered Precedence — Design

**Date:** 2026-06-08
**Status:** Approved (pending spec review)

## Problem

The admin UI rule form (`internal/adapters/cfg/restapi/static/index.html`) only exposes
**protocol / method / path** match criteria. The backend `MatchCriteria` model
(`domain/model.go`) also defines `Headers`, `Query`, and `Body` (JSONPath → exact value),
but:

- **Headers / Query** matching is implemented in the matcher
  (`internal/domain/matching/matcher.go`) yet not reachable from the UI.
- **Body** matching exists only in the model — the matcher ignores `Match.Body` entirely,
  so any body condition silently never matches.

Additionally, rule precedence today is a **weighted sum** in `specificity()`
(`headers*10 + query*5 + path-no-wildcard*3`). This can produce surprising ordering
(enough query matches outranks an exact path). The desired behavior is a **strict tiered
(lexicographic) precedence**.

## Goals

1. Expose **Headers**, **Query**, and **Body** matchers in the rule modal (Focused — no
   layout overhaul).
2. Implement **Body (JSONPath) matching** in the matcher so the UI does not promise a
   no-op.
3. Replace weighted-sum precedence with **strict tiered ordering**.
4. Surface configured matchers in the rules table.

Non-goals: rule modal layout redesign, match preview/testing UI, full JSONPath spec.

## Components

### 1. UI — rule modal (`static/index.html`)

Inside the existing **Match Criteria** section, below the protocol/method/path row, add
three repeatable key-value groups, each collapsed to a "+ Add" link when empty (zero
clutter when unused):

```
Headers   [ key ]            [ value ]   [×]   + Add header
Query     [ key ]            [ value ]   [×]   + Add query param
Body      [ $.user.id ]      [ value ]   [×]   + Add body match
```

- State held as JS arrays `_headers`, `_query`, `_body` — same pattern as `_buckets`.
  Each entry `{ key, value }`.
- Render functions `renderHeaders()`, `renderQuery()`, `renderBody()` mirror
  `renderBuckets()` (innerHTML rebuild; inputs write back into the arrays by index).
- Add/remove helpers per group (`addHeader()`/`removeHeader(i)`, etc.).
- **Save** (`saveRule()`): replace the hardcoded `headers: {}, query: {}` with objects
  built from non-empty rows; add `body` object likewise. Empty key → row skipped.
- **Edit round-trip** (`editRule()`): populate `_headers`/`_query`/`_body` from
  `r.match.headers` / `r.match.query` / `r.match.body` (object → array of `{key,value}`).
- **Reset** (`openRuleModal()`): clear all three arrays.
- Body helper text: "Matches against JSON request bodies. JSONPath (e.g. `$.user.id`),
  exact value match."

### 2. Rules table (`static/index.html`)

Add compact muted matcher chips under the Path cell (no new column — keeps existing
header row intact):

```
H:x-cid=123   Q:q=hi   B:$.user.id=7
```

Built from `r.match.headers/query/body`. Truncate past 3 entries with `+N`. Omitted when
no matchers configured.

### 3. Backend — Body matcher (`internal/domain/matching/matcher.go`)

`MatchCriteria.Body` is `map[string]string` (path → exact value). Add a matcher step after
the Query loop:

- If `len(m.Body) == 0` → skip (no effect).
- Parse `req.Body` (`[]byte`) as JSON into `interface{}`. Parse failure → **no match** for
  any rule with a body condition.
- For each `path, want`: resolve `path` against the parsed value; stringify the resolved
  leaf; compare to `want`. Any miss → no match.

**Path evaluator — hand-rolled, no new dependency** (full JSONPath not needed for exact
leaf lookups; keeps supply chain clean):

- Accept optional leading `$.` or `$`; split remainder on `.`.
- Each segment is either an object key or an array index (all-digits → index).
- Stringify leaf: numbers via `strconv.FormatFloat`/int detection to avoid `1` vs `1.0`
  mismatch; bools `"true"`/`"false"`; strings as-is. Non-leaf (object/array) at end → no
  match.

### 4. Backend — tiered precedence (`internal/domain/matching/matcher.go`)

Replace scalar `specificity()` + comparison in `sortBySpecificity()` with a
**lexicographic tuple** compared element-wise, descending. First differing element decides.

Tuple `(headers, url, body, query)`:

| Tier | Value |
|------|-------|
| headers | `len(Match.Headers)` |
| url | exact path (non-empty, no `*`) → `1000`; wildcard → `100 + literalSegmentCount`; empty → `0` |
| body | `len(Match.Body)` |
| query | `len(Match.Query)` |

- `literalSegmentCount` = count of `/`-split path segments containing no `*` (so
  `/api/v1/*` → 2 literal segs → `102`; `/*` → 0 → `100`; exact `/exact-url` → `1000`).
- Exact always beats any wildcard (1000 > 100+n for realistic paths).
- All tiers equal → preserve input order (stable). Existing `sortBySpecificity` is an
  insertion sort; keep it stable (only swap on strict `>`).
- Protocol/method are filters only — excluded from ranking.

**Behavior change:** today `query*5` could outrank `path*3`. Under tiers, headers dominate,
then url, then body, then query. Example: `/*` + 1 header → `(1,100,0,0)` beats
`/exact-url` + 0 headers → `(0,1000,0,0)` because header tier compares first.

## Data Flow

```
Request → matcher.Match(rule, req):
  protocol filter → method filter → path glob →
  headers loop → query loop → body (parse JSON + path eval)  → bool

Matching rules → sortBySpecificity (tuple lexicographic desc, stable) → first wins
```

## Error Handling

- Malformed request JSON body + rule has body condition → rule does not match (fail
  closed). Rules without body conditions are unaffected.
- UI: empty key rows are dropped silently on save (not an error).
- Missing/typed-mismatch path leaf → no match for that rule (not a server error).

## Testing

Go unit tests (`internal/domain/matching/matcher_test.go`):

- Body match (top-level, nested `$.a.b`, array index `$.items.0.id`).
- Body mismatch (wrong value).
- Malformed JSON body with body condition → no match.
- Path resolves to object/array (non-leaf) → no match.
- Precedence tiers: header-rule (`/*`+header) beats exact-path no-header rule.
- Precedence: exact path beats wildcard when headers equal.
- Precedence: `/api/v1/*` beats `/*` (literal segment count).
- Precedence: body beats query when headers+url equal.
- Stable order when all tiers equal.

Manual UI verification:

- Create rule with header + query + body matchers; save; reload; confirm persisted.
- Edit the rule; confirm all three groups round-trip from server.
- Rules table shows matcher chips.
- Send a matching request and a non-matching request; confirm precedence behaves per
  example.

## Files Touched

- `internal/adapters/cfg/restapi/static/index.html` — form groups, save/edit/reset,
  table chips.
- `internal/domain/matching/matcher.go` — body matcher, tuple precedence.
- `internal/domain/matching/matcher_test.go` — new tests.
