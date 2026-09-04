# Stage 2 Learning Notes — HTTP Reverse Proxy, Connection Pooling & Edge Topology

## 1. Initial Assumptions

Before implementing Stage 2, our assumptions based on standard documentation and Stage 1 TCP findings were:
1. **HTTP vs TCP**: HTTP is essentially framed application-level messaging over a persistent TCP byte stream. We expected `net/http.Transport` to handle connection reuse invisibly and efficiently.
2. **Keep-Alive Cost**: We hypothesized that disabling keep-alive would re-introduce the exact OS socket allocation bottleneck and latency spikes measured in Stage 1 ($c=100$ collapsing due to port churn).
3. **Reverse Proxying**: We assumed proxying is straightforward byte forwarding, but anticipated that without granular timing ($T_0 \to T_5$), upstream latency and proxy processing overhead would be conflated.
4. **Static Routing Limitations**: We assumed that static round-robin would degrade non-linearly when any single edge node exhibited latency skew.

---

## 2. Implementation Architecture

We built FlashFlow Stage 2 with clean domain separation:

```text
flashflow/
├── cmd/
│   ├── http-origin/           # Standalone Origin HTTP server
│   ├── edge/                  # Thin forwarding Edge server (Identity + delay hook)
│   ├── proxy/                 # FlashFlow Reverse Proxy server CLI
│   ├── experiment-002a1/      # Exp 002-A1: Single upstream connection reuse isolation
│   ├── experiment-002a2/      # Exp 002-A2: Multi-edge connection pooling
│   ├── experiment-002b/       # Exp 002-B: Edge failure detection & recovery
│   ├── experiment-002c/       # Exp 002-C: Three-edge baseline throughput
│   └── experiment-002d/       # Exp 002-D: One slow edge latency impact
│
├── internal/
│   ├── clock/                 # Minimal Clock interface & WallClock implementation
│   ├── httpx/                 # Opaque Request ID, JSON envelopes, T0-T5 timings, benchmark client
│   ├── transport/             # TrackedTransport with atomic dial/conn metrics & pool knobs
│   ├── health/                # 4-state health machine (HEALTHY, DEGRADED, UNHEALTHY, RECOVERING)
│   ├── proxy/                 # ReverseProxy, RequestRecords, TargetSelector interface
│   └── topology/              # OriginServer and EdgeServer components
│
└── deployments/
    ├── Dockerfile             # Multi-binary container build
    └── docker-compose/
        └── stage2.yml         # 3-Edge + Origin + Proxy bridge network topology
```

---

## 3. First-Principles Answers to Stage 2 Learning Questions

### HTTP Semantics over TCP
- **What does an HTTP server add on top of TCP?**
  TCP is a raw byte stream with no application boundaries. HTTP/1.1 adds a structured request/response framing syntax consisting of a request line (`METHOD URI PROTOCOL`), headers (`Key: Value\r\n`), header terminator (`\r\n\r\n`), and payload boundaries determined by `Content-Length` or `Transfer-Encoding: chunked`.
- **What happens when HTTP uses a persistent TCP connection?**
  Instead of executing a 3-way handshake (`SYN` $\to$ `SYN-ACK` $\to$ `ACK`) and 4-way teardown (`FIN` $\to$ `ACK` $\to$ `FIN` $\to$ `ACK`) per transaction, the TCP socket remains open in `ESTABLISHED` state. Subsequent HTTP requests serialize across the socket, eliminating handshake latency and kernel socket creation overhead.

### Reverse Proxying & Request Tracing
- **What does a reverse proxy actually do?**
  It terminates client-side downstream HTTP connections, inspects request headers/paths, selects an upstream target via a routing decision, constructs an upstream HTTP request, forwards payload bytes, receives the upstream response, and relays status, headers, and body back to the client.
- **Where should Request IDs be generated and latency measured?**
  Request IDs must be generated at the proxy ingress boundary if missing (`X-Request-ID`), preserved across all upstream tiers, and echoed back in the response for end-to-end correlation.
  Latency must be decomposed into explicit intervals:
  - $T_{proxy} = (T_4 - T_1) - (T_3 - T_2)$ (pure proxy CPU/scheduling time)
  - $T_{upstream} = T_3 - T_2$ (network transmission + edge/origin processing)
  - $T_{e2e} = T_5 - T_0$ (full client-perceived round-trip)

### Connection Pooling Mechanics (`http.Transport`)
- **What does `http.Transport` actually pool?**
  `http.Transport` maintains an internal idle connection cache (`map[connectKey][]*persistConn`). When a request completes and the response body is fully read and closed, the underlying TCP connection is returned to the idle pool rather than closed.
- **What do `MaxIdleConnsPerHost` and `MaxConnsPerHost` control?**
  - `MaxIdleConnsPerHost`: Maximum number of idle (keep-alive) sockets preserved per destination host. If a host completes requests with high concurrency and drops to idle, connections beyond this limit are closed (`FIN`).
  - `MaxConnsPerHost`: Maximum total connections (active + idle) allowed to a host. Requests beyond this limit block until an active connection becomes available.
