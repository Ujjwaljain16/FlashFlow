# Stage 17 — Diagnostic Tooling: Failure Report, Explain, Stress Map

This is **tooling built on top of Stages 11-16's completed research, not a new research stage**. No new
topology model, no new finding, no verdict ceremony — every quantity the tools below compute already
existed in `internal/backlog` or in Stage 15/16's own published findings. What's new is turning that data
into something an engineer can actually run and read: `go run ./cmd/flashflow report`, `explain`, and
`stress-map`.

## What Was Built

- Two small additions to `internal/backlog`: `Timeline.PeakDepthAt()` (when, not just how deep, the peak
  occurred) and `Timeline.DrainedAfter(capacity, afterMs)` (does a target's queue ever clear, independent
  of whether any policy reaction was detected — needed for static policies like weighted-round-robin that
  never register a "diversion" at all).
- A new package, `internal/report`, containing the classifier, the report/explanation renderers, the
  canonical-scenario constructor (extracted once from `experiment-015a`/`016-flagship` rather than
  duplicated a further time), and the stress-map grid runner.
- A new CLI, `cmd/flashflow`, with three subcommands: `report`, `explain`, `stress-map`.

## The Classifier

Four labels, matching Stage 15/16's own two-mechanism model: `STABLE`, `ACUTE_COLLAPSE`,
`CHRONIC_COLLAPSE`, `RECOVERY_LIMITED`.

```
1. !CongestionFound                                → STABLE

2. CongestionFound:
     concentrated := ConcentrationRatio >= 1.2       (bottleneck's own dispatch share
                                                       during its congestion episode,
                                                       divided by fair share)
     severe       := CommittedWork/Capacity >= 10

     !Drained:
        concentrated  → ACUTE_COLLAPSE     (locked on, still hasn't recovered)
        !concentrated → CHRONIC_COLLAPSE   (never adapts, never recovers)

     Drained:
        !severe                  → STABLE            (resolved with minimal backlog, regardless of concentration)
        severe && concentrated   → ACUTE_COLLAPSE     (real lock-in, recovered only after heavy over-commitment)
        severe && !concentrated  → RECOVERY_LIMITED   (structural mismatch, no active correction, but did clear)
```

## Where the Two Constants Come From, and Three Bugs Caught Calibrating Them

**Corrected, independent-audit finding**: this section originally claimed both magnitude constants
(`1.2`, `10`) were "chosen once, up front... not tuned after the fact to produce a pleasing table." That
is not an accurate description of the process the three bugs below actually record. In plain terms: the
classifier's decision tree, and both of its numeric thresholds, were iteratively adjusted UNTIL the tool's
output matched Stage 15/16's own already-published characterization of the same six policies — a genuine
calibration process, but one against a single, fixed, six-point sample with no held-out scenario or
policy the thresholds were checked against afterward. `1.2` sits in the real gap between round-robin's
own concentration ratio (≈1.06x) and every other policy's (1.4x-3.1x) BECAUSE it was picked to land there;
`10` similarly separates this same six-point sample's severe cases from its mild ones. That is a legitimate
way to build a diagnostic tool matched to known ground truth, and the three bugs it caught below were
real and worth fixing — but it is calibration against six known points, not an independently-derived
threshold merely confirmed by them, and it says nothing about how the tree would classify a seventh
policy or a different scenario. Three bugs, each worth recording precisely:

**Bug 1 — fraction-of-time-over-capacity is not the right chronic/acute gate.** The first version of the
classifier used `FractionAboveCapacity >= 0.5` as the primary gate. It misclassified EWMA as
`RECOVERY_LIMITED`: EWMA's own drain takes until t≈6.85s of an 8s horizon, so its fraction-of-time-over-
capacity (≈0.55) is comparable to round-robin's (≈0.71) even though the two failures are mechanistically
opposite — one is a single acute episode that happens to take a long time to clear, the other is a
permanent structural mismatch. **Fix**: gate on CONCENTRATION instead (does the bottleneck receive
meaningfully more than fair share), which correctly separates the two: round-robin's bottleneck sits at
≈1.06x fair share (never concentrates at all); every other policy's sits at 1.4x-3.1x.

**Bug 2 — concentration must be measured from dispatches, not completions, and windowed to the episode,
not the whole run.** The first fix used `CompletedByTarget`'s whole-run share. This produced EWMA's own
concentration ratio as ≈0.8 (BELOW fair share!) — the opposite of reality. Two compounding errors: (a)
completions undercount a still-backlogged target relative to what it was actually sent, the identical
horizon-truncation distortion Stage 13 already found in `internal/attribution`'s own rho calculation; (b)
EWMA's own worst-congestion target (chosen by peak queue depth) is not always the same target it favors
for most of the run — it can lock onto one fast target for the low-rate baseline period, then briefly
misjudge a *different* target during a sudden burst, before its smoothed signal catches up. Whole-run
share looks at the wrong window entirely. **Fix**: measure the bottleneck's dispatch share strictly within
its own congestion episode (`[congestion onset, resolution)`), not across the whole run.

**Bug 3 — committed-work severity must be checked symmetrically, not only on the concentrated branch.**
The first version of the tree only checked `CommittedWork` for concentrated cases, defaulting
non-concentrated-but-drained cases straight to `RECOVERY_LIMITED`. This misclassified P2C-load (which
never concentrates by design, and separately commits almost no work at all, `CommittedWork=4`) as
`RECOVERY_LIMITED` instead of `STABLE`. **Fix**: check backlog severity first, for every case, regardless
of concentration; concentration then only distinguishes *why* a severe case failed, not whether a mild
one gets credit.

## Validation Against Stage 16's Own Six Policies

This table is a calibration check, not an independent validation: these are the same six outcomes the
thresholds above were iteratively adjusted against, so a match here confirms internal consistency with
Stage 15/16's own prior characterization, not that the classifier generalizes to a policy or scenario
outside this set.

| Policy | Classification | Matches Stage 16's characterization? |
|---|---|---|
| round-robin | `CHRONIC_COLLAPSE` | Yes — exactly |
| weighted-round-robin | `ACUTE_COLLAPSE` | A genuine refinement, not a mismatch: WRR's own peak depth (81) and committed work (163) are real and substantial, even though its aggregate mean/p99 are comparatively mild. Stage 16 called it "stable when calibrated" at the level of final outcome; this tool shows real transient backlog underneath that outcome — both are true, at different levels of resolution |
| least-connections | `STABLE` | Yes — exactly |
| ewma | `ACUTE_COLLAPSE` | Yes — exactly, matching the original motivating example for this whole tool |
| p2c-load | `STABLE` | Yes — exactly |
| adaptive | `ACUTE_COLLAPSE` | Yes — matches Stage 16's own finding that Adaptive's P99 was the worst of six in this scenario |

## Usage

```bash
go run ./cmd/flashflow report --policy ewma
go run ./cmd/flashflow explain --policy ewma <the report JSON just written>
go run ./cmd/flashflow stress-map --policy least-connections
```

Note: `explain`'s `--policy` flag works before OR after the file path (a small, deliberate robustness fix
over Go's own `flag` package default, which otherwise silently stops recognizing flags once it hits the
first positional argument).

## Non-Goals

This tooling does not add a new research claim, does not change any Stage 11-16 finding, and does not
retroactively alter any committed experiment result. `flashflow stress-map`'s own grid is a NEW, small
(9-cell) exploratory run using Stage 13's own three heterogeneity topologies and Stage 15's own three
workload shapes at a fixed request count (600) — it is not a replication of any specific prior experiment,
and its own numbers should not be cited as a Stage 13-16 finding.
