# FlashFlow — Product Requirements Document (v3.1 — Dual-Engine Architecture)

**Adaptive Edge Networking & Flash-Crowd Resilience Laboratory**

Status: Approved for build · Owner: JainSahab · Stack: Go / Docker / Linux
Scope: Core execution and experimental engines. Complex ML models (LinUCB, DR-OPE) and full live dashboards are explicitly staged for later phases to preserve focus.

---

## 0. What Changed From v3.0

1. **Dual-Engine Architecture**: FlashFlow now explicitly defines two execution modes that share the same configuration and policies:
   - **Virtual-Time Engine**: A deterministic, discrete-event simulator for rigorous scientific research, auto-tuning, and counterfactual replay.
   - **Real Emulation Engine**: A high-fidelity, Docker-backed validation engine using `net/http` and `tc netem` to prove policies survive contact with the real Linux networking stack.
2. **Canonical Event Streaming**: The system records an immutable Event Stream as the single source of truth, from which Prometheus metrics, HdrHistograms, and experiment artifacts are derived.
3. **Queueing-Theoretic Attribution**: Added an analytical layer to explain *why* tail latencies spike (e.g., origin utilization approaching 1.0) rather than just reporting the spike.
4. **Research-Driven Progressions**: The Auto-Tuner now evolves from Random Search to Bayesian Optimization, making the optimizer itself a research subject. Advanced ML models (LinUCB) and full live dashboards are explicitly deferred to advanced stages.

---

## 1. What — Definition

**FlashFlow is a programmable Go laboratory for studying distributed edge topologies under extreme load and partial failure.** It provides a dual-engine architecture: a deterministic virtual-time engine for rigorous, reproducible routing strategy comparisons, and a real-world Docker emulation engine for high-fidelity validation. FlashFlow produces scored, queueing-theory-explained, statistically grounded comparisons between routing strategies, featuring a self-tuning adaptive router and a stateful counterfactual replay engine.

---

## 2. Why

Two questions the project exists to answer:

**Systems question:** How should an edge system trade off latency, availability, bandwidth, compute capacity, cache efficiency, and infrastructure cost under unpredictable traffic and partial failures?

**Portfolio question:** Can a system that generates its own evidence (auto-tuned weights, replay-verified comparisons, real production traffic import) be more defensible in an interview than a system that just implements five load-balancing algorithms and shows a bar chart? Everyone has the bar chart. Almost nobody has the deterministic replay engine that proves the bar chart wasn't cherry-picked.

---

## 3. Who

**Primary user:** you — learning networking, concurrency, distributed systems, and cloud economics hands-on, one course session at a time, using this as the vehicle.

**Secondary user:** a reviewer/interviewer/mentor who should be able to evaluate the architectural separation between the deterministic research engine and the high-fidelity Docker validation engine, inspect the generated statistical reports, and observe the queueing-theoretic attributions.

---

## 4. Non-Goals

FlashFlow must not become: a Kubernetes replacement, a production CDN, a complete Internet/TCP-IP simulator, a replacement for nginx/Envoy/Cloudflare, a general-purpose packet analyzer, a cloud provider, a distributed database, or a production DDoS mitigation platform.

**Explicitly Deferred**: Deep Reinforcement Learning (DRL), complex Multi-Armed Bandits (LinUCB) as core dependencies, Doubly Robust Offline Policy Evaluation (DR-OPE) for synthetic runs, and the full interactive live dashboard (deferred to Stage 8 to prevent UI from interrupting analytical development).

---

## 5. Design Philosophy

1. **Build the real networking pieces first** — Understand TCP client/server and reverse proxying natively before abstracting into virtual time.
2. **Abstract the policy layer** — Routing logic must be cleanly separated from transport logic so it runs identically in both execution engines.
3. **Build the virtual-time engine** — Construct the deterministic engine specifically to solve the reproducibility problems encountered when tuning policies on wall-clock time.
4. **Move controlled experiments into virtual time** — Leverage the simulation engine for counterfactuals, large parameter sweeps, and statistical analysis.
5. **Validate against reality** — Use the real emulation engine to prove selected findings survive actual Docker/Linux execution.

---

## 6. The Uniqueness Layer (core, not optional)

### 6.1 Dual-Engine Execution
A shared `ExperimentEngine` abstraction running two modes: the **Virtual-Time Engine** (for scientific tuning/replay) and the **Real-World Emulator** (for realism/validation).

### 6.2 Weight Auto-Tuner Progression
The adaptive router's scoring function is tuned through an evolutionary progression:
- **Tuner v1**: Random Search (transparent baseline).
- **Tuner v2**: Latin Hypercube Sampling.
- **Tuner v3 (Advanced)**: Bayesian Optimization. 
This makes the optimizer itself an experimental subject. Results are always validated against unseen holdout scenarios to prevent overfitting.

### 6.3 Stateful Counterfactual Replay
A true scientific evaluation framework. To test "what if," the engine replays the *identical exogenous trace* (arrival events, failure schedules) against a new policy. Crucially, the new policy evolves its own **isolated endogenous state** (cache hits, queue depths).

### 6.4 Queueing-Theoretic Attribution
Instead of just logging latency spikes, the system automatically evaluates utilization ($\rho$) and outputs causal explanations (e.g., "Queue saturation $\rightarrow$ origin utilization $\rho = 0.96 \rightarrow$ queue buildup").