- **How does connection reuse show up in metrics?**
  Expressed through **Requests per Upstream Connection**:
  $$\text{RequestsPerConn} = \frac{\text{Successful Requests}}{\text{Successful TCP Dials}}$$
  With Keep-Alive: $\text{RequestsPerConn} \gg 1$ (e.g. 50–550 in our experiments).
  Without Keep-Alive: $\text{RequestsPerConn} = 1.00$.

### Multi-Tier Metrics Separation
Because the FlashFlow topology consists of two network hops (`Proxy -> Edge` and `Edge -> Origin`), transport connection metrics must be independently labeled:
- `proxy_upstream_dials`, `proxy_upstream_active_conns`, `proxy_upstream_idle_conns`
- `edge_origin_dials`, `edge_origin_active_conns`, `edge_origin_idle_conns`
Conflating them would obscure where connection bottlenecks or socket leaks occur.

### Health State Machine & Failure Dynamics
- **4-State Architecture**:
  - `HEALTHY`: 0 consecutive probe failures and normal error rates.
  - `DEGRADED`: Active probes succeed, but application traffic error rate exceeds threshold (e.g. $>20\%$ 5xx responses). In Stage 2, degraded nodes remain eligible for traffic because no load-reduction weighting exists yet; Stage 3+ routing policies will dynamically downweight degraded edges.
  - `UNHEALTHY`: Active probe fails $\ge \text{UnhealthyFailThreshold}$ consecutive times. Excluded from target selection.
  - `RECOVERING`: Unhealthy target passes $\ge 1$ probe, acting as an observation gate before full restoration to `HEALTHY`. The full graduated traffic-ramp (10% $\to$ 50% $\to$ 100%) will be implemented in Stage 3 alongside dynamic routing.
- **Cumulative Error Rate Baseline**: Stage 2 evaluates application health using cumulative lifetime error statistics for simplicity. Stage 3 will upgrade this to rolling-window error tracking to ensure recovery from past transient errors.
- **Clock Abstraction vs Prober Scheduling**: While the health state machine transitions evaluate time purely via the `Clock` interface, the prober loop itself runs on a system ticker in Stage 2. Full discrete-event scheduler ownership will be integrated in the Virtual-Time Engine.
- **Why automatic blind retry is dangerous**: Blindly retrying failed requests against an already saturated or failing origin causes **retry storms**, amplifying queue depth $\rho \to 1$ and triggering cascading failures.

### Critical Discovery for Stage 3: Active Sockets $\neq$ In-Flight Application Load
- `transport.ActiveConns` measures **physical open TCP sockets** in the pool.
- However, for HTTP/1.1 with connection pooling, a single idle open TCP connection can sit ready while another connection is actively processing a serialized request.
- **Rule for Stage 3 Least Connections**: A true Least Connections router must track **in-flight application requests** per edge, rather than equating socket count with workload.

---

## 4. Key Experimental Observations

### 1. Connection Reuse Amortization (Exp 002-A1)
- At $c=100$, Keep-Alive delivered **8,445.2 RPS** with **5.17ms p50** using only **64 physical TCP dials** across 1,000 requests (16.4 reqs/conn).
- Disabling Keep-Alive dropped throughput to **2,760.4 RPS** (67.3% reduction) and inflated p50 to **28.87ms** and p99 to **97.28ms** while burning **1,050 dials** (1.00 req/conn).

### 2. Multi-Edge Scaling (Exp 002-A2 & 002-C)
- Across 3 edge nodes, Keep-Alive pooling delivered **10,159.9 RPS** at $c=100$, maintaining low p50 (**2.48ms**) and amortizing connection dials across all 3 edge hosts.

### 3. Rapid Failure Exclusion (Exp 002-B)
- When Edge B was stopped abruptly during active traffic, the background prober detected the outage in **12.86ms**, marked Edge B `UNHEALTHY`, and immediately rerouted 100% of subsequent traffic to Edge A and Edge C with zero dropped requests.

### 4. The Fatal Flaw of Static Routing (Exp 002-D)
- When Edge C degraded to 100ms processing delay, static round-robin continued blindly routing 33.3% of all requests to Edge C.
- Cluster throughput collapsed from **4,610 RPS down to 478 RPS** (89.6% loss), and p95 latency spiked from **21.97ms to 102.27ms**.
- *Note:* Edge delay is strictly **simulated application processing time**, distinct from network link degradation which will be handled via `tc netem`.

---

## 5. Architectural Implications & Bridge into Stage 3

Stage 2 proved that transport connection pooling, hop-by-hop header stripping, and health state machines function reliably. However, Experiment 002-D conclusively demonstrated that **static routing cannot handle heterogeneous edge conditions**.

This establishes the direct mandate for:
**Stage 3 — Server-Side Scaling & Routing Policies: Round Robin $\to$ Weighted Round Robin $\to$ Least Connections $\to$ Latency-Aware EWMA $\to$ Power of Two Choices (P2C) $\to$ Adaptive Six-Signal Scoring.**
