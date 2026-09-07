# Stage 16 — Flagship Demo Script

A concise, presentable walkthrough of FlashFlow's flagship demonstration. Run it yourself with:

```bash
./scripts/reproduce-flagship.sh
```

## 1. The Research Question

> What happens when a routing policy commits too much work to a target whose capacity is insufficient?

Not "which policy has the lowest average latency" — that question hides the mechanism. This demo shows
the mechanism directly.

## 2. The Topology

Five heterogeneous targets, each with exactly one serving slot (Capacity=1 — a FIFO queue forms the
moment more than one request needs that target at once):

```
edge-00: 15ms service time
edge-01: 30ms
edge-02: 45ms
edge-03: 60ms
edge-04: 75ms
```

This is the exact same topology `experiment-015a` (Stage 15's own canonical scenario) used — no new
topology model was introduced for this demo, per this stage's own instruction.

## 3. The Workload

A FlashCrowd pattern: a low background rate (20 req/s) that spikes to 300 req/s for about a second,
centered at t=2.5s, over an 8-second horizon — long enough to see the full build → detect → respond →
recover sequence, not just a boundary snapshot.

## 4. Run Competing Policies

Six policies, unmodified, no tuning, same scenario, same seed: round-robin, weighted-round-robin,
least-connections, EWMA, P2C-load, Adaptive.

## 5. Tail Outcome (Seed A, 16000)

| Policy | Mean | P99 |
|---|---:|---:|
| round-robin | 845.10ms | 3760.97ms |
| weighted-round-robin | 433.98ms | 1218.17ms |
| least-connections | 451.54ms | 2500.41ms |
| ewma | 970.95ms | 4399.88ms |
| p2c-load | 535.99ms | 2693.10ms |
| adaptive | 638.06ms | 4072.11ms |

Adaptive's mean looks reasonable — better than EWMA's, worse than LC/WRR/P2C's. Its P99 tells a different
story: 4072ms, worse than EWMA's own 4399ms is close and in other seeds Adaptive's P99 is the outright
worst of all six (see Step 9).

## 6. Queue/Backlog Timeline (Seed A, edge-02 for EWMA/P2C/Adaptive, edge-00 for WRR/LC, edge-04 for
round-robin — each policy's own bottleneck target)

```
round-robin (edge-04):        ▂▄▆▇▇▇▇▇▇▇▆▆▆▆▆▆▅▅▅▅▅▅▄▄▄▄▄▄
weighted-round-robin (edge-00): ▂▅▇▇▇▆▅▄▃▂▁
least-connections (edge-00):    ▂▅▇▇▇▅▄▃▂
ewma (edge-02):                  ▇▇▇▆▆▆▅▅▄▄▄▃▃▃▂▂▂▁▁
p2c-load (edge-02):             ▂▅▇█▇▆▆▅▄▄▄▃▂▂▁
adaptive (edge-02):               ▃▆▇▇▇▆▆▆▅▅▅▄▄▃▃▃▂▂▂▁▁
```

40 buckets across the 8-second horizon; darker/taller = deeper queue at that moment. Round-robin's queue
never fully empties out within the visible window — it stays elevated long after the others have drained.
EWMA and Adaptive both build the deepest queues of the six and take the longest to come down.

## 7. First Divergence

All six policies detect congestion on their own bottleneck target within a few hundred milliseconds of
each other (roughly t=2.3-2.5s, matching the workload's own peak). The divergence isn't in WHEN they
notice — it's in what happens between noticing and correcting:

| Policy | Committed backlog (Seed A) | Meaning |
|---|---:|---|
| round-robin | 4 | Never "diverts" in the reactive sense — its allocation is fixed from the start |
| weighted-round-robin | N/A* | Never registers a diversion event at all — its static weights never change |
| least-connections | 8 | Diverts fast, commits little |
| p2c-load | 4 | Diverts fast, commits little |
| ewma | 98 | Diverts, but not before committing ~25x more work than LC/P2C |
| adaptive | 107 | Diverts, but commits even more than EWMA in this seed |

\* "N/A" is a TEXT-RENDERING convention (`cmd/experiment-016-flagship`'s own console printer), not a
property of the committed JSON artifact -- `016-flagship-results.json` stores `committed_backlog: 0` for
weighted-round-robin, the same literal value it would store for a genuinely zero committed backlog. The
`diversion_found: false` field is what actually distinguishes the two cases, and is present in the JSON;
a reader consuming the raw artifact directly (not this table) must check `diversion_found` first and treat
`committed_backlog` as not meaningfully comparable to another policy's when it's false, rather than reading
`0` as "zero backlog." Found stated as an unqualified fact ("correctly reported as N/A... not zero") in an
independent audit, when it's true of this document's own table but not of the data underneath it.

## 8. Explaining Committed Backlog

Committed backlog is the count of requests dispatched to a target strictly between the moment it becomes
congested and the moment the policy's own routing decisions materially shift away from it (a trailing
window of decisions showing that target's share drop below its scaled fair-share threshold). EWMA's
smoothed-latency history takes a while to reflect a target's CURRENT queueing state, so it keeps sending
work there past the point a current-pressure-aware policy (least-connections, P2C's sampling, Adaptive's
own Load signal) would have already redirected it. Once committed, that work cannot be un-sent — later
recognition cannot retroactively drain a queue that's already formed.

## 9. The Chronic Counterexample

Round-robin's own numbers look almost reassuring at first glance — committed backlog of only 4, the
lowest of the six. But its bottleneck target (edge-04, the slowest) spends **71% of the entire 8-second
run above capacity** and never fully drains within the horizon — the highest fraction-of-time-over-
capacity of any policy tested. This is not an acute event at all: round-robin's fixed 1-in-5 allocation
permanently exceeds edge-04's own service capacity, from the first request to the last. Committed backlog
correctly says "nothing acute happened here"; it says nothing about this chronic, structural failure.
That is why this project's final synthesis names TWO mechanisms, not one.

And Adaptive is not simply "the safe one." Across all three independently-seeded reproductions:

| Seed | Adaptive Mean | Adaptive P99 | Adaptive Committed Backlog | Drains? |
|---|---:|---:|---:|---|
| A (16000) | 638.06ms | 4072.11ms | 107 | Yes |
| B (16001) | 714.37ms | 4732.39ms | 127 | **No** |
| C (16002) | 582.73ms | 4853.07ms | 93 | **No** |

Adaptive's P99 is at or near the worst of all six policies in every seed. A controlled ablation
(`experiment-015c`) traced this specifically to how much its Load signal is weighted — remove Load
entirely and Adaptive's committed backlog more than doubles (86→206 in the deterministic single-seed
version of this scenario), worse than EWMA's own. Adaptive is safer than EWMA here because of a specific,
identifiable signal, not because it is generically "smart."

## 10. Reproducibility

```bash
./scripts/reproduce-flagship.sh
```

This reruns the exact scenario above across all three seeds and prints every number in this document.
Because this is a pure virtual-time simulation (no real network, no wall-clock timing), reruns on the
same machine and Go toolchain version produce byte-for-byte identical result JSON except for the
`timestamp` field — confirmed directly: rerunning it during this stage's own audit changed exactly one
line of the committed result file. (This is a property of the virtual engine specifically — the project's
one real-engine experiment, `experiment-015f`, is not byte-identical across runs; see
`docs/StageArtifacts/Stage16.md`'s reproducibility section for that distinction.)

## 11. The Limitation

This demo shows one canonical scenario, three seeds, one topology size and shape. The mechanism it
demonstrates — committed backlog for acute collapse, fraction-of-time-over-capacity for chronic collapse
— is confirmed to generalize across target count (N=3/5/8) and, imperfectly, across workload shape
(Constant/Burst/FlashCrowd). It does not explain Stage 13's own cache-affinity interim-latency effect,
tested directly and left genuinely unresolved. The real-engine reproduction of this exact mechanism's
directional claim holds at most, not all, tested overload levels.

**This is the mechanism we can support from the evidence. Everything beyond this boundary remains an open
question.**
