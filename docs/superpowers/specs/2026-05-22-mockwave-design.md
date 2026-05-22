# Mockwave — Mock Server Design Spec

**Date:** 2026-05-22  
**Status:** Approved  

---

## Overview

Mockwave is an open-source mock server supporting HTTP/REST, GraphQL, SOAP, and gRPC protocols. It intercepts requests, matches them against configured rules, and either returns a simulated response or proxies the request to a real upstream service — with percentile-based traffic splitting.

Distributed as a single compiled Go binary. Configurable via JSON file, REST API, or embedded web UI. Deployable as a CLI tool or in EKS.

---

## Goals

- Single binary, zero runtime dependencies
- Multi-protocol: HTTP, GraphQL, SOAP, gRPC
- Percentile traffic splitting: N% simulate, M% forward (proxy)
- Request matching by path glob, method, headers, query params, body
- Payload manipulation via JavaScript (goja)
- Multiple storage backends for rules/simulations
- Hot-reload: changes apply in <1ms without dropping in-flight requests
- EKS-ready: ConfigMap/env config, IRSA credentials, health probes

---

## Non-Goals (v1)

- Authentication/authorization on admin port (recommend network policy)
- Stateful session tracking across requests
- Recording/replay of real traffic
- Dynamic .proto compilation at runtime

---

## Architecture: Hexagonal (Ports & Adapters)

Core domain is isolated from protocols and storage. All external dependencies are adapters behind interfaces.

```
mockwave/
├── cmd/mockwave/           ← Cobra CLI entry point
├── internal/domain/
│   ├── pipeline/           ← pipeline engine, stage interface, context
│   ├── matching/           ← rule condition evaluator
│   ├── simulation/         ← simulation loader, template renderer
│   └── ports/              ← DataStore, ProtocolHandler interfaces
├── internal/adapters/in/
│   ├── httprest/           ← net/http handler
│   ├── graphql/            ← gqlgen dynamic schema
│   ├── soap/               ← net/http + encoding/xml
│   └── grpc/               ← google.golang.org/grpc
├── internal/adapters/out/
│   ├── jsonfile/           ← local JSON file store
│   ├── dynamodb/           ← AWS SDK v2
│   ├── mongodb/            ← mongo-driver
│   ├── cosmosdb/           ← Cosmos DB via MongoDB wire protocol
│   └── bigtable/           ← cloud.google.com/go/bigtable
├── internal/adapters/cfg/
│   ├── fileconfig/         ← JSON file loader + fsnotify watcher
│   └── restapi/            ← admin REST API + SSE log stream
├── internal/scripting/     ← goja JS engine wrapper
├── web/                    ← React + Vite SPA → go:embed
└── deploy/eks/             ← Helm chart + manifests
```

---

## Domain Model

### Rule

```json
{
  "id": "rule-get-user",
  "name": "GET /users/:id with x-cid match",
  "match": {
    "protocol": "http",
    "method":   "GET",
    "path":     "/users/*",
    "headers":  { "x-cid": "12345" },
    "query":    {},
    "body":     {}
  },
  "forward_url": "https://real-api.internal/users",
  "buckets": [
    { "weight": 1,  "action": "simulate", "simulation_id": "sim-error-500" },
    { "weight": 99, "action": "forward" }
  ]
}
```

**MatchCriteria fields:**
- `protocol`: `http` | `graphql` | `soap` | `grpc`
- `method`: HTTP verb (ignored for gRPC/SOAP); for GraphQL: operation type (`query`|`mutation`); for gRPC: full method name (`/package.Service/Method`)
- `path`: glob pattern (`/users/*`, `/api/**`); for GraphQL: operation name; for SOAP: SOAPAction header value
- `headers`: exact key/value map (applies to all protocols)
- `query`: exact key/value query param map (HTTP only)
- `body`: JSONPath → exact value map, e.g. `{"$.userId": "42", "$.type": "admin"}` — all entries must match (AND)

