# MCP Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Embed an MCP server in the `mockwave` binary as `mockwave mcp --admin-url <url>`, allowing Claude Code to create, inspect, and delete rules and simulations on any local or remote Mockwave instance.

**Architecture:** `mockwave mcp` spawns as a local stdio process. Claude Code sends MCP tool calls via stdin/stdout. The MCP server translates each call into an HTTP request against the configured `--admin-url` and returns the JSON result. Two admin URL instances → two entries in `mcp.json`, same local binary.

**Tech Stack:** Go 1.26, `github.com/mark3labs/mcp-go`, `github.com/spf13/cobra` (already present), `net/http/httptest` for tests.

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| Modify | `internal/adapters/cfg/restapi/server.go` | Add GET /api/rules/:id, GET /api/simulations/:id, PUT /api/simulations/:id, POST /api/reload |
| Modify | `internal/adapters/cfg/restapi/server_test.go` | Tests for new endpoints |
| Create | `internal/adapters/cfg/restapi/openapi.yaml` | OpenAPI 3.0 spec (embedded) |
| Modify | `internal/adapters/cfg/restapi/ui.go` | Embed openapi.yaml, serve GET /api/openapi.json |
| Modify | `go.mod` / `go.sum` | Add github.com/mark3labs/mcp-go |
| Create | `internal/mcp/client.go` | HTTP client wrapping admin API |
| Create | `internal/mcp/client_test.go` | httptest-based tests for client |
| Create | `internal/mcp/server.go` | MCP server setup + tool registration |
| Create | `internal/mcp/tools.go` | All MCP tool handler functions |
| Create | `cmd/mockwave/mcp.go` | `mcpCmd()` Cobra subcommand |
| Modify | `cmd/mockwave/main.go` | Register `mcpCmd()` |

---

## Task 1: REST API gaps — GET by ID, PUT simulation, POST reload

**Context:** `ruleByID` handles PUT and DELETE but not GET. `simulationByID` handles DELETE only. No `/api/reload` endpoint. MCP needs all of these.

**Files:**
- Modify: `internal/adapters/cfg/restapi/server.go`
- Modify: `internal/adapters/cfg/restapi/server_test.go`

- [ ] **Step 1: Write failing tests for the three missing endpoints**

Add to `internal/adapters/cfg/restapi/server_test.go` (before the final closing brace — after `TestAdminAPI_ServesUI`):

```go
func TestAdminAPI_GetRuleByID(t *testing.T) {
	store := &memStore{rules: []domain.Rule{
		{ID: "r1", Name: "R1", Match: domain.MatchCriteria{Path: "/foo"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "s1"}}},
	}}
	mux := restapi.NewMux(store, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/rules/r1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	var rule domain.Rule
	require.NoError(t, json.NewDecoder(w.Body).Decode(&rule))
	assert.Equal(t, "r1", rule.ID)
}

func TestAdminAPI_GetRuleByID_NotFound(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/rules/nope", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 404, w.Code)
}

func TestAdminAPI_GetSimulationByID(t *testing.T) {
	store := &memStore{sims: []domain.Simulation{
		{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}},
	}}
	mux := restapi.NewMux(store, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/simulations/s1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	var sim domain.Simulation
	require.NoError(t, json.NewDecoder(w.Body).Decode(&sim))
	assert.Equal(t, "s1", sim.ID)
}

func TestAdminAPI_GetSimulationByID_NotFound(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/simulations/nope", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 404, w.Code)
}

func TestAdminAPI_PutSimulation(t *testing.T) {
	store := &memStore{sims: []domain.Simulation{
		{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200}},
	}}
	mux := restapi.NewMux(store, nil, nil, nil, nil)
	updated := domain.Simulation{Protocol: "http", Response: domain.HTTPResponse{Status: 201}}
	body, _ := json.Marshal(updated)
	req := httptest.NewRequest(http.MethodPut, "/api/simulations/s1", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	var sim domain.Simulation
	require.NoError(t, json.NewDecoder(w.Body).Decode(&sim))
	assert.Equal(t, "s1", sim.ID)
	assert.Equal(t, 201, sim.Response.Status)
}

func TestAdminAPI_PostReload(t *testing.T) {
	reloaded := false
	mux := restapi.NewMux(&memStore{}, func() { reloaded = true }, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/reload", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 204, w.Code)
	assert.True(t, reloaded)
}
```

