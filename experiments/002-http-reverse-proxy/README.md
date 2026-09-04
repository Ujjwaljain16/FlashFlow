# Experiment 002: HTTP Reverse Proxy, Connection Pooling & First Real Edge Topology

## 1. Executive Summary & Research Questions

Stage 2 transitions FlashFlow from the raw TCP stream foundations established in Stage 1 into real HTTP request/response semantics, observable connection pooling, and multi-edge topology emulation.

### Primary Research Question
> **How does a real HTTP reverse proxy behave when forwarding requests to multiple edge services, and how much does upstream connection reuse matter under concurrent load?**

### Secondary Research Questions
1. Does Go's `net/http.Transport` actually reuse upstream TCP connections under concurrent load?
2. What happens to throughput and latency percentiles when keep-alive is disabled?
3. How do connection pool limits behave when routing across multiple edge targets?
4. What happens when one edge node experiences abrupt failure vs elevated application latency?
5. How quickly does the 4-state health machine detect and exclude an unhealthy edge?

---

## 2. Experimental Setup & Topology

### Topology Architecture
```text
  [HTTP Benchmark Client]
             │ (HTTP/1.1)
             ▼
  [FlashFlow Reverse Proxy] (:8080)
   ├── Tracked Transport (proxy_upstream)
   ├── 4-State Health Machine (HEALTHY, DEGRADED, UNHEALTHY, RECOVERING)
   └── Pluggable TargetSelector (Static / Round-Robin)
             │
   ┌─────────┼─────────┐
   ▼         ▼         ▼
[Edge A]  [Edge B]  [Edge C]  (:8001, :8002, :8003)
   │         │         │ (HTTP/1.1 Forwarding + X-Edge-ID)
   └─────────┼─────────┘
             ▼
      [Origin Server] (:8000)
```

### Environment Metadata
- **OS**: Windows / Linux (Docker Engine)
- **Go Version**: `go1.23.3 windows/amd64` / `linux/amd64`
- **Docker Compose**: `v2.40.3`
- **Container Base**: `alpine:3.20`
- **Measurement Model**:
  - $T_0$: Client request start
  - $T_1$: Proxy receives request
  - $T_2$: Proxy dispatches to upstream edge
  - $T_3$: Upstream edge response arrives at proxy
  - $T_4$: Proxy transmits response
  - $T_5$: Client receives complete response

---

## 3. Results Summary: Experiment 002-A1 (Single Upstream Connection Reuse)

Comparing Keep-Alive Enabled vs Keep-Alive Disabled on `Client -> Proxy -> Edge -> Origin`:

| Concurrency | Requests | Keep-Alive | RPS | p50 Latency | p99 Latency | Proxy Upstream Dials | Requests / Proxy Conn | Edge Origin Dials | Requests / Edge Conn |
|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| **c=1** | 500 | **Enabled** | **3,747.4** | ~0 ms | 2.83 ms | **1** | **550.0** | 1 | 550.0 |
| **c=1** | 500 | Disabled | 1,262.4 | ~0 ms | 7.00 ms | 550 | 1.00 | 550 | 1.00 |
| **c=10** | 1000 | **Enabled** | **4,637.0** | 1.69 ms | 8.75 ms | **10** | **105.0** | 9 | 116.7 |
| **c=10** | 1000 | Disabled | 2,648.9 | 3.11 ms | 15.64 ms | 1,050 | 1.00 | 1,050 | 1.00 |
| **c=50** | 1000 | **Enabled** | **7,987.5** | 3.51 ms | 27.82 ms | **38** | **27.6** | 22 | 47.7 |
| **c=50** | 1000 | Disabled | 3,271.6 | 12.91 ms | 58.31 ms | 1,050 | 1.00 | 1,050 | 1.00 |
| **c=100** | 1000 | **Enabled** | **8,445.2** | 5.17 ms | 52.85 ms | **64** | **16.4** | 22 | 47.7 |
| **c=100** | 1000 | Disabled | 2,760.4 | 28.87 ms | 97.28 ms | 1,050 | 1.00 | 1,050 | 1.00 |

### Key Takeaways from 002-A1:
1. **Connection Churn Amortization**: At $c=100$, Keep-Alive achieves **3.06× higher throughput (8,445 vs 2,760 RPS)** and reduces p50 latency by **82% (5.17ms vs 28.87ms)**.
2. **Socket Pressure**: Without keep-alive, 1,000 requests force 1,050 physical TCP connections on both tiers (Proxy $\to$ Edge and Edge $\to$ Origin), burning OS ephemeral ports and repeating the Stage 1 socket allocation bottleneck.

---

## 4. Results Summary: Experiment 002-A2 (Multi-Edge Connection Pooling)

Evaluating connection pool behavior across 3 distinct upstream hosts:

| Concurrency | Requests | Mode | RPS | p50 Latency | p99 Latency | Proxy $\to$ Edges Dials | Requests / Proxy Conn | Total Edge $\to$ Origin Dials |
|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| **c=10** | 1200 | Keep-Alive | 5,205.8 | 1.58 ms | 8.07 ms | 16 | 78.8 | 11 |
| **c=50** | 1500 | Keep-Alive | 7,975.1 | 2.79 ms | 34.40 ms | 40 | 39.0 | 25 |
| **c=100** | 2000 | Keep-Alive | **9,894.9** | 2.06 ms | 36.72 ms | 34 | 60.6 | 24 |
| **c=50** | 1500 | Disabled | 4,786.2 | 8.83 ms | 51.90 ms | 1,560 | 1.00 | 1,560 |

---

## 5. Results Summary: Experiment 002-B (Edge Failure Detection)

- **Scenario**: Continuous 100 req/sec traffic distributed across Edge A, B, C. Edge B stopped abruptly.
- **Time to Detection**: **12.86 ms** (detected within 1 prober interval).
- **Transient Failures**: 0 (all in-flight retries / health exclusion routed immediately to Edge A and C).
- **Subsequent Traffic**: 100% routed exclusively to surviving healthy nodes (`edge-a`, `edge-c`).

---

## 6. Results Summary: Experiment 002-D (One Slow Edge Latency Impact)

Demonstrating the fundamental flaw of static selection:

| Scenario | Edge A Delay | Edge B Delay | Edge C Delay | Throughput (RPS) | p50 Latency | p95 Latency | p99 Latency |
|:---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| **Homogeneous Baseline** | 1 ms | 1 ms | 1 ms | **4,610.2 RPS** | 3.49 ms | 21.97 ms | 38.81 ms |
| **One Degraded Edge** | 1 ms | 1 ms | **100 ms** | **478.0 RPS** | 2.36 ms | **102.27 ms** | **119.98 ms** |

> **Architectural Finding**: Because static routing blind-indexes targets round-robin, exactly 33.3% of all traffic is sent to Edge C. Edge C's 100ms processing delay causes client worker queuing, dropping cluster throughput by **89.6%** and inflating p95/p99 tail latency past 100ms.
> 
> **Stage 3 Bridge**: This directly motivates dynamic latency-aware and queue-aware routing policies (EWMA, Least Connections, Power of Two Choices, Adaptive Six-Signal Scoring).
