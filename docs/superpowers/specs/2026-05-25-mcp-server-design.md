# Mockwave MCP Server Design

**Date:** 2026-05-25
**Status:** Approved

## Goal

Allow AI assistants (Claude Code) to interact with any Mockwave instance — local or remote — to create, inspect, and delete rules and simulations via the Model Context Protocol (MCP).

## Architecture

```
Claude Code
    │  stdio (stdin/stdout)
    ▼
mockwave mcp (local process, spawned by Claude Code)
    │  HTTP
    ▼
Mockwave Admin API (:9090) — localhost OR remote (EKS, sandbox, etc.)
```

MCP servers are always local processes. Claude Code communicates via stdio only. `mockwave mcp` acts as an HTTP bridge: receives MCP tool calls via stdio, translates to HTTP requests against `--admin-url`, returns responses.

Two instances → two entries in `mcp.json`, same local binary:

```json
{
  "mcpServers": {
    "mockwave-local": {
      "command": "mockwave",
      "args": ["mcp", "--admin-url", "http://localhost:9090"]
    },
    "mockwave-sandbox": {
      "command": "mockwave",
      "args": ["mcp", "--admin-url", "https://mockwave.sandbox.acme.com"]
    }
  }
}
```

Claude Code namespaces tools automatically: `mockwave-local__create_rule`, `mockwave-sandbox__list_rules`, etc.

---

## Deliverables

### 1. OpenAPI spec — `GET /api/openapi.json`

New endpoint on the existing admin server. Serves a static OpenAPI 3.0 spec (embedded in the binary at build time — not generated via runtime reflection). Documents all existing admin API endpoints.

File location: `internal/adapters/cfg/restapi/openapi.yaml` (embedded via `//go:embed`).

### 2. `mockwave mcp` subcommand

New Cobra subcommand in `cmd/mockwave/main.go`.

**Flag:** `--admin-url string` (required) — base URL of the Mockwave admin API.

**Transport:** stdio (MCP standard for local processes). Uses `github.com/mark3labs/mcp-go` SDK.

**Session behavior:** The MCP server does not prompt for URLs itself — URL is fixed at launch via `--admin-url`. If the user has multiple instances registered in `mcp.json`, Claude Code presents both tool namespaces and Claude naturally asks the user which instance to target before acting.

---

## MCP Tools (CRUD Complete)

### Rules

| Tool | Description | Key params |
|------|-------------|------------|
| `list_rules` | List all rules | — |
| `get_rule` | Get single rule by ID | `id` |
| `create_rule` | Create a new rule | `id`, `name`, `match`, `buckets`, `forward_url?` |
| `update_rule` | Replace a rule | `id`, full rule body |
| `delete_rule` | Delete a rule by ID | `id` |

### Simulations

| Tool | Description | Key params |
|------|-------------|------------|
| `list_simulations` | List all simulations | — |
| `get_simulation` | Get single simulation by ID | `id` |
| `create_simulation` | Create a simulation | `id`, `protocol`, `response`, `script?`, etc. |
| `delete_simulation` | Delete a simulation by ID | `id` |

### Observability

| Tool | Description |
|------|-------------|
| `get_metrics` | Current metrics snapshot (total requests, per-rule hits, p95) |
| `list_unmatched` | List captured unmatched requests |
| `clear_unmatched` | Clear unmatched request buffer |
| `reload` | Trigger hot-reload from store |
| `health` | Check admin API reachability |

---

## Data Flow

```
Claude: "create a mock for GET /orders returning 200 with body {orders:[]}"

1. Claude calls create_simulation(id="orders-sim", protocol="http",
                                   response={status:200, body:{orders:[]}})
   → POST /api/simulations

2. Claude calls create_rule(id="orders-get", name="Get Orders",
                             match={method:"GET", path:"/orders"},
                             buckets=[{weight:100, action:"simulate",
                                       simulation_id:"orders-sim"}])
   → POST /api/rules

3. MCP returns success with IDs
4. Claude confirms to user
```

---

## File Structure

| Action | Path |
|--------|------|
| Create | `cmd/mockwave/mcp.go` — `mcpCmd()` Cobra command |
| Create | `internal/mcp/server.go` — MCP server setup, tool registration |
| Create | `internal/mcp/tools.go` — all tool handlers (HTTP calls to admin API) |
| Create | `internal/mcp/client.go` — thin HTTP client wrapping admin API |
| Create | `internal/adapters/cfg/restapi/openapi.yaml` — OpenAPI 3.0 spec |
| Modify | `internal/adapters/cfg/restapi/server.go` — add `GET /api/openapi.json` route |
| Modify | `cmd/mockwave/main.go` — register `mcpCmd` |
| Modify | `go.mod` — add `github.com/mark3labs/mcp-go` |

---

## Out of Scope

- Remote MCP transport (SSE/HTTP) — stdio is sufficient; admin URL handles remote targets
- `generate_from_openapi` tool (tracked as roadmap task #56)
- Authentication on admin API (future work)
- MCP tool for `update_simulation` (admin API has no PUT for simulations — delete + create)