Also update `memStore` — add `DeleteSimulation` to return real errors (the existing one always returns nil which broke `TestAdminAPI_DeleteSimulation_NotFound` via the `errorDeleteSimStore` wrapper — keep that pattern, it's fine).

Add `UpdateSimulation` support by making `SaveSimulation` upsert (it already does since it appends — for PUT we need it to replace). Update `memStore.SaveSimulation` in the test file to replace if ID already exists:

```go
func (m *memStore) SaveSimulation(s domain.Simulation) error {
	for i, existing := range m.sims {
		if existing.ID == s.ID {
			m.sims[i] = s
			return nil
		}
	}
	m.sims = append(m.sims, s)
	return nil
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/adapters/cfg/restapi/... -run "TestAdminAPI_GetRuleByID|TestAdminAPI_GetSimulationByID|TestAdminAPI_PutSimulation|TestAdminAPI_PostReload" -v
```

Expected: FAIL — 405 or 404 responses where 200 expected.

- [ ] **Step 3: Implement GET /api/rules/:id, GET+PUT /api/simulations/:id, POST /api/reload**

In `internal/adapters/cfg/restapi/server.go`, update `ruleByID`:

```go
func (a *adminAPI) ruleByID(w http.ResponseWriter, r *http.Request) {
	id := idFromPath(r.URL.Path, "/api/rules/")
	switch r.Method {
	case http.MethodGet:
		rules, err := a.store.GetRules()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		for _, rule := range rules {
			if rule.ID == id {
				writeJSON(w, 200, rule)
				return
			}
		}
		writeError(w, 404, "rule not found: "+id)
	case http.MethodDelete:
		if err := a.store.DeleteRule(id); err != nil {
			writeError(w, 404, err.Error())
			return
		}
		a.reload()
		w.WriteHeader(204)
	case http.MethodPut:
		var rule domain.Rule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		rule.ID = id
		if err := rule.Validate(); err != nil {
			writeError(w, 422, err.Error())
			return
		}
		if err := a.store.SaveRule(rule); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		writeJSON(w, 200, rule)
	default:
		writeError(w, 405, "method not allowed")
	}
}
```

Update `simulationByID`:

```go
func (a *adminAPI) simulationByID(w http.ResponseWriter, r *http.Request) {
	id := idFromPath(r.URL.Path, "/api/simulations/")
	switch r.Method {
	case http.MethodGet:
		sim, err := a.store.GetSimulation(id)
		if err != nil {
			writeError(w, 404, err.Error())
			return
		}
		if sim == nil {
			writeError(w, 404, "simulation not found: "+id)
			return
		}
		writeJSON(w, 200, sim)
	case http.MethodPut:
		var sim domain.Simulation
		if err := json.NewDecoder(r.Body).Decode(&sim); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		sim.ID = id
		if err := a.store.SaveSimulation(sim); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		a.reload()
		writeJSON(w, 200, sim)
	case http.MethodDelete:
		if err := a.store.DeleteSimulation(id); err != nil {
			writeError(w, 404, err.Error())
			return
		}
		a.reload()
		w.WriteHeader(204)
	default:
		writeError(w, 405, "method not allowed")
	}
}
```

Add `reloadHandler` and register it. In `NewMux`, add:

```go
mux.HandleFunc("/api/reload", api.reloadHandler)
```

Add handler method:

```go
func (a *adminAPI) reloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	a.reload()
	w.WriteHeader(204)
}
```

Also update the two existing test cases that test "method not allowed" for GET on `simulationByID` and `ruleByID` — those tests now need updating since GET is implemented. Replace them:

In `server_test.go`, find and replace:

```go
// OLD — remove this:
func TestAdminAPI_SimulationByIDMethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/simulations/s1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

func TestAdminAPI_RuleByIDMethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/rules/r1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

// NEW — replace with:
func TestAdminAPI_SimulationByIDMethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPatch, "/api/simulations/s1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

func TestAdminAPI_RuleByIDMethodNotAllowed(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPatch, "/api/rules/r1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}
```

- [ ] **Step 4: Run all tests**

```bash
go test ./internal/adapters/cfg/restapi/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/cfg/restapi/server.go internal/adapters/cfg/restapi/server_test.go
git commit -m "feat: add GET by ID, PUT simulation, and POST reload endpoints"
```

---

## Task 2: OpenAPI spec + GET /api/openapi.json

**Context:** Serves the admin API spec for documentation and future tooling. Embedded in the binary via `//go:embed`. `ui.go` already handles static file embedding — follow the same pattern.

**Files:**
- Create: `internal/adapters/cfg/restapi/openapi.yaml`
- Modify: `internal/adapters/cfg/restapi/ui.go`
- Modify: `internal/adapters/cfg/restapi/server_test.go`

- [ ] **Step 1: Write failing test**

Add to `server_test.go`:

```go
func TestAdminAPI_OpenAPI(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	body := w.Body.String()
	assert.Contains(t, body, "openapi")
	assert.Contains(t, body, "/api/rules")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/adapters/cfg/restapi/... -run TestAdminAPI_OpenAPI -v
```

Expected: FAIL — 404.

- [ ] **Step 3: Create openapi.yaml**

Create `internal/adapters/cfg/restapi/openapi.yaml`:

```yaml
openapi: "3.0.3"
info:
  title: Mockwave Admin API
  version: "0.2.0"
  description: >
    HTTP API for managing rules, simulations, metrics, and unmatched requests
    in a Mockwave instance. All endpoints are served on the admin port (default 9090).

servers:
  - url: http://localhost:9090
    description: Local instance

paths:
  /api/health:
    get:
      summary: Health check
      operationId: health
      responses:
        "200":
          description: Service is healthy
          content:
            application/json:
              schema:
                type: object
                properties:
                  status:
                    type: string
                    example: ok

  /api/rules:
    get:
      summary: List all rules
      operationId: listRules
      responses:
        "200":
          description: Array of rules
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/Rule"
    post:
      summary: Create a rule
      operationId: createRule
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/Rule"
      responses:
        "201":
          description: Rule created
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Rule"
        "400":
          $ref: "#/components/responses/BadRequest"
        "422":
          $ref: "#/components/responses/UnprocessableEntity"

  /api/rules/{id}:
    parameters:
      - name: id
        in: path
        required: true
        schema:
          type: string
    get:
      summary: Get a rule by ID
      operationId: getRule
      responses:
        "200":
          description: Rule found
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Rule"
        "404":
          $ref: "#/components/responses/NotFound"
    put:
      summary: Update (replace) a rule
      operationId: updateRule
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/Rule"
      responses:
        "200":
          description: Rule updated
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Rule"
        "400":
          $ref: "#/components/responses/BadRequest"
        "422":
          $ref: "#/components/responses/UnprocessableEntity"
    delete:
      summary: Delete a rule
      operationId: deleteRule
      responses:
        "204":
          description: Rule deleted
        "404":
          $ref: "#/components/responses/NotFound"

  /api/simulations:
    get:
      summary: List all simulations
      operationId: listSimulations
      responses:
        "200":
          description: Array of simulations
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/Simulation"
    post:
      summary: Create a simulation
      operationId: createSimulation
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/Simulation"
      responses:
        "201":
          description: Simulation created
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Simulation"
        "400":
          $ref: "#/components/responses/BadRequest"

  /api/simulations/{id}:
    parameters:
      - name: id
        in: path
        required: true
        schema:
          type: string
    get:
      summary: Get a simulation by ID
      operationId: getSimulation
      responses:
        "200":
          description: Simulation found
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Simulation"
        "404":
          $ref: "#/components/responses/NotFound"
    put:
      summary: Update (replace) a simulation
      operationId: updateSimulation
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/Simulation"
      responses:
        "200":
          description: Simulation updated
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Simulation"
        "400":
          $ref: "#/components/responses/BadRequest"
    delete:
      summary: Delete a simulation
      operationId: deleteSimulation
      responses:
        "204":
          description: Simulation deleted
        "404":
          $ref: "#/components/responses/NotFound"

  /api/metrics:
    get:
      summary: Current metrics snapshot
      operationId: getMetrics
      responses:
        "200":
          description: Metrics snapshot
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/MetricsSnapshot"

  /api/metrics/stream:
    get:
      summary: SSE stream of metrics (one event per second)
      operationId: streamMetrics
      responses:
        "200":
          description: SSE stream
          content:
            text/event-stream:
              schema:
                type: string

  /api/unmatched:
    get:
      summary: List captured unmatched requests
      operationId: listUnmatched
      responses:
        "200":
          description: Array of unmatched requests
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
    delete:
      summary: Clear unmatched request buffer
      operationId: clearUnmatched
      responses:
        "204":
          description: Buffer cleared

  /api/reload:
    post:
      summary: Trigger hot-reload from store
      operationId: reload
      responses:
        "204":
          description: Reload triggered

components:
  schemas:
    MatchCriteria:
      type: object
      properties:
        protocol:
          type: string
          enum: [http, graphql, soap, grpc]
        method:
          type: string
          example: GET
        path:
          type: string
          example: /users/*
        headers:
          type: object
          additionalProperties:
            type: string
        query:
          type: object
          additionalProperties:
            type: string
        body:
          type: object
          additionalProperties:
            type: string

    WeightedBucket:
      type: object
      required: [weight, action]
      properties:
        weight:
          type: integer
          minimum: 1
        action:
          type: string
          enum: [simulate, forward]
        simulation_id:
          type: string

    Rule:
      type: object
      required: [id, match, buckets]
      properties:
        id:
          type: string
        name:
          type: string
        match:
          $ref: "#/components/schemas/MatchCriteria"
        buckets:
          type: array
          items:
            $ref: "#/components/schemas/WeightedBucket"
        forward_url:
          type: string

    HTTPResponse:
      type: object
      properties:
        status:
          type: integer
          example: 200
        headers:
          type: object
          additionalProperties:
            type: string
        body:
          description: Any JSON value
        delay_ms:
          type: integer

    Simulation:
      type: object
      required: [id, protocol]
      properties:
        id:
          type: string
        protocol:
          type: string
          enum: [http, graphql, soap, grpc]
        response:
          $ref: "#/components/schemas/HTTPResponse"
        script:
          type: string
          description: Optional JavaScript (goja) run on every matched request
        soap_envelope:
          type: string
        grpc_message:
          type: string
        grpc_status:
          type: integer

    RuleMetric:
      type: object
      properties:
        rule_id:
          type: string
        rule_name:
          type: string
        hits:
          type: integer
        p95_ms:
          type: number

    MetricsSnapshot:
      type: object
      properties:
        at:
          type: string
          format: date-time
        total_requests:
          type: integer
        misses:
          type: integer
        rules:
          type: array
          items:
            $ref: "#/components/schemas/RuleMetric"

  responses:
    BadRequest:
      description: Invalid JSON or request body
      content:
        application/json:
          schema:
            type: object
            properties:
              error:
                type: string
    NotFound:
      description: Resource not found
      content:
        application/json:
          schema:
            type: object
            properties:
              error:
                type: string
    UnprocessableEntity:
      description: Validation error
      content:
        application/json:
          schema:
            type: object
            properties:
              error:
                type: string
```

- [ ] **Step 4: Embed openapi.yaml and serve GET /api/openapi.json**

Read `internal/adapters/cfg/restapi/ui.go` first to understand the embed pattern, then add to it:

```go
//go:embed openapi.yaml
var openapiYAML []byte
```

Add a new function `serveOpenAPI(mux)` and call it from `NewMux` in `server.go` (or handle inline in ui.go — follow whatever pattern ui.go uses).

Add to `server.go` in `NewMux`:

```go
mux.HandleFunc("/api/openapi.json", api.openapiHandler)
```

Add handler:

```go
func (a *adminAPI) openapiHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "method not allowed")
		return
	}
	// Convert YAML to JSON on the fly using encoding/json + gopkg.in/yaml.v3
	var v any
	if err := yaml.Unmarshal(openapiYAML, &v); err != nil {
		writeError(w, 500, "openapi parse error: "+err.Error())
		return
	}
	writeJSON(w, 200, v)
}
```

Add import `"gopkg.in/yaml.v3"` (already in go.mod as indirect dep). Add `openapiYAML` embed var to `ui.go` (same file as other embed vars). Update `server.go` imports to include `"gopkg.in/yaml.v3"`.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/adapters/cfg/restapi/... -v
```

Expected: all PASS including `TestAdminAPI_OpenAPI`.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/cfg/restapi/openapi.yaml internal/adapters/cfg/restapi/ui.go internal/adapters/cfg/restapi/server.go internal/adapters/cfg/restapi/server_test.go
git commit -m "feat: add OpenAPI spec embedded at GET /api/openapi.json"
```

---

## Task 3: Add mcp-go + admin HTTP client

**Context:** `internal/mcp/client.go` is a thin HTTP client over the admin API. Tested with `httptest.NewServer` — no real Mockwave needed.

**Files:**
- Modify: `go.mod`
- Create: `internal/mcp/client.go`
- Create: `internal/mcp/client_test.go`

- [ ] **Step 1: Add mcp-go dependency**

```bash
go get github.com/mark3labs/mcp-go@latest
go mod tidy
```

Verify it appeared in `go.mod`:

```bash
grep "mark3labs/mcp-go" go.mod
```

Expected: a line like `github.com/mark3labs/mcp-go v0.x.x`.

- [ ] **Step 2: Write failing client tests**

Create `internal/mcp/client_test.go`:

```go
package mcp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mockwavemcp "github.com/mockwave/mockwave/internal/mcp"
	"github.com/mockwave/mockwave/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeAdminServer() *httptest.Server {
	mux := http.NewServeMux()

	// Rules
	mux.HandleFunc("/api/rules", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			rules := []domain.Rule{{ID: "r1", Name: "R1", Match: domain.MatchCriteria{Path: "/foo"}}}
			json.NewEncoder(w).Encode(rules)
		case http.MethodPost:
			var rule domain.Rule
			json.NewDecoder(r.Body).Decode(&rule)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(rule)
		}
	})
	mux.HandleFunc("/api/rules/", func(w http.ResponseWriter, r *http.Request) {
		rule := domain.Rule{ID: "r1", Match: domain.MatchCriteria{Path: "/foo"}}
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(rule)
		case http.MethodPut:
			var r2 domain.Rule
			json.NewDecoder(r.Body).Decode(&r2)
			json.NewEncoder(w).Encode(r2)
		case http.MethodDelete:
			w.WriteHeader(204)
		}
	})

	// Simulations
	mux.HandleFunc("/api/simulations", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			sims := []domain.Simulation{{ID: "s1", Protocol: "http"}}
			json.NewEncoder(w).Encode(sims)
		case http.MethodPost:
			var sim domain.Simulation
			json.NewDecoder(r.Body).Decode(&sim)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(sim)
		}
	})
	mux.HandleFunc("/api/simulations/", func(w http.ResponseWriter, r *http.Request) {
		sim := domain.Simulation{ID: "s1", Protocol: "http"}
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(sim)
		case http.MethodPut:
			var s2 domain.Simulation
			json.NewDecoder(r.Body).Decode(&s2)
			json.NewEncoder(w).Encode(s2)
		case http.MethodDelete:
			w.WriteHeader(204)
		}
	})

	// Observability
	mux.HandleFunc("/api/metrics", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"total_requests": 5})
	})
	mux.HandleFunc("/api/unmatched", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		json.NewEncoder(w).Encode([]any{})
	})
	mux.HandleFunc("/api/reload", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	return httptest.NewServer(mux)
}

func TestClient_ListRules(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	rules, err := c.ListRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "r1", rules[0].ID)
}

func TestClient_GetRule(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	rule, err := c.GetRule("r1")
	require.NoError(t, err)
	assert.Equal(t, "r1", rule.ID)
}

func TestClient_CreateRule(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	r := domain.Rule{ID: "r2", Match: domain.MatchCriteria{Path: "/bar"}}
	out, err := c.CreateRule(r)
	require.NoError(t, err)
	assert.Equal(t, "r2", out.ID)
}

func TestClient_UpdateRule(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	r := domain.Rule{ID: "r1", Match: domain.MatchCriteria{Path: "/updated"}}
	out, err := c.UpdateRule("r1", r)
	require.NoError(t, err)
	assert.Equal(t, "/updated", out.Match.Path)
}

func TestClient_DeleteRule(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.DeleteRule("r1")
	require.NoError(t, err)
}

func TestClient_ListSimulations(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	sims, err := c.ListSimulations()
	require.NoError(t, err)
	require.Len(t, sims, 1)
	assert.Equal(t, "s1", sims[0].ID)
}

func TestClient_GetSimulation(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	sim, err := c.GetSimulation("s1")
	require.NoError(t, err)
	assert.Equal(t, "s1", sim.ID)
}

func TestClient_CreateSimulation(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	s := domain.Simulation{ID: "s2", Protocol: "http"}
	out, err := c.CreateSimulation(s)
	require.NoError(t, err)
	assert.Equal(t, "s2", out.ID)
}

func TestClient_UpdateSimulation(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	s := domain.Simulation{Protocol: "graphql"}
	out, err := c.UpdateSimulation("s1", s)
	require.NoError(t, err)
	assert.Equal(t, "graphql", out.Protocol)
}

func TestClient_DeleteSimulation(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.DeleteSimulation("s1")
	require.NoError(t, err)
}

func TestClient_GetMetrics(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	raw, err := c.GetMetrics()
	require.NoError(t, err)
	assert.Contains(t, string(raw), "total_requests")
}

func TestClient_ListUnmatched(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	raw, err := c.ListUnmatched()
	require.NoError(t, err)
	assert.NotNil(t, raw)
}

func TestClient_ClearUnmatched(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.ClearUnmatched()
	require.NoError(t, err)
}

func TestClient_Reload(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.Reload()
	require.NoError(t, err)
}

func TestClient_Health(t *testing.T) {
	srv := fakeAdminServer()
	defer srv.Close()
	c := mockwavemcp.NewClient(srv.URL)
	err := c.Health()
	require.NoError(t, err)
}

func TestClient_Health_Unreachable(t *testing.T) {
	c := mockwavemcp.NewClient("http://127.0.0.1:1")
	err := c.Health()
	assert.Error(t, err)
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./internal/mcp/... -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 4: Implement client.go**

Create `internal/mcp/client.go`:

```go
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/mockwave/mockwave/domain"
)

