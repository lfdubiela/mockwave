# Running Mockwave on Kubernetes

Sizing guidance, a reference manifest, and notes on running more than one
replica.

The short version: Mockwave is far cheaper than most people size it for.
A single core served **~72,000 req/s** in testing, so a typical workload
needs a fraction of a core and well under 128Mi of memory. The one setting
that is easy to get wrong, and costly when you do, is `GOMAXPROCS`.

- [Resource sizing](#resource-sizing)
- [Set GOMAXPROCS explicitly](#set-gomaxprocs-explicitly)
- [Reference manifest](#reference-manifest)
- [Probes](#probes)
- [Running multiple replicas](#running-multiple-replicas)
- [Measured numbers](#measured-numbers)
- [How these numbers were produced](#how-these-numbers-were-produced)

---

## Resource sizing

At 1,000 req/s against a 50-rule config, a single pod used roughly **8% of
one core** and **14MiB** of memory. Idle sat near 9MiB.

```yaml
resources:
  requests:
    cpu: 100m
    memory: 64Mi
  limits:
    cpu: "1"
    memory: 128Mi
```

`requests.cpu: 100m` covers about 1,000 req/s with room to spare. The `1`
CPU limit exists for burst headroom, not because steady state needs it.

Scale `requests.cpu` roughly linearly with throughput and keep the limit at
least 2-3x the request. Memory is close to flat in throughput — it tracks
config size and enabled features far more than traffic — so raise
`memory` when you turn on matched capture or event capture, not when
traffic grows.

## Set GOMAXPROCS explicitly

**Do not rely on the Go runtime inferring your CPU limit.** In testing, a
container limited to 0.5 CPU still reported `GOMAXPROCS=2`, matching the
machine's core count rather than the cgroup quota — even though the quota
was plainly readable at `/sys/fs/cgroup/cpu.max`.

On a large node this is the difference between a working pod and a badly
behaved one. With `limits.cpu: 500m` on a 64-core node, Go would start 64
scheduler threads for half a core of quota, and CFS would throttle it in
100ms windows. The symptom is latency spikes at low CPU utilisation, which
is a miserable thing to debug.

Set it from the CPU limit, rounded up to a whole number:

```yaml
env:
  - name: GOMAXPROCS
    value: "1"
```

Verify on your own cluster rather than trusting this note — runtime
behaviour here varies by Go version and container runtime:

```bash
kubectl exec deploy/mockwave -- cat /sys/fs/cgroup/cpu.max
```

If you would rather not hardcode it, the downward API can derive it:

```yaml
env:
  - name: GOMAXPROCS
    valueFrom:
      resourceFieldRef:
        resource: limits.cpu   # rounds up to the nearest whole core
```

## Reference manifest

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mockwave
spec:
  replicas: 2
  selector:
    matchLabels: { app: mockwave }
  template:
    metadata:
      labels: { app: mockwave }
    spec:
      containers:
        - name: mockwave
          image: ghcr.io/lfdubiela/mockwave:latest
          args:
            - start
            - --store=dynamodb
            - --dynamo-region=us-east-1
            - --reload-interval=15s
            - --port=8080
            - --admin-port=9090
          ports:
            - { name: mock,  containerPort: 8080 }
            - { name: admin, containerPort: 9090 }
          env:
            - name: GOMAXPROCS
              value: "1"
          resources:
            requests: { cpu: 100m, memory: 64Mi }
            limits:   { cpu: "1",  memory: 128Mi }
          readinessProbe:
            httpGet: { path: /api/health, port: admin }
            initialDelaySeconds: 2
            periodSeconds: 5
          livenessProbe:
            httpGet: { path: /api/health, port: admin }
            initialDelaySeconds: 10
            periodSeconds: 20
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities: { drop: ["ALL"] }
---
apiVersion: v1
kind: Service
metadata:
  name: mockwave
spec:
  selector: { app: mockwave }
  ports:
    - { name: mock,  port: 8080, targetPort: mock }
    - { name: admin, port: 9090, targetPort: admin }
```

The published image is built `FROM gcr.io/distroless/static-debian12:nonroot`,
so it already runs as non-root with no shell. It needs no writable
filesystem when using a remote store, which is why `readOnlyRootFilesystem`
is safe above. With `--store=json` you will need the config mounted, and a
`ConfigMap` volume is the natural fit.

## Probes

`GET /api/health` on the **admin port** returns `{"status":"ok"}`.

Cold start is quick: about **1.7s** from container start to first served
request, including the initial full load of rules from DynamoDB. A
2-second `initialDelaySeconds` is comfortable; there is no need for a
startup probe at typical config sizes.

One caveat worth knowing: `/api/health` reports that the admin server is
listening. It does **not** assert that rules finished loading. In practice
the mock port starts serving at essentially the same moment, but if you
need a stricter readiness signal, probe `GET /api/rules` on the admin port
and check for a non-empty array.

## Running multiple replicas

Mockwave already synchronises rules across pods, provided you use a store
that supports it.

Every pod keeps rules in memory and serves requests from that snapshot, so
**the store is never on the request path**. Instead, each pod polls the
store's config version on `--reload-interval` (default 15s) and rebuilds
its in-memory pipeline only when the version changed. A rule written
through any pod's admin API becomes visible on every other pod within one
interval.

| Store | Cross-pod sync | Notes |
|-------|----------------|-------|
| `dynamodb` | Yes | Recommended for multi-replica |
| `mongo` | Yes | Recommended for multi-replica |
| `cosmos` | **No** | Does not report a config version; pods will not converge |
| `json` | **No** | Local file, never polled |

With `--store=json`, each pod owns a private copy of the config. That is
fine when the rules are immutable and shipped in a `ConfigMap`, but writes
through the admin API will silently diverge between pods. Do not use it for
multi-replica setups where rules change at runtime.

Two consequences worth planning around:

- **Convergence takes up to one `--reload-interval`.** Lower it if you need
  rules to propagate faster; the poll is a single cheap key lookup
  (measured at ~0.6ms) and had no measurable effect on request latency.
- **Poll load scales with replica count.** Ten pods at the default interval
  is ~40 store reads per minute purely for version checks. Cheap, but it is
  the one cost that grows as you scale out.

Because a single pod handles a large amount of traffic, prefer scaling
replicas for **availability and rollout safety**, not throughput. If you do
use an HPA, target CPU — memory is nearly flat with load and makes a poor
scaling signal.

## Measured numbers

50-rule config, HTTP, one pod:

| Metric | Value |
|--------|-------|
| Saturation throughput, 1 core | ~72,000 req/s |
| Saturation throughput, 2 cores | ~97,000 req/s |
| CPU at 1,000 req/s | ~8% of one core |
| Memory at 1,000 req/s | ~14MiB |
| Memory idle | ~9MiB |
| p50 / p99 at 1,000 req/s | ~135µs / ~700µs |
| Cold start to first request | ~1.7s |
| Container image size | ~50MB |

Store backend made no meaningful difference to request performance —
DynamoDB and a JSON file landed within 3% of each other on throughput and
within noise on latency, which is what you would expect given the store is
not on the request path.

## How these numbers were produced

Measured on an ARM64 macOS host, Docker Desktop, against a 50-rule config
with an in-memory DynamoDB Local as the backing store. Treat them as
order-of-magnitude guidance for capacity planning, not as a specification:

- The host is not Linux on x86, and CPU limits were approximated with
  `GOMAXPROCS` for the higher-throughput runs. That does **not** reproduce
  CFS throttling, which is exactly the failure mode the `GOMAXPROCS`
  section warns about.
- DynamoDB Local removes real network latency. That does not change request
  throughput, since the store is off the request path, but it does make
  cold start optimistic — a real AWS endpoint will be slower.
- Your rule count, response sizes, and enabled features all move these
  numbers. Re-measure before committing to tight limits.
