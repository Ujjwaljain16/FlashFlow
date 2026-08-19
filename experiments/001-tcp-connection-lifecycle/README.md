# Experiment 001: TCP Connection Lifecycle

## 1. Research Question
What is the performance and connection-lifecycle difference between creating a new TCP connection for every request versus reusing persistent TCP connections?

## 2. Hypotheses
See [hypotheses.md](hypotheses.md).

## 3. System Under Test
- **Server**: Raw Go TCP echo server (`cmd/tcp-server`). Each accepted connection handled in a separate goroutine. Atomic connection tracking via `Tracker`.
- **Client**: Raw Go TCP load generator (`cmd/tcp-client`).  Workers use atomic counters; measurement aggregation via sorted latency array.
- **Protocol**: Binary framing `[length (uint32 big-endian)][payload bytes]`.

## 4. Measurement Definitions

> **Application round-trip latency (RTT)**: Wall-clock time from the moment `WriteMessage()` is called until `ReadMessage()` returns the complete response. This is the application-level loopback round-trip and explicitly excludes TCP connection establishment in persistent mode.

> **Connection establishment latency**: Wall-clock time for `net.Dial()` to return a usable connection. This is the cost of the 3-way TCP handshake.

> **End-to-end transaction latency (per-request mode)**: Connection establishment + application RTT. The two are measured separately and can be summed.

> **Throughput (RPS)**: Total successful application messages divided by total benchmark wall-clock duration. Not to be confused with TCP throughput.

> **Percentiles**: Use sorted nearest-rank index selection (`latencies[n*p/100]`). This is a simple approximation; a proper histogram implementation will replace this in a later stage.

## 5. Experimental Setup
All measurements run on a single Windows machine over loopback (`127.0.0.1`). Client and server are in the same process space via the benchmark runner. A **fresh server** is started for each configuration cell to prevent TIME_WAIT socket state from earlier cells contaminating later measurements. Cell execution order is randomised.

## 6. Controlled Variables
- Server implementation
- Client implementation
- Machine (OS, hardware, architecture)
- Payload size per cell
- Request count per cell
- Concurrency per cell
- Protocol framing
- Server configuration

## 7. Changed Variable (Experiment 001)
**Connection Mode**: `per-request` (new TCP socket per application request) vs `persistent` (one socket per concurrent worker, reused across all requests).

## 8. Benchmark Matrix
- **Concurrency**: 1, 10, 100
- **Requests**: 1,000 / 10,000
- **Payload**: 32 / 1,024 bytes
- **Iterations**: 5 measured runs per cell (1 warmup discarded)
- **Total measured invocations**: 120
- **Total discarded warmup invocations**: 24

## 9. Experiment 001-A: Causal Decomposition

To understand *why* persistent connections outperform per-request connections, a separate causal decomposition experiment (`cmd/experiment-001a`) isolates three distinct costs:

| Phase | Measures |
|---|---|
| **A: Dial-only** | TCP 3-way handshake in isolation (no application data) |
| **B: Per-request** | Dial latency + application RTT measured separately |
| **C: Persistent** | Application RTT only (no dial cost per request) |

### Results (loopback, 64-byte payload)

| Concurrency | A: Dial p99 | B: Conn p50 | B: App RTT p50 | C: Persistent RTT p50 |
|---|---|---|---|---|
| 1 | 673 µs | 557 µs | ~0 µs | ~0 µs |
| 10 | 1.13 ms | 588 µs | 1.00 ms | ~0 µs |
| 100 | 1.50 ms | 667 µs | **9.38 ms** | ~0 µs |

**Key observation**: At c=100, the per-request application RTT jumps to 9.4ms (p50) even though the loopback RTT at c=1 is sub-millisecond. This is OS scheduling and ephemeral port-buffer contention. The persistent RTT at the same concurrency remains near-zero. The dial overhead alone (~667µs) does not explain the 9ms gap — the penalty is systemic, not just additive.

## 10. Repeatability
5 iterations per cell. Variance generally < 5% for persistent mode. Per-request mode at high concurrency showed higher run-to-run variance due to residual OS socket state (TIME_WAIT).

## 11. TIME_WAIT: A Key Finding

During the original single-server benchmark run, sequential cells contaminated each other: per-request workloads left thousands of sockets in `TIME_WAIT`, degrading subsequent measurements. This is not a code bug.

**Finding**: On Windows, the default ephemeral port range (~16,000 ports) can be saturated by high-concurrency, high-volume per-request workloads. Sockets remain in `TIME_WAIT` for approximately 60 seconds. Subsequent benchmark cells that reuse the same server address inherit this residual state.

**Methodology consequence**: Each benchmark cell must use a fresh server on a dynamically assigned port (`:0`). This is why the benchmark runner starts and stops a new server per cell rather than sharing one instance.

**This was not masked with `SO_REUSEADDR`.** The finding is recorded as an observed system behaviour.

## 12. Interpretation

Persistent connections dominate per-request across all metrics. The advantage has two sources:
1. **Direct overhead eliminated**: No 3-way handshake per request (~0.5–1.5ms on loopback, significantly more on real networks).
2. **Systemic OS pressure removed**: Port allocation, file descriptor cycling, and TIME_WAIT all accumulate and interact at high concurrency, compounding latency beyond the simple sum of per-dial costs.

## 13. Limitations
- All measurements are on `127.0.0.1`. On a real network, dial latency is dominated by actual network RTT (e.g., 10–100ms intercontinental) making the relative per-request penalty far larger.
- Percentiles use index-based approximation, not a proper histogram.
- TIME_WAIT mitigation (cooldowns) is partial: the OS reclaims ports over ~60 seconds, not milliseconds.

## 14. What Stage 1 Taught Us
1. TCP is a byte stream. Application-level message boundaries require explicit framing (`[length][payload]`). One `Read()` ≠ one message.
2. Connection establishment is not free. The 3-way handshake adds measurable latency even on loopback.
3. Short-lived connections create systemic OS pressure beyond simple per-connection overhead.
4. Measuring latency correctly requires separating what you are measuring: dial latency ≠ application RTT ≠ end-to-end transaction latency.
5. Sequential benchmark cells can contaminate each other through residual socket state. Experimental isolation requires fresh server instances.

## 15. Why Stage 2 Follows Naturally
The empirical evidence demands that the HTTP reverse proxy in Stage 2 must pool connections to origin servers. A proxy that dials a new origin connection per incoming request will reproduce the per-request collapse observed here at high concurrency. Stage 2 will examine `net/http`'s `Transport` connection pooling and measure what the standard library does by default.