// Client makes HTTP requests to a Mockwave admin API.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient creates a Client targeting adminURL (e.g. "http://localhost:9090").
func NewClient(adminURL string) *Client {
	return &Client{baseURL: adminURL, http: &http.Client{}}
}

// --- Rules ---

func (c *Client) ListRules() ([]domain.Rule, error) {
	var rules []domain.Rule
	return rules, c.get("/api/rules", &rules)
}

func (c *Client) GetRule(id string) (*domain.Rule, error) {
	var rule domain.Rule
	return &rule, c.get("/api/rules/"+id, &rule)
}

func (c *Client) CreateRule(r domain.Rule) (*domain.Rule, error) {
	var out domain.Rule
	return &out, c.post("/api/rules", r, &out)
}

func (c *Client) UpdateRule(id string, r domain.Rule) (*domain.Rule, error) {
	var out domain.Rule
	return &out, c.put("/api/rules/"+id, r, &out)
}

func (c *Client) DeleteRule(id string) error {
	return c.doDelete("/api/rules/" + id)
}

// --- Simulations ---

func (c *Client) ListSimulations() ([]domain.Simulation, error) {
	var sims []domain.Simulation
	return sims, c.get("/api/simulations", &sims)
}

func (c *Client) GetSimulation(id string) (*domain.Simulation, error) {
	var sim domain.Simulation
	return &sim, c.get("/api/simulations/"+id, &sim)
}

