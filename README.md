# Mockwave

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8.svg)](https://golang.org)

**Mockwave** is an open-source, multi-protocol mock server. Define rules and simulations in JSON (or manage them via the browser UI), and Mockwave responds to HTTP, GraphQL, SOAP, and gRPC requests — with real-time metrics and a live admin dashboard.

---

## Features

- **Multi-protocol** — HTTP REST, GraphQL, SOAP, gRPC (reflection-free, descriptor-based)
- **Traffic splitting** — weighted buckets per rule (e.g., 90% mock / 10% forward to real service)
- **Dynamic scripting** — per-simulation JavaScript (goja) for computed responses
- **Real-time admin UI** — browser dashboard at `localhost:9090` for rule/simulation CRUD, live metrics, and unmatched request capture
- **Multiple store backends** — JSON file, DynamoDB, MongoDB, Azure Cosmos DB (MongoDB API)
- **Hot reload** — update rules without restarting via the admin API
- **Embeddable library** — `store.DataStore` interface is public; bring your own backend

---

## Quick Start

### Binary

```bash
# Download the latest release binary (replace OS/ARCH as needed)
curl -Lo mockwave https://github.com/lfdubiela/mockwave/releases/download/v0.1.0/mockwave-linux-amd64
chmod +x mockwave

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

# Start the server
./mockwave start -f config.json
```

```bash
# Test it
curl http://localhost:8080/hello
# {"message":"Hello from Mockwave!"}

# Open the admin UI
open http://localhost:9090
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
| `DELETE` | `/api/simulations/:id` | Delete a simulation |
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

## JavaScript Scripting

Set `"script"` on any simulation to run JavaScript (via [goja](https://github.com/dop251/goja)) on every matched request. The script must return an object with a `body` key (and optionally `status`, `headers`, `delay_ms`) to override the response.

```json
{
  "id": "dynamic-user",
  "protocol": "http",
  "response": { "status": 200 },
  "script": "return { body: { id: request.path[request.path.length-1], ts: Date.now() } };"
}
```

The return object can override any part of the response:

```js
return {
  status: 201,
  headers: { "X-Custom": "value" },
  body: { id: request.path[request.path.length-1], ts: Date.now() },
  delay_ms: 100
};
```

Available in the script context:

| Variable | Type | Description |
|----------|------|-------------|
| `request.method` | string | HTTP method |
| `request.path` | array | Path segments (e.g. `["users","42"]`) |
| `request.headers` | object | Request headers (lowercase keys) |
| `request.body` | string | Raw request body |
| `request.query` | object | Query string parameters |

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
