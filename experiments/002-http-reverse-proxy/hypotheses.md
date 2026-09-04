# Hypotheses: Experiment 002 — HTTP Reverse Proxy & Connection Pooling

## H1 — Upstream Connection Reuse
Enabling upstream HTTP connection reuse (HTTP Keep-Alive with `net/http.Transport` connection pooling) will dramatically reduce the number of physical TCP 3-way handshakes and socket allocations compared to intentionally disabling keep-alive (`DisableKeepAlives = true`).
- *Prediction*: Under persistent keep-alive, `requests_per_connection >> 1` (e.g. 50–500 reqs/conn). When keep-alive is disabled, `requests_per_connection == 1.0`.

## H2 — Connection Reuse Performance under Concurrency
Under concurrent traffic ($c \ge 10$), upstream connection reuse will significantly improve overall throughput (RPS) and suppress tail latency (p95/p99) by amortizing TCP handshake costs and avoiding OS socket allocation bottlenecks and ephemeral port churn identified in Stage 1.

## H3 — Multi-Edge Connection Pooling
When the reverse proxy balances traffic across multiple upstream hosts ($N=3$ edges), the proxy connection pool will maintain separate idle connection sets per host governed by `MaxIdleConnsPerHost`. Total connections across the pool will scale proportionally with active concurrency per target.

## H4 — One Slow Edge Latency Impact under Static Routing
A static, non-adaptive selection policy (e.g. Round-Robin) will blindly distribute equal request volume to all healthy edges regardless of processing latency differences. When one edge degrades (e.g. 100ms artificial delay), static routing will cause severe head-of-line and tail latency inflation (p95/p99) across the entire cluster, motivating the necessity for latency-aware routing in Stage 3.

## H5 — Edge Failure Detection and Traffic Exclusion
An active health-checking state machine operating across 4 distinct states (`HEALTHY`, `DEGRADED`, `UNHEALTHY`, `RECOVERING`) will detect an abrupt edge failure within its configured probe interval ($<50\text{ms}$), transition the target to `UNHEALTHY`, and immediately exclude it from the proxy selection pool, allowing 100% of subsequent traffic to be served by surviving edge nodes without total outage.