func (c *Client) CreateSimulation(s domain.Simulation) (*domain.Simulation, error) {
	var out domain.Simulation
	return &out, c.post("/api/simulations", s, &out)
}

func (c *Client) UpdateSimulation(id string, s domain.Simulation) (*domain.Simulation, error) {
	var out domain.Simulation
	return &out, c.put("/api/simulations/"+id, s, &out)
}

func (c *Client) DeleteSimulation(id string) error {
	return c.doDelete("/api/simulations/" + id)
}

// --- Observability ---

func (c *Client) GetMetrics() (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.get("/api/metrics", &raw)
}

func (c *Client) ListUnmatched() (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.get("/api/unmatched", &raw)
}

func (c *Client) ClearUnmatched() error {
	return c.doDelete("/api/unmatched")
}

func (c *Client) Reload() error {
	return c.post("/api/reload", nil, nil)
}

func (c *Client) Health() error {
	var raw json.RawMessage
	return c.get("/api/health", &raw)
}

// --- HTTP helpers ---

func (c *Client) get(path string, out any) error {
	resp, err := c.http.Get(c.baseURL + path)
	if err != nil {
		return err
	}
	return decode(resp, out)
}

func (c *Client) post(path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := c.http.Post(c.baseURL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	return decode(resp, out)
}

func (c *Client) put(path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	return decode(resp, out)
}

func (c *Client) doDelete(path string) error {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func decode(resp *http.Response, out any) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/mcp/... -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/mcp/client.go internal/mcp/client_test.go
git commit -m "feat: add admin HTTP client for MCP server"
```

---

## Task 4: MCP tool handlers

**Context:** Each tool maps one-to-one to a `Client` method. Tools parse string params from `req.Params.Arguments`, call the client, JSON-encode the result.

**Files:**
- Create: `internal/mcp/tools.go`

- [ ] **Step 1: Create tools.go**

Create `internal/mcp/tools.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mockwave/mockwave/domain"
)

// jsonResult marshals v as indented JSON and wraps it in a text tool result.
func jsonResult(v any) (*mcpsdk.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return mcpsdk.NewToolResultText(string(b)), nil
}

func stringParam(req mcpsdk.CallToolRequest, name string) (string, error) {
	v, ok := req.Params.Arguments[name]
	if !ok {
		return "", fmt.Errorf("missing required parameter: %s", name)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s must be a string", name)
	}
	return s, nil
}

// jsonParam deserialises the named argument (which may be a map or string) into dst.
func jsonParam(req mcpsdk.CallToolRequest, name string, dst any) error {
	v, ok := req.Params.Arguments[name]
	if !ok {
		return fmt.Errorf("missing required parameter: %s", name)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("parameter %s: %w", name, err)
	}
	return json.Unmarshal(b, dst)
}

// --- Rule tools ---

func handleListRules(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		rules, err := c.ListRules()
		if err != nil {
			return nil, err
		}
		return jsonResult(rules)
	}
}

func handleGetRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return nil, err
		}
		rule, err := c.GetRule(id)
		if err != nil {
			return nil, err
		}
		return jsonResult(rule)
	}
}

func handleCreateRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var rule domain.Rule
		if err := jsonParam(req, "rule", &rule); err != nil {
			return nil, err
		}
		out, err := c.CreateRule(rule)
		if err != nil {
			return nil, err
		}
		return jsonResult(out)
	}
}

func handleUpdateRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return nil, err
		}
		var rule domain.Rule
		if err := jsonParam(req, "rule", &rule); err != nil {
			return nil, err
		}
		out, err := c.UpdateRule(id, rule)
		if err != nil {
			return nil, err
		}
		return jsonResult(out)
	}
}

func handleDeleteRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return nil, err
		}
		if err := c.DeleteRule(id); err != nil {
			return nil, err
		}
		return mcpsdk.NewToolResultText("rule " + id + " deleted"), nil
	}
}

// --- Simulation tools ---

func handleListSimulations(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		sims, err := c.ListSimulations()
		if err != nil {
			return nil, err
		}
		return jsonResult(sims)
	}
}

func handleGetSimulation(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return nil, err
		}
		sim, err := c.GetSimulation(id)
		if err != nil {
			return nil, err
		}
		return jsonResult(sim)
	}
}

func handleCreateSimulation(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var sim domain.Simulation
		if err := jsonParam(req, "simulation", &sim); err != nil {
			return nil, err
		}
		out, err := c.CreateSimulation(sim)
		if err != nil {
			return nil, err
		}
		return jsonResult(out)
	}
}

func handleUpdateSimulation(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return nil, err
		}
		var sim domain.Simulation
		if err := jsonParam(req, "simulation", &sim); err != nil {
			return nil, err
		}
		out, err := c.UpdateSimulation(id, sim)
		if err != nil {
			return nil, err
		}
		return jsonResult(out)
	}
}

func handleDeleteSimulation(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return nil, err
		}
		if err := c.DeleteSimulation(id); err != nil {
			return nil, err
		}
		return mcpsdk.NewToolResultText("simulation " + id + " deleted"), nil
	}
}

// --- Observability tools ---

func handleGetMetrics(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		raw, err := c.GetMetrics()
		if err != nil {
			return nil, err
		}
		return mcpsdk.NewToolResultText(string(raw)), nil
	}
}

func handleListUnmatched(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		raw, err := c.ListUnmatched()
		if err != nil {
			return nil, err
		}
		return mcpsdk.NewToolResultText(string(raw)), nil
	}
}

func handleClearUnmatched(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if err := c.ClearUnmatched(); err != nil {
			return nil, err
		}
		return mcpsdk.NewToolResultText("unmatched buffer cleared"), nil
	}
}

