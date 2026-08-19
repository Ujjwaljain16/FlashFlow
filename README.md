# FlashFlow

**Adaptive Edge Networking & Flash-Crowd Resilience Laboratory**

> Dual-engine architecture: a deterministic virtual-time simulator for rigorous, counterfactual-verified policy tuning — combined with a high-fidelity Docker emulation engine for real-world validation.

---

## What is FlashFlow?

FlashFlow is a programmable Go laboratory for studying distributed edge topologies under extreme load and partial failure. The project is built stage-by-stage from raw TCP fundamentals upward, with every engineering decision grounded in experimental evidence.

```text
                       FlashFlow
                           │
              Common ExperimentEngine API
                           │
          ┌────────────────┴────────────────┐
          │                                 │
   Virtual-Time Engine              Real Emulation Engine
   (deterministic)                  (Docker / tc netem)
          │                                 │
          └────────────────┬────────────────┘
                           │
                    Canonical Event Stream
                           │
               ┌───────────┼───────────────┐
               ▼           ▼               ▼
          Prometheus    HdrHistogram   Experiment Ledger
```

---

## Build Sequence

FlashFlow is built learning-first — not architecture-first.

| Stage | Ships | Status |
|---|---|---|
| **1** | Raw TCP server/client, connection lifecycle, framing, benchmarks | ✅ Complete |
| **2** | HTTP reverse proxy, 3-edge topology, real emulation engine | 🔲 |
| **3** | Round-robin → EWMA routing policies | 🔲 |
| **4** | LRU+TTL+SWR edge cache, `tc netem` network degradation | 🔲 |
| **5** | Virtual-Time Engine, Clock abstraction, Event Stream | 🔲 |
| **6** | HdrHistogram, Queueing-theoretic attribution, Mann-Whitney U | 🔲 |
| **7** | P2C + Six-Signal Adaptive Router, Counterfactual Replay | 🔲 |
| **8** | Auto-Tuner (Random → LHS → Bayesian), Live Dashboard | 🔲 |

---

## Stage 1 — TCP Foundations

**Research question**: What is the performance difference between creating a new TCP connection for every request versus reusing persistent connections?

**Key findings**:
1. TCP is a byte stream. Application-level message boundaries require explicit framing — one `Read()` does not correspond to one message.
2. Even on loopback, `net.Dial()` (3-way handshake) costs ~0.5–1.5ms p99. On real networks this is 50–200ms.
3. At c=100, per-request mode app RTT degrades to ~9.4ms (p50) vs ~0µs for persistent — the penalty is systemic OS pressure, not just dial overhead.
4. TIME_WAIT socket exhaustion contaminates sequential benchmarks. Correct methodology requires fresh server instances per experiment cell.

### Running Stage 1

```bash
# Start the echo server
go run ./cmd/tcp-server --addr 127.0.0.1:9000

# Run benchmark (persistent mode)
go run ./cmd/tcp-client --addr 127.0.0.1:9000 --requests 10000 --concurrency 10 --connection-mode persistent

# Run benchmark (per-request mode)
go run ./cmd/tcp-client --addr 127.0.0.1:9000 --requests 10000 --concurrency 10 --connection-mode per-request

# Run full benchmark matrix (120 measured runs)
go run ./cmd/benchmark-runner

# Run causal decomposition experiment 001-A
go run ./cmd/experiment-001a
```

### Running Tests

```bash
go test ./...
go vet ./...
```

---

## Specifications

- [PRD v3.1](prd.md) — Product requirements and build sequence authority
- [TRD v3.1](trd.md) — Technical architecture and implementation authority
- [Research](research.md) — Research methodology reference

---

## Experiments

| # | Title | Status |
|---|---|---|
| [001](experiments/001-tcp-connection-lifecycle/) | TCP Connection Lifecycle | ✅ Complete |
| [001-A](experiments/001-tcp-connection-lifecycle/results/) | Connection Cost Decomposition | ✅ Complete |

---

## Resume Line

> **FlashFlow — Adaptive Edge Networking Laboratory**: Built a dual-engine distributed system in Go combining a deterministic virtual-time simulator and a real-world Docker emulator (`tc netem`); implemented adaptive routing, LRU+coalescing edge caching, and a self-tuning parameter optimizer validated via stateful counterfactual replay and statistical queueing analysis. Stage 1 grounded the system in raw TCP semantics through a controlled connection-lifecycle benchmarking study that discovered TIME_WAIT exhaustion and its effects on sequential benchmark contamination.