---

## 7. High-Level Architecture

```text
                           FLASHFLOW
                              │
                 ┌────────────┴────────────┐
                 │  Common Experiment API  │
                 └────────────┬────────────┘
                              │
         ┌────────────────────┼────────────────────┐
         │                                         │
         ▼                                         ▼
   Virtual-Time                              Real Emulation
     Engine                                     Engine
         │                                         │
   deterministic                              Docker/Linux
   research runs                              realism checks
         │                                         │
         └────────────┬────────────────────────────┘
                      ▼
               Event Stream (Canonical)
                      │
              ┌───────┼────────────────┐
              ▼       ▼                ▼
        Prometheus  HdrHistogram   Manifest & Artifacts
              │                        │
              └───────┬────────────────┘
                      ▼
              Optimization/Tuner
                      │
                      ▼
            Improved Policy Parameters
```

---

## 8. Full Feature Set

### 8.1 Traffic Generator
Constant, ramp-up, ramp-down, burst, flash-crowd. Traffic model fields: `request_id, client_id, target, method, path, payload_size, region, priority`. **Plus:** Fuze log import.

### 8.2 Global Router
Pluggable routing strategies in progression: Round Robin $\rightarrow$ Weighted RR $\rightarrow$ Least Connections $\rightarrow$ Latency-Aware EWMA $\rightarrow$ Power of Two Choices (P2C) $\rightarrow$ Adaptive (six-signal).

### 8.3 Reverse Proxy
Custom HTTP proxy per edge — request parsing, upstream connections, response forwarding, connection reuse. Abstracted so the Virtual Engine can use the same policy logic.

### 8.4 Connection Management
Active/accepted/closed connection tracking, reuse metrics, error rates.

### 8.5 Edge Cache
TTL, LRU eviction, hit/miss tracking. Four policies: No Cache, TTL, LRU, Stale-While-Revalidate. Request coalescing for stampede prevention.

### 8.6 Health Monitoring
Full 4-state machine: HEALTHY, DEGRADED, UNHEALTHY, RECOVERING. Timers use the abstracted `Clock` interface, not hardcoded wall-clock time.

### 8.7 Chaos Engine (Declarative)
Failures are defined declaratively in YAML (e.g., `at: 15s, target: edge-a, action: delay`). The Virtual Engine translates this into modeled events; the Emulation Engine translates this into actual `tc netem` Docker commands.

### 8.8 Experiment Ledger & Provenance
Experiments as first-class objects: `experiment_id, name, topology, traffic_profile, routing_policy`. **Added:** Immutable manifest with hierarchical seeds (Traffic, Topology, Failure, Policy) and Configuration Hashes.

### 8.9 Metrics & Analytics
An internal **Event Stream** is the canonical runtime record. This routes to:
- **HdrHistogram**: High-resolution tail latency distribution.
- **Prometheus**: Aggregate operational metrics.
- **Internal Statistics**: Bootstrap CI, Mann-Whitney U, Cliff's Delta, and Pareto analysis.

### 8.10 Live Dashboard (Explicitly Deferred)
The full live interactive dashboard (topology view, live metrics panel) is explicitly deferred to Stage 8. Focus remains on the analytical outputs and experimental ledgers first.

---

## 9. Build Sequence

| Stage | Course Alignment | Ships |
|---|---|---|
| 1 | Network Programming 101 | TCP client/server, basic HTTP proxy, connection tracking |
| 2 | Eagle-Eye Networking | 3-edge topology, static routing, Real Emulation Engine (`net/http`) |
| 3 | Server-Side Scaling | Round robin, least-connections, latency-aware EWMA |
| 4 | Edge Cache & Failures | LRU+TTL+SWR cache, request coalescing, `tc netem` realism checks |
| 5 | The Reproducibility Wall | **Virtual-Time Engine**, Clock Abstraction, Event Stream, Manifest Ledger |
| 6 | Statistical Science | HdrHistogram, Queueing-Theoretic Attribution, Mann-Whitney U analysis |
| 7 | Adaptive Routing | P2C, Adaptive Router (six-signal), Counterfactual Replay |
| 8 | Auto-Tuner & Optimization | Tuner v1 (Random Search), Train/Test generalization, Live Dashboard |

---

## 10. Success Criteria

**Architectural:** The system successfully executes experiments across both engines from a single specification, demonstrating that the Virtual-Time Engine provides deterministic, causally controlled / counterfactual-verified results, while the Emulation Engine validates those findings against real networking physics.

**Experimental:** The Auto-Tuner improves upon baseline heuristics (e.g., P2C), validated by unseen holdout scenarios and proven via stateful Counterfactual Replay, outputting a statistical report with queueing explanations.

---

## 11. Repository/Portfolio Positioning

README should say:

> **FlashFlow is an adaptive edge-networking laboratory utilizing a dual-engine architecture. It combines a deterministic virtual-time simulator for rigorous, counterfactual-verified policy tuning with a high-fidelity Docker emulation engine for real-world validation.**

---

## 12. Resume Line

> **FlashFlow — Adaptive Edge Networking Laboratory:** Built a dual-engine distributed system in Go combining a deterministic virtual-time simulator and a real-world Docker emulator (`tc netem`); implemented adaptive routing, LRU+coalescing edge caching, and a self-tuning parameter optimizer validated via stateful counterfactual replay and statistical queueing analysis.