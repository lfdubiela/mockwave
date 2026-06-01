# Mockwave

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8.svg)](https://golang.org)

**Mockwave** is an open-source, multi-protocol mock server. Define rules and simulations in JSON, manage them through the browser UI, or let an AI assistant do it — Mockwave responds to HTTP, GraphQL, SOAP, and gRPC requests with weighted traffic splitting, dynamic JavaScript responses, real-time metrics, and a built-in MCP server for Claude Code integration.

---

## Features

- **Multi-protocol** — HTTP REST, GraphQL, SOAP, gRPC (reflection-free, descriptor-based)
- **Traffic splitting** — weighted buckets per rule (e.g., 90% mock / 10% forward to real service)
- **Dynamic scripting** — per-simulation JavaScript (goja) for computed responses
- **Real-time admin UI** — browser dashboard at `localhost:9090` for rule/simulation CRUD, live metrics, and unmatched request capture
- **Multiple store backends** — JSON file, DynamoDB, MongoDB, Azure Cosmos DB (MongoDB API)
- **Hot reload** — update rules without restarting via the admin API
- **AI integration (MCP)** — `mockwave mcp` exposes a Model Context Protocol server so Claude Code can create rules, manage simulations, and auto-generate mocks from any OpenAPI 2.0/3.0 spec
- **Embeddable library** — `store.DataStore` interface is public; bring your own backend

---

## Quick Start

### Homebrew (macOS / Linux)

```bash
brew tap lfdubiela/mockwave
brew install mockwave

# Verify installation
mockwave version
```

```bash
# Create a minimal config
cat > config.json <<'EOF'
{
  "rules": [
    {
      "id": "hello",
      "name": "Hello World",
      "match": { "method": "GET", "path": "/hello" },
      "buckets": [{ "weight": 100, "action": "simulate", "simulation_id": "hello-sim" }]
    }
  ],
  "simulations": [
    {
      "id": "hello-sim",
      "protocol": "http",
      "response": { "status": 200, "body": { "message": "Hello from Mockwave!" } }
    }
  ]
}
EOF

# Start on default ports (mock :8080, admin :9090)
mockwave start -f config.json

# Custom ports
mockwave start -f config.json --port 3000 --admin-port 3001
```

```bash
# Test it
curl http://localhost:8080/hello
# {"message":"Hello from Mockwave!"}

# Open the admin UI
open http://localhost:9090
```

```bash
# Upgrade
brew upgrade mockwave
```

### MCP (Claude Code integration)

With Mockwave running, add to `~/.claude/mcp.json` (create if it doesn't exist):

```json
{
  "mcpServers": {
    "mockwave-local": {
      "command": "mockwave",
      "args": ["mcp", "--admin-url", "http://localhost:9090"]
    }
  }
}
```

Then ask Claude Code to do the work:

```
"Generate mocks from https://petstore3.swagger.io/api/v3/openapi.json"
"Create a mock for POST /checkout that returns 201 with an order ID"
"What requests are hitting mockwave but not matching any rule?"
```

### Binary

```bash
# Download the latest release binary (replace OS/ARCH as needed)
curl -Lo mockwave https://github.com/lfdubiela/mockwave/releases/download/v0.2.0/mockwave-linux-amd64
chmod +x mockwave

# Start the server
./mockwave start -f config.json
```

### Docker

```bash
docker run -p 8080:8080 -p 9090:9090 \
  -v $(pwd)/config.json:/config.json \
  ghcr.io/lfdubiela/mockwave:v0.1.0 \
  start -f /config.json
```

---

## CLI Reference

```
mockwave [command]

Commands:
  start     Start the mock server
  validate  Validate a config file without starting the server
  version   Print version
  mcp       Start MCP server for AI assistant integration (Claude Code, etc.)

Flags (start):
  -f, --config string            Path to JSON config file (required for --store=json)
      --port int                 Mock server port (default 8080)
      --admin-port int           Admin UI/API port (default 9090)
      --protocols string         Comma-separated: http,graphql,soap,grpc (default "http")
      --grpc-port int            gRPC server port (default 50051)
      --grpc-proto string        Path to compiled .pb descriptor for gRPC proto conversion

  # Store backend
      --store string             Storage backend: json|dynamodb|mongo|cosmos (default "json")

  # DynamoDB
      --dynamo-rules-table string  DynamoDB table for rules (default "mockwave-rules")
      --dynamo-sims-table string   DynamoDB table for simulations (default "mockwave-simulations")
      --dynamo-region string       AWS region (default "us-east-1")
      --dynamo-endpoint string     Custom endpoint, e.g. http://localhost:8000

  # MongoDB
      --mongo-uri string          MongoDB connection URI (default "mongodb://localhost:27017")
      --mongo-db string           MongoDB database name (default "mockwave")

  # Cosmos DB
      --cosmos-uri string         Cosmos DB connection string (MongoDB API)
      --cosmos-db string          Cosmos DB database name (default "mockwave")
```

### Examples

```bash
# HTTP + GraphQL on the same port
mockwave start -f config.json --protocols http,graphql

# All protocols
mockwave start -f config.json --protocols http,graphql,soap,grpc --grpc-proto service.pb

# DynamoDB backend (uses default AWS credential chain)
mockwave start --store dynamodb --dynamo-region eu-west-1

# Local DynamoDB (e.g. DynamoDB Local)
mockwave start --store dynamodb --dynamo-endpoint http://localhost:8000

# MongoDB backend
mockwave start --store mongo --mongo-uri mongodb://user:pass@host:27017/mydb

# Validate a config file
mockwave validate config.json
```

---

## Config File Format

The JSON config file has two top-level arrays: `rules` and `simulations`.

```json
{
  "rules": [ ...Rule... ],
  "simulations": [ ...Simulation... ]
}
```

### Rule

```json
{
  "id": "string (required, unique)",
  "name": "string (display label)",
  "match": {
    "protocol": "http | graphql | soap | grpc",
    "method":   "GET | POST | PUT | DELETE | PATCH | ...",
    "path":     "/users/* (glob supported)",
    "headers":  { "X-Tenant": "acme" },
    "query":    { "version": "2" },
    "body":     { "$.type": "order" }
  },
  "buckets": [
    {
      "weight":        100,
      "action":        "simulate | forward",
      "simulation_id": "sim-id (required when action=simulate)"
    }
  ],
  "forward_url": "https://real-api.example.com (required when any bucket has action=forward)"
}
```

**Path globs:** `*` matches a single segment, `**` matches any number of segments.
- `/users/*` matches `/users/123` but not `/users/123/orders`
- `/api/**` matches any path under `/api/`

**Traffic splitting:** weights are relative. `[{weight:90,…}, {weight:10,…}]` routes 90% to the first bucket and 10% to the second. Weights do not need to sum to 100.

**Forwarding:** set `action: "forward"` and provide `forward_url`. The request is proxied to `forward_url + original_path` with original headers and body.

### Simulation

```json
{
  "id":       "string (required, unique)",
  "protocol": "http | graphql | soap | grpc",

  "response": {
    "status":   200,
    "headers":  { "Content-Type": "application/json" },
    "body":     { "any": "JSON value" },
    "delay_ms": 150
  },

  "script": "// optional JS — return value overrides response.body\nreturn { computed: request.path };",

  "soap_envelope": "<soap:Envelope>...</soap:Envelope>",

  "grpc_message": "{ \"userId\": \"123\" }",
  "grpc_status":  0
}
```

---

## Protocols

### HTTP REST

Enabled by default. Routes by `method` + `path`. Response body supports Go template variables:

```json
{
  "id": "user-get",
  "protocol": "http",
  "response": {
    "status": 200,
    "body": { "id": "{{.PathParam \"id\"}}", "name": "Alice" }
  }
}
```

### GraphQL

Enable with `--protocols http,graphql`. Mockwave parses the `operationName` from the request body and matches against `match.path` (treated as operation name prefix/glob).

```json
{
  "id": "gql-user",
  "match": { "protocol": "graphql", "path": "GetUser" },
  "buckets": [{ "weight": 100, "action": "simulate", "simulation_id": "gql-user-sim" }]
}
```

### SOAP

Enable with `--protocols http,soap`. Mockwave reads the SOAP action from the `SOAPAction` header and routes accordingly. Set `soap_envelope` in the simulation to return a raw XML envelope.

```json
{
  "id": "soap-create-order",
  "match": { "protocol": "soap", "path": "CreateOrder" },
  "buckets": [{ "weight": 100, "action": "simulate", "simulation_id": "create-order-sim" }]
}
```

```json
{
  "id": "create-order-sim",
  "protocol": "soap",
  "soap_envelope": "<soap:Envelope xmlns:soap=\"http://schemas.xmlsoap.org/soap/envelope/\"><soap:Body><CreateOrderResponse><orderId>42</orderId></CreateOrderResponse></soap:Body></soap:Envelope>"
}
```

### gRPC

Enable with `--protocols http,grpc`. Requires a compiled protobuf descriptor:

```bash
# Compile your .proto to a descriptor
protoc --descriptor_set_out=service.pb --include_imports service.proto

# Start with the descriptor
mockwave start -f config.json --protocols http,grpc --grpc-proto service.pb
```

Set `grpc_message` (JSON representation of the proto response) and `grpc_status` (gRPC status code, 0 = OK) in the simulation:

```json
{
  "id": "get-user-sim",
  "protocol": "grpc",
  "grpc_message": "{ \"userId\": \"abc\", \"name\": \"Alice\" }",
  "grpc_status": 0
}
```

---

## Store Backends

| Backend | Flag | Notes |
|---------|------|-------|
| JSON file | `--store json -f config.json` | Default. File is read on start; hot-reloaded via admin API. |
| DynamoDB | `--store dynamodb` | Uses AWS default credential chain. Tables must exist with PK `id` (String). |
| MongoDB | `--store mongo` | Tested with MongoDB 6+. |
| Cosmos DB | `--store cosmos` | Uses MongoDB wire protocol. `ssl=true` and `retryWrites=false` applied automatically. |

### DynamoDB Setup

Create two tables (substitute region/table names as needed):

```bash
aws dynamodb create-table --table-name mockwave-rules \
  --attribute-definitions AttributeName=id,AttributeType=S \
  --key-schema AttributeName=id,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST

aws dynamodb create-table --table-name mockwave-simulations \
  --attribute-definitions AttributeName=id,AttributeType=S \
  --key-schema AttributeName=id,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST
```

---

## Admin UI

Open `http://localhost:9090` in a browser. The UI is served from the admin port and requires no external dependencies.

| Tab | What it does |
|-----|--------------|
| **Rules** | List, create, edit, and delete rules |
| **Simulations** | List, add (JSON editor), and delete simulations |
| **Metrics** | Live request counters and per-rule hit rates (SSE, updates every second) |
| **Unmatched** | Requests that matched no rule — click "Create Rule" to pre-fill the rule form |

---

## Admin REST API

All endpoints are on the admin port (default `:9090`).

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/rules` | List all rules |
| `POST` | `/api/rules` | Create a rule |
| `GET` | `/api/rules/:id` | Get a rule |
| `PUT` | `/api/rules/:id` | Update a rule |
| `DELETE` | `/api/rules/:id` | Delete a rule |
| `GET` | `/api/simulations` | List all simulations |
| `POST` | `/api/simulations` | Create a simulation |
| `GET` | `/api/simulations/:id` | Get a simulation |
| `PUT` | `/api/simulations/:id` | Update a simulation |
| `DELETE` | `/api/simulations/:id` | Delete a simulation |
| `GET` | `/api/openapi.json` | OpenAPI 3.0 spec (JSON) |
| `GET` | `/api/metrics` | Current metrics snapshot (JSON) |
| `GET` | `/api/metrics/stream` | SSE stream — one event per second |
| `GET` | `/api/unmatched` | List captured unmatched requests |
| `DELETE` | `/api/unmatched` | Clear unmatched request buffer |
| `POST` | `/api/reload` | Trigger hot-reload from store |
| `GET` | `/api/health` | `{"status":"ok"}` |

### Metrics snapshot shape

```json
{
  "at": "2026-05-24T12:00:00Z",
  "total_requests": 1042,
  "misses": 13,
  "rules": [
    {
      "rule_id":   "hello",
      "rule_name": "Hello World",
      "hits":      1029,
      "p95_ms":    42.3
    }
  ]
}
```

---

## AI Integration (MCP)

`mockwave mcp` exposes a [Model Context Protocol](https://modelcontextprotocol.io) server that lets Claude Code (and other MCP-compatible AI assistants) create, inspect, and delete rules and simulations on any Mockwave instance — local or remote.

### How it works

```
Claude Code
    │  stdio (stdin/stdout)
    ▼
mockwave mcp (local process, spawned by Claude Code)
    │  HTTP
    ▼
Mockwave Admin API (:9090) — localhost OR remote (EKS, sandbox, etc.)
```

`mockwave mcp` runs locally and bridges MCP tool calls to HTTP requests against `--admin-url`. The admin URL can point anywhere — a local dev instance or a shared sandbox running in the cloud.

### Setup

Ensure `mockwave` is on your `$PATH` (e.g. via `brew install mockwave`), then add to `~/.claude/mcp.json`:

```json
{
  "mcpServers": {
    "mockwave-local": {
      "command": "mockwave",
      "args": ["mcp", "--admin-url", "http://localhost:9090"]
    }
  }
}
```

Multiple instances are supported — Claude Code namespaces the tools automatically:

```json
{
  "mcpServers": {
    "mockwave-local": {
      "command": "mockwave",
      "args": ["mcp", "--admin-url", "http://localhost:9090"]
    },
    "mockwave-sandbox": {
      "command": "mockwave",
      "args": ["mcp", "--admin-url", "https://mockwave.sandbox.example.com"]
    }
  }
}
```

> **Security:** The Mockwave admin API has no authentication. When pointing `--admin-url` at a remote instance, ensure the admin port is protected by a firewall or reverse proxy.

### Available tools

| Tool | Description |
|------|-------------|
| `list_rules` | List all rules |
| `get_rule` | Get a rule by ID |
| `create_rule` | Create a new rule |
| `update_rule` | Replace a rule |
| `delete_rule` | Delete a rule |
| `list_simulations` | List all simulations |
| `get_simulation` | Get a simulation by ID |
| `create_simulation` | Create a new simulation |
| `update_simulation` | Replace a simulation |
| `delete_simulation` | Delete a simulation |
| `generate_from_openapi` | Auto-generate rules + simulations from an OpenAPI 2.0/3.0 spec (URL or file) |
| `get_metrics` | Current metrics snapshot |
| `list_unmatched` | List requests that matched no rule |
| `clear_unmatched` | Clear the unmatched buffer |
| `reload` | Trigger hot-reload from store |
| `health` | Check admin API reachability |

### Examples

**Create a rule from a natural language description:**
```
You: "Create a mock for GET /orders that returns 200 with an empty orders array"

Claude calls create_simulation → POST /api/simulations
Claude calls create_rule       → POST /api/rules
Claude: "Done. GET http://localhost:8080/orders now returns {"orders":[]}"
```

**Generate mocks from an existing OpenAPI spec:**
```
You: "Generate mocks from https://petstore3.swagger.io/api/v3/openapi.json"

Claude calls generate_from_openapi with the URL
Claude: "Created 18 rules and 18 simulations covering all Petstore endpoints.
         GET /pet/{petId} → 200 {"id":1,"name":"doggie","status":"available"}
         POST /pet        → 201 {"id":1,"name":"doggie"}
         DELETE /pet/{petId} → 200
         ..."
```

**Inspect what's being hit and fix gaps:**
```
You: "What requests are hitting mockwave but not matching any rule?"

Claude calls list_unmatched
Claude: "3 unmatched requests found:
         POST /api/v2/checkout  ← no rule
         GET  /api/v2/cart/99   ← no rule
         Want me to create mocks for these?"
```

**Dynamic script mock via MCP:**
```
You: "Create a mock for GET /users/:id that echoes the ID back in the response"

Claude calls create_simulation with script:
  const id = request.path.split('/').pop();
  return { body: { id: id, name: "User " + id } };
Claude calls create_rule → POST /api/rules
Claude: "Done. GET /users/42 now returns {"id":"42","name":"User 42"}"
```

### Tip — add to CLAUDE.md

Drop this in your project's `CLAUDE.md` to make Claude aware of Mockwave automatically:

```markdown
## Mocking
Mockwave is running at http://localhost:8080 (admin: http://localhost:9090).
Use the `mockwave-local` MCP tools to create or update mocks instead of hardcoding responses.
When a test hits an unmocked endpoint, call `list_unmatched` and create the missing rule.
```

---

## JavaScript Scripting

Set `"script"` on any simulation to run JavaScript (via [goja](https://github.com/dop251/goja)) on every matched request. The script must return an object with at least a `body` key (and optionally `status`, `headers`, `delay_ms`) to override the response.

```json
{
  "id": "dynamic-user",
  "protocol": "http",
  "response": { "status": 200 },
  "script": "const id = request.path.split('/').pop(); return { body: { id: id, ts: Date.now() } };"
}
```

The return object can override any part of the response:

```js
return {
  status: 201,
  headers: { "X-Custom": "value" },
  body: { id: request.path.split('/').pop(), ts: Date.now() },
  delay_ms: 100
};
```

### Extracting path parameters

`request.path` is the raw path string. Use standard JS string methods to extract segments:

```js
// GET /users/42  →  id = "42"
const id = request.path.split('/').pop();
return { body: { id: id } };
```

```js
// GET /org/acme/users/42  →  segments = ["org","acme","users","42"]
const parts = request.path.split('/').filter(Boolean);
const org  = parts[1]; // "acme"
const id   = parts[3]; // "42"
return { body: { org: org, userId: id } };
```

```js
// named segment via regex: /users/:id/orders/:orderId
const match = request.path.match(/\/users\/([^/]+)\/orders\/([^/]+)/);
const userId  = match ? match[1] : null;
const orderId = match ? match[2] : null;
return { body: { userId: userId, orderId: orderId } };
```

```js
// numeric ID anywhere in path
const match = request.path.match(/\/(\d+)/);
const id = match ? parseInt(match[1], 10) : null;
return {
  status: id ? 200 : 404,
  body: id ? { id: id } : { error: "not found" }
};
```

```js
// reflect full path + method back (useful for debugging)
return {
  body: {
    method: request.method,
    path:   request.path,
    parts:  request.path.split('/').filter(Boolean)
  }
};
```

### Using request headers

```js
// bearer token presence check
const auth = request.headers["authorization"] || "";
const token = auth.replace("Bearer ", "");
return {
  status: token ? 200 : 401,
  body: token ? { token: token } : { error: "unauthorized" }
};
```

```js
// tenant routing via custom header
const tenant = request.headers["x-tenant-id"] || "default";
const id = request.path.split('/').pop();
return { body: { tenant: tenant, id: id, source: "mock" } };
```

```js
// echo all headers back (debugging)
return { body: { headers: request.headers } };
```

### Using request body

```js
// body is already parsed when Content-Type is application/json
const name = request.body && request.body.name;
return { body: { message: "Hello, " + (name || "stranger") } };
```

```js
// validate required fields, return 422 if missing
const b = request.body || {};
if (!b.email || !b.name) {
  return { status: 422, body: { error: "email and name are required" } };
}
return { status: 201, body: { id: Math.floor(Math.random() * 10000), email: b.email } };
```

### Combining path + headers + body

```js
// POST /accounts/:accountId/transfers
const accountId = request.path.split('/').filter(Boolean)[1];
const requestId = request.headers["x-request-id"] || "none";
const amount    = request.body && request.body.amount;
return {
  status: 202,
  headers: { "x-request-id": requestId },
  body: {
    transferId: "txn-" + Date.now(),
    from:       accountId,
    amount:     amount,
    status:     "pending"
  }
};
```

Available in the script context:

| Variable | Type | Description |
|----------|------|-------------|
| `request.method` | string | HTTP method (`"GET"`, `"POST"`, …) |
| `request.path` | string | Full path string (e.g. `"/orders/12345"`) |
| `request.headers` | object | Request headers (lowercase keys) |
| `request.body` | object\|null | Parsed JSON body, or `null` |
| `response.status` | number | Current response status (modifiable) |
| `response.body` | object | Current response body (modifiable) |

---

## Custom Store Backend

Implement the `store.DataStore` interface to plug in any storage:

```go
import "github.com/mockwave/mockwave/store"

type MyStore struct{}

var _ store.DataStore = (*MyStore)(nil) // compile-time check

func (s *MyStore) GetRules() ([]domain.Rule, error)              { ... }
func (s *MyStore) GetSimulation(id string) (*domain.Simulation, error) { ... }
func (s *MyStore) ListSimulations() ([]domain.Simulation, error) { ... }
func (s *MyStore) SaveRule(r domain.Rule) error                  { ... }
func (s *MyStore) SaveSimulation(s domain.Simulation) error      { ... }
func (s *MyStore) DeleteRule(id string) error                    { ... }
func (s *MyStore) DeleteSimulation(id string) error              { ... }
```

---

## Observability

Mockwave ships an `observability` package with **Logger**, **Tracer**, and **MetricsRecorder** interfaces. Every method is context-aware — implementations automatically extract `request_id`, `method`, `path`, and `protocol` from the context.

Built-in defaults: `SlogLogger` (JSON to stdout), `NoopTracer`, `NoopMetrics`.

**→ [Observability guide: custom implementations, wiring, zerolog/OTEL/Prometheus examples](docs/observability.md)**

---

## Docker

```bash
# Run with a local config file
docker run -p 8080:8080 -p 9090:9090 \
  -v $(pwd)/config.json:/config.json \
  ghcr.io/lfdubiela/mockwave:v0.1.0 \
  start -f /config.json

# All protocols
docker run -p 8080:8080 -p 9090:9090 -p 50051:50051 \
  -v $(pwd)/config.json:/config.json \
  -v $(pwd)/service.pb:/service.pb \
  ghcr.io/lfdubiela/mockwave:v0.1.0 \
  start -f /config.json --protocols http,graphql,soap,grpc --grpc-proto /service.pb

# DynamoDB backend (IAM role or env vars)
docker run -p 8080:8080 -p 9090:9090 \
  -e AWS_ACCESS_KEY_ID=... \
  -e AWS_SECRET_ACCESS_KEY=... \
  -e AWS_REGION=us-east-1 \
  ghcr.io/lfdubiela/mockwave:v0.1.0 \
  start --store dynamodb
```

### Building locally

```bash
docker build -t mockwave:local .
docker run -p 8080:8080 -p 9090:9090 \
  -v $(pwd)/config.json:/config.json \
  mockwave:local start -f /config.json
```

---

## Building from Source

Requirements: Go 1.21+

```bash
git clone https://github.com/lfdubiela/mockwave.git
cd mockwave

# Build binary
make build          # outputs ./mockwave

# Run tests
make test

# Check coverage (must be ≥80%)
make coverage
```

---

## Contributing

Contributions are welcome. Please open an issue before submitting a large PR.

1. Fork the repo
2. Create a feature branch (`git checkout -b feat/my-feature`)
3. Write tests first (TDD)
4. Ensure `make test` and `make coverage` pass
5. Submit a pull request

---

## License

Mockwave is released under the [MIT License](LICENSE). Free to use, modify, and distribute — commercially or otherwise — with attribution.
