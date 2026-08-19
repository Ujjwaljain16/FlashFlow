# FlashFlow Benchmarks

This directory tracks the benchmark infrastructure and methodology for all stages.

---

## Stage 1 Benchmarks

### Tools

| Command | Purpose |
|---|---|
| `go run ./cmd/benchmark-runner` | Full experiment-001 matrix (120 measured runs, 24 discarded warmups) |
| `go run ./cmd/experiment-001a` | Causal decomposition: dial-only, per-request, persistent RTT |
| `go run ./cmd/tcp-client` | Single-configuration benchmark with text or JSON output |

### Measurement Definitions

**Application round-trip latency (RTT)**
: Wall-clock time from `WriteMessage()` call to `ReadMessage()` return on the client. Excludes TCP connection establishment in persistent mode. This is _not_ TCP RTT (kernel-level), which is typically sub-100µs on loopback.

**Connection establishment latency**
: Wall-clock time for `net.Dial()` to return a usable connection. Represents the 3-way handshake cost.

**End-to-end transaction latency**
: Connection establishment + application RTT. Only meaningful in per-request mode where a new dial occurs per transaction. In persistent mode it equals the application RTT.

**Throughput (RPS)**
: Successful application messages per second of total wall-clock duration.

**Percentiles**
: Sorted nearest-rank index selection: `latencies[n*p/100]`. This is an approximation. A proper HDR histogram implementation will replace this in Stage 6.

### Isolation Methodology

- A **fresh server** is started on `:0` (dynamically assigned port) for each benchmark cell.
- **Cell execution order is randomised** to reduce order-dependent bias.
- A **200ms cooldown** is applied between cells to allow TIME_WAIT sockets to begin draining (they persist ~60s on Windows; the cooldown reduces peak congestion, not total socket count).
- **Warmup runs** (~10% of request count, minimum 50) are executed before measurement and their results discarded.

### Why Not SO_REUSEADDR?

`SO_REUSEADDR` / `SO_REUSEPORT` would mask the TIME_WAIT phenomenon rather than document it. The TIME_WAIT finding is one of Stage 1's key results — it directly motivates connection pooling in Stage 2.

### Matrix (Experiment 001)

| Dimension | Values |
|---|---|
| Concurrency | 1, 10, 100 |
| Requests | 1,000, 10,000 |
| Payload | 32 bytes, 1,024 bytes |
| Modes | `persistent`, `per-request` |
| Iterations | 5 measured + 1 warmup per cell |

Total: 24 cells × 5 iterations = **120 measured runs**, 24 discarded warmups.

Results are written to `experiments/001-tcp-connection-lifecycle/results/` as individual JSON files named:
```
c{concurrency}-r{requests}-p{payload}-{mode}-iter{n}.json
```

### Causal Decomposition (Experiment 001-A)

Three isolated phases per concurrency level:
- **A**: `net.Dial()` + `conn.Close()` only — no application data
- **B**: Full per-request transaction with dial and RTT measured separately  
- **C**: Full persistent transaction (RTT only, no dial)

Results: `experiments/001-tcp-connection-lifecycle/results/001A-c{concurrency}-r{requests}.json`

---

## Environment Metadata Template

Every benchmark report should record:

```json
{
  "go_version": "go1.23.3",
  "os": "windows",
  "arch": "amd64",
  "date": "2026-08-19"
}
```