**WeightedBucket fields:**
- `weight`: relative weight (values don't need to sum to 100)
- `action`: `simulate` | `forward`
- `simulation_id`: required when `action = simulate`

`forward_url` at rule level is the proxy target for any bucket with `action = forward`.

### Simulation

```json
{
  "id": "sim-user-ok",
  "protocol": "http",
  "response": {
    "status":   200,
    "headers":  { "content-type": "application/json" },
    "body":     { "id": "{{request.path[1]}}", "name": "mock" },
    "delay_ms": 0
  },
  "script": "response.body.cid = request.headers['x-cid']; return response;"
}
```

For gRPC: `proto_message` (JSON string — deserialized to protobuf via `--grpc-proto` descriptor at startup) and `grpc_status` (`codes.Code` int). The gRPC handler is a generic pass-through that accepts any message; the `.proto` file is used only for body deserialization and matching, not for code generation at runtime.  
For SOAP: `soap_envelope` (raw XML string returned as-is).

### Matching Priority

Rules evaluated in order of specificity (highest → lowest):
1. Header match (e.g. `x-cid: 12345`)
2. Exact path match
3. Path glob match
4. Method + path
5. Catch-all (`/**`)

No match → `404` with body `{"error":"no rule matched","path":"..."}`.

---

## Pipeline Engine

Five sequential stages. Each stage receives and mutates `PipelineContext`. If any stage returns an error, the pipeline halts and returns an error response.

```go
type PipelineContext struct {
    Request      NormalizedRequest
    Response     *MockResponse
    Matched      *Rule
    SimulationID string
    ShouldForward bool
    Aborted      bool
}

type Stage interface {
    Name() string
    Execute(ctx context.Context, pctx *PipelineContext) error
}
```

### Stage 1 — ConditionMatchStage
Evaluates all rules against `pctx.Request` in priority order. Sets `pctx.Matched`. Aborts with 404 if no rule matches.

### Stage 2 — PercentileRouterStage
Randomly selects a `WeightedBucket` from `pctx.Matched.Buckets` using relative weights. Seed per-request for reproducibility. Sets `pctx.SimulationID` or `pctx.ShouldForward = true`.

### Stage 3 — SimulationStage
Skipped if `ShouldForward = true`. Loads `Simulation` from `DataStore` by `SimulationID`. Renders body template (`{{request.path[1]}}` etc.). Sets `pctx.Response`.

### Stage 4 — ScriptStage (goja)
Skipped if `ShouldForward = true` or `Simulation.Script` is empty. Executes JavaScript in sandboxed goja runtime. Exposes `request` and `response` objects. Script must return modified `response`.

```js
// available in script
request.headers["x-cid"]     // read header
response.body.userId = "..."  // modify body
response.status = 422         // override status
response.delay_ms = 2000      // add latency
return response;
```

### Stage 5 — ForwardStage
If `ShouldForward = true`: HTTP proxy to `rule.ForwardURL`, passing original headers and body as-is. Upstream response becomes `pctx.Response`.  
If `ShouldForward = false`: returns `pctx.Response` from SimulationStage.

---

## Adapters

### Outbound Port — DataStore

```go
type DataStore interface {
    GetRules() ([]Rule, error)
    GetSimulation(id string) (*Simulation, error)
    SaveRule(r Rule) error
    SaveSimulation(s Simulation) error
    DeleteRule(id string) error
}
```

Implementations: `jsonfile`, `dynamodb`, `mongodb`, `cosmosdb`, `bigtable`.

### Inbound Port — ProtocolHandler

```go
type ProtocolHandler interface {
    Start(addr string) error
    Stop(ctx context.Context) error
}
```

Each handler normalizes its protocol's request into `NormalizedRequest` before entering the pipeline, and converts `MockResponse` back to the protocol's wire format.

---

## Hot-Reload

Rules and simulations are held in memory behind a `sync.RWMutex`. In-flight requests hold an `RLock`. Reload acquires a `Lock` only to swap the pointer — no requests are dropped.

Two reload triggers:
1. **API change**: `POST /api/rules` → saves to DataStore → channel notification → engine swaps in <1ms
2. **File change**: `fsnotify` watches config file → detects write → full reload

---

## Admin API (:admin-port, default 9090)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/rules` | list rules |
| POST | `/api/rules` | create rule (hot-reload) |
| PUT | `/api/rules/:id` | update rule |
| DELETE | `/api/rules/:id` | delete rule |
| GET | `/api/simulations` | list simulations |
| POST | `/api/simulations` | create simulation |
| PUT | `/api/simulations/:id` | update simulation |
| DELETE | `/api/simulations/:id` | delete simulation |
| GET | `/api/logs` | last N requests (ring buffer, 1000 entries) |
| GET | `/api/logs/stream` | SSE stream of live requests |
| POST | `/api/config/reload` | force reload of JSON config file |
| GET | `/api/health` | liveness + readiness for EKS probes |

**Log entry schema:**
```json
{
  "ts":           "2026-05-22T19:00:00Z",
  "protocol":     "http",
  "method":       "GET",
  "path":         "/users/42",
  "headers":      { "x-cid": "12345" },
  "matched_rule": "rule-get-user",
  "bucket_hit":   "simulate",
  "simulation":   "sim-user-ok",
  "status":       200,
  "latency_ms":   12,
  "forwarded":    false
}
```

---

## Admin UI (Embedded SPA)

React + Vite, compiled to `web/dist/`, embedded via `go:embed web/dist`. Served from admin port at `/`.

Pages:
- **Rules** — list, create, edit rules with visual condition builder
- **Simulations** — manage mock responses, JS script editor
- **Logs** — live SSE stream with match highlighting
- **Settings** — datasource, ports, active protocols

No auth in v1. Admin port should be restricted to cluster-internal access via network policy.

---

## CLI

```
mockwave start
  -f, --config string       JSON config file path
  --store string            json|dynamodb|mongodb|cosmosdb|bigtable (default: json)
  --port int                mock server port (default: 8080)
  --admin-port int          admin UI + API port (default: 9090)
  --protocols strings       comma-separated: http,graphql,soap,grpc (default: http)
  --grpc-proto string       path to .proto file for gRPC

mockwave validate -f config.json
mockwave export --store dynamodb
mockwave version
```

---

## EKS Deployment

Single container, two ports. Config via environment variables or Kubernetes ConfigMap.

```yaml
env:
  - name: MOCKWAVE_STORE
    value: dynamodb
  - name: MOCKWAVE_AWS_REGION
    value: us-east-1
  - name: MOCKWAVE_PORT
    value: "8080"
  - name: MOCKWAVE_ADMIN_PORT
    value: "9090"

ports:
  - containerPort: 8080  # mock traffic
  - containerPort: 9090  # admin UI + API

livenessProbe:
  httpGet: { path: /api/health, port: 9090 }
readinessProbe:
  httpGet: { path: /api/health, port: 9090 }
```

AWS credentials via IRSA (IAM Roles for Service Accounts) — no secrets in pod.

**Dockerfile:** multi-stage — Node build (SPA) → Go build → `scratch` base image → ~15MB final image.

`deploy/eks/` contains a Helm chart with `values.yaml` for store, ports, protocol selection, and resource limits.

---

## Key Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/dop251/goja` | JavaScript engine (scripting) |
| `google.golang.org/grpc` | gRPC server |
| `github.com/99designs/gqlgen` | GraphQL server |
| `github.com/aws/aws-sdk-go-v2` | DynamoDB adapter |
| `go.mongodb.org/mongo-driver` | MongoDB + Cosmos adapter |
| `cloud.google.com/go/bigtable` | Bigtable adapter |
| `github.com/fsnotify/fsnotify` | File watch for hot-reload |
| `golang.org/x/sync` | RWMutex utilities |