func handleReload(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if err := c.Reload(); err != nil {
			return nil, err
		}
		return mcpsdk.NewToolResultText("reload triggered"), nil
	}
}

func handleHealth(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if err := c.Health(); err != nil {
			return nil, fmt.Errorf("admin API unreachable: %w", err)
		}
		return mcpsdk.NewToolResultText("ok"), nil
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/mcp/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/tools.go
git commit -m "feat: add MCP tool handlers for rules, simulations, and observability"
```

---

## Task 5: MCP server setup

**Context:** `NewServer` registers all tools and returns an `*server.MCPServer` ready to be served via stdio.

**Files:**
- Create: `internal/mcp/server.go`

- [ ] **Step 1: Create server.go**

Create `internal/mcp/server.go`:

```go
package mcp

import (
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewServer builds an MCP server wired to the Mockwave admin API at adminURL.
func NewServer(adminURL, version string) *server.MCPServer {
	c := NewClient(adminURL)

	s := server.NewMCPServer("mockwave", version,
		server.WithToolCapabilities(true),
	)

	// Rules
	s.AddTool(
		mcpsdk.NewTool("list_rules",
			mcpsdk.WithDescription("List all rules in the Mockwave instance"),
		),
		handleListRules(c),
	)
	s.AddTool(
		mcpsdk.NewTool("get_rule",
			mcpsdk.WithDescription("Get a single rule by ID"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Rule ID")),
		),
		handleGetRule(c),
	)
	s.AddTool(
		mcpsdk.NewTool("create_rule",
			mcpsdk.WithDescription("Create a new rule. The 'rule' parameter is a JSON object matching the Rule schema (id, name, match, buckets, forward_url)."),
			mcpsdk.WithObject("rule", mcpsdk.Required(), mcpsdk.Description("Rule object")),
		),
		handleCreateRule(c),
	)
	s.AddTool(
		mcpsdk.NewTool("update_rule",
			mcpsdk.WithDescription("Replace an existing rule. Overwrites the rule with the given ID."),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Rule ID to update")),
			mcpsdk.WithObject("rule", mcpsdk.Required(), mcpsdk.Description("Updated rule object")),
		),
		handleUpdateRule(c),
	)
	s.AddTool(
		mcpsdk.NewTool("delete_rule",
			mcpsdk.WithDescription("Delete a rule by ID"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Rule ID to delete")),
		),
		handleDeleteRule(c),
	)

	// Simulations
	s.AddTool(
		mcpsdk.NewTool("list_simulations",
			mcpsdk.WithDescription("List all simulations in the Mockwave instance"),
		),
		handleListSimulations(c),
	)
	s.AddTool(
		mcpsdk.NewTool("get_simulation",
			mcpsdk.WithDescription("Get a single simulation by ID"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Simulation ID")),
		),
		handleGetSimulation(c),
	)
	s.AddTool(
		mcpsdk.NewTool("create_simulation",
			mcpsdk.WithDescription("Create a new simulation. The 'simulation' parameter is a JSON object with fields: id, protocol, response (status/headers/body/delay_ms), script, soap_envelope, grpc_message, grpc_status."),
			mcpsdk.WithObject("simulation", mcpsdk.Required(), mcpsdk.Description("Simulation object")),
		),
		handleCreateSimulation(c),
	)
	s.AddTool(
		mcpsdk.NewTool("update_simulation",
			mcpsdk.WithDescription("Replace an existing simulation. Overwrites the simulation with the given ID."),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Simulation ID to update")),
			mcpsdk.WithObject("simulation", mcpsdk.Required(), mcpsdk.Description("Updated simulation object")),
		),
		handleUpdateSimulation(c),
	)
	s.AddTool(
		mcpsdk.NewTool("delete_simulation",
			mcpsdk.WithDescription("Delete a simulation by ID"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Simulation ID to delete")),
		),
		handleDeleteSimulation(c),
	)

	// Observability
	s.AddTool(
		mcpsdk.NewTool("get_metrics",
			mcpsdk.WithDescription("Get current metrics snapshot: total requests, per-rule hits, p95 latency"),
		),
		handleGetMetrics(c),
	)
	s.AddTool(
		mcpsdk.NewTool("list_unmatched",
			mcpsdk.WithDescription("List requests that matched no rule — useful for discovering what to mock next"),
		),
		handleListUnmatched(c),
	)
	s.AddTool(
		mcpsdk.NewTool("clear_unmatched",
			mcpsdk.WithDescription("Clear the unmatched request buffer"),
		),
		handleClearUnmatched(c),
	)
	s.AddTool(
		mcpsdk.NewTool("reload",
			mcpsdk.WithDescription("Trigger hot-reload from the store (picks up externally modified rules/simulations)"),
		),
		handleReload(c),
	)
	s.AddTool(
		mcpsdk.NewTool("health",
			mcpsdk.WithDescription("Check if the Mockwave admin API is reachable"),
		),
		handleHealth(c),
	)

	return s
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/mcp/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/server.go
git commit -m "feat: add MCP server with all rule, simulation, and observability tools"
```

---

## Task 6: Cobra subcommand + wire into main

**Context:** `mockwave mcp --admin-url <url>` starts the stdio MCP server. Add to root command alongside `start`, `validate`, `version`.

**Files:**
- Create: `cmd/mockwave/mcp.go`
- Modify: `cmd/mockwave/main.go`

- [ ] **Step 1: Create mcp.go**

Create `cmd/mockwave/mcp.go`:

```go
package main

import (
	"github.com/mark3labs/mcp-go/server"
	mockwavemcp "github.com/mockwave/mockwave/internal/mcp"
	"github.com/spf13/cobra"
)

func mcpCmd() *cobra.Command {
	var adminURL string

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start MCP server — bridges Claude Code to a Mockwave admin API",
		Long: `Starts a stdio MCP server that allows AI assistants (e.g. Claude Code) to
manage rules and simulations on a Mockwave instance.

Register in ~/.claude/mcp.json:

  {
    "mcpServers": {
      "mockwave-local": {
        "command": "mockwave",
        "args": ["mcp", "--admin-url", "http://localhost:9090"]
      }
    }
  }`,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := mockwavemcp.NewServer(adminURL, version)
			return server.ServeStdio(s)
		},
	}

	cmd.Flags().StringVar(&adminURL, "admin-url", "http://localhost:9090",
		"Mockwave admin API base URL (local or remote)")
	_ = cmd.MarkFlagRequired("admin-url")

	return cmd
}
```

- [ ] **Step 2: Register mcpCmd in main.go**

In `cmd/mockwave/main.go`, update `rootCmd()`:

```go
func rootCmd() *cobra.Command {
	root := &cobra.Command{Use: "mockwave", Short: "Multi-protocol mock server"}
	root.AddCommand(startCmd(), validateCmd(), versionCmd(), mcpCmd())
	return root
}
```

- [ ] **Step 3: Build and verify the subcommand exists**

```bash
go build -o /tmp/mockwave-test ./cmd/mockwave/
/tmp/mockwave-test mcp --help
```

Expected output contains:
```
Start MCP server — bridges Claude Code to a Mockwave admin API
...
Flags:
      --admin-url string   Mockwave admin API base URL (local or remote) (default "http://localhost:9090")
```

- [ ] **Step 4: Run all tests**

```bash
go test ./... 
```

Expected: all PASS, no regressions.

- [ ] **Step 5: Check coverage still ≥80%**

```bash
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out | grep total
```

Expected: `total: ... 80%+`

- [ ] **Step 6: Commit**

```bash
git add cmd/mockwave/mcp.go cmd/mockwave/main.go
git commit -m "feat: add mockwave mcp subcommand for Claude Code integration"
```

---

## Usage after implementation

Register in `~/.claude/mcp.json`:

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

Claude Code will expose tools namespaced as `mockwave-local__create_rule`, `mockwave-sandbox__list_simulations`, etc.
