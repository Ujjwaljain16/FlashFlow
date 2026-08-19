# Learning Notes: TCP Foundations

## Initial assumption
One might expect: (a) a TCP socket behaves like a message queue — one `Write()` produces one `Read()` on the other side; and (b) opening and closing connections is "cheap enough" for local testing.

## Implementation
A raw TCP server and client using a custom binary framing protocol (`[length (uint32)][payload]`). Two client modes: `persistent` (reuse one socket across N requests per worker) and `per-request` (Dial → Write → Read → Close per request).

## Experiment
Full matrix: 3 concurrency levels × 2 request counts × 2 payload sizes, 5 iterations each, with a separate Connection-Cost Decomposition experiment (001-A) that isolates:
- Dial cost alone (phase A)
- Per-request connection + application RTT separately (phase B)
- Persistent RTT with no dial cost (phase C)

## Observations

### 1. TCP is a byte stream
Without the `[length][payload]` framing loop and `io.ReadFull`, a single `Read()` would return an arbitrary number of bytes — potentially partial messages, or multiple messages merged together. The `TestProtocol_PartialReads` test explicitly proved this using a `slowReader` that yields 3 bytes at a time.

**FlashFlow implication**: Any custom protocol layer (future multiplexed edge connections) must rigorously enforce its own framing semantics.

### 2. Dial cost on loopback is ~0.5–1.5ms
On loopback, the measured `net.Dial()` time captures TCP connection-establishment and associated local socket/kernel overhead; no packets leave the machine.

**FlashFlow implication**: On a real network, connection-establishment cost will also include network propagation and transmission delays, so the penalty can become substantially larger than the loopback measurement observed here.

### 3. Connection churn creates systemic OS pressure
At c=100 (100 concurrent per-request workers), the application RTT in per-request mode jumps to 9.4ms (p50) even though the persistent RTT at the same concurrency is sub-millisecond. Connection establishment is a major contributor to this bottleneck under churn, but high connection churn also creates secondary OS/socket pressure that inflates application-level latency well beyond the measured connection-establishment cost alone.

### 4. TIME_WAIT contaminates sequential benchmarks
At sufficiently high connection churn, the client exhausted available ephemeral socket/port resources, causing subsequent `Dial()` calls to fail until previously used resources became reusable. In this Windows environment, the affected connections remained unavailable for roughly tens of seconds (about 60 seconds was observed), contributing to subsequent `Dial()` failures. This means:
- Benchmark cell B can be contaminated by benchmark cell A's leftover sockets.
- **The fix is experimental isolation, not masking**: start a fresh server on a dynamic port (`:0`) per cell; add cooldowns between phases; never use `SO_REUSEADDR` to make the problem disappear silently.

### 5. Latency terminology must be precise
This measurement should not be described as TCP RTT: it includes application-level write, server processing, and response-read time on an established connection.

The three distinct latency types measured in experiment 001-A:
- **Dial latency**: cost of `net.Dial()` (3-way handshake)
- **Application RTT**: cost of write + server processing + read (on an established connection)
- **End-to-end transaction latency**: dial + application RTT (per-request mode only)

### 6. Unexpected behavior
At c=100, the original single-server benchmark runner produced irreproducible results because cells ran sequentially on the same server. This was not caught until the Connection-Cost Decomposition experiment failed with all-zero results — which itself demonstrated the TIME_WAIT phenomenon empirically rather than theoretically.

## Evidence & Connection-Cost Decomposition Data

To isolate and prove these dynamics, Experiment 001-A (Connection-Cost Decomposition) ran 3 phases under loopback using a 64-byte payload:
- **Phase A (Dial-only)**: Measuring connection handshake cost in isolation (no application data).
- **Phase B (Per-request)**: Connection establishment and application RTT measured separately.
- **Phase C (Persistent)**: Application RTT only on a pre-established connection.

### Experiment 001-A Results Table

| Measurement | c=1 p50 | c=1 p99 | c=10 p50 | c=10 p99 | c=100 p50 | c=100 p99 |
|---|---|---|---|---|---|---|
| **A: Dial-only** | ~0µs | 673µs | ~0µs | 1.13ms | ~0µs | 1.50ms |
| **B: Per-req conn latency** | 557µs | 795µs | 588µs | 1.71ms | 667µs | 3.14ms |
| **B: Per-req app RTT** | ~0µs | 791µs | 1.00ms | 1.77ms | **9.38ms** | 12.20ms |
| **C: Persistent app RTT** | ~0µs | 654µs | ~0µs | 1.02ms | ~0µs | 13.77ms |

### What the Evidence Teaches Us
1. **Dial Overhead in Isolation**: The measured p99 dial latency was 673 µs at c=1, 1.13 ms at c=10, and 1.50 ms at c=100. This isolates the baseline overhead of TCP's 3-way handshake under varying concurrencies.
2. **OS/Socket Pressure**: At c=100, persistent-mode p50 application RTT remained near zero, but its p99 reached 13.77 ms, indicating substantial tail variability even without per-request connection establishment. The measurements indicate systemic OS/socket pressure associated with high connection churn, producing application-level latency substantially beyond the measured connection-establishment cost alone.
3. **TIME_WAIT Contamination**: In the original unisolated benchmark run, sequential cells polluted each other because TIME_WAIT ports persisted for tens of seconds on the OS. This proved that benchmark isolation (fresh server instances, randomised cell order, and dynamic `:0` port allocations) is a mandatory methodology requirement.
4. **Resolution Limitations**: The `~0µs` p50 latency measurements on loopback reflect duration rounding at nanosecond resolution on Windows. This exposes a measurement-resolution limitation and motivates higher-fidelity latency measurement and histogram tooling in later stages.

## FlashFlow Implication (Why Stage 2 Follows Naturally)

1. **Connection Pooling Expected Value**: Stage 2 should explicitly measure and validate connection pooling. A proxy that opens a new upstream TCP connection for every request is expected to reproduce the connection-churn behavior observed in Stage 1. Go's `net/http` Transport connection pooling (`Transport.MaxIdleConnsPerHost`) is the targeted mechanism to resolve this.
2. **Decouple Policy from Transport**: To prepare for Stage 5's Virtual-Time Engine, the proxy's routing/cache logic must be completely decoupled from Go's concrete socket/transport layer. The routing policy must take abstract metadata and return a target edge, so the same policy function can be run identically on the real network proxy and in the deterministic event simulator.

## Evidence Boundary

What this experiment establishes:
- TCP framing is required for application message boundaries.
- Connection establishment has measurable cost even on loopback.
- High connection churn correlates with increased application-level latency and socket-resource pressure.
- Sequential benchmark cells can be contaminated by residual socket state.

What this experiment does not establish:
- Exact kernel mechanism responsible for the high-concurrency latency amplification.
- Real-network/inter-region latency figures.
- HTTP-specific connection-pooling behavior.
- Generalization to non-Windows environments.

