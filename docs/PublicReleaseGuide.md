# Public Release Guide

This is the positioning and information-architecture reference for FlashFlow's public-facing surfaces
(README, dashboard). It exists so the README and the dashboard's copy stay consistent with each other and
with what the evidence actually supports — write new public copy against this document, not from scratch.

## Identity

**FlashFlow is a routing failure-analysis laboratory for distributed edge systems.** It lets engineers run
controlled experiments, compare routing policies under identical conditions, reconstruct congestion and
backlog dynamics, and explain why latency collapses.

Primary promise: **find out why your routing system fails under pressure** — not which policy produced
the lowest benchmark number.

Supporting line: *FlashFlow runs controlled routing experiments, compares policies under identical
conditions, reconstructs congestion behavior, and explains the mechanism behind latency collapse.*

Alternative, terser line where space is tight: **Benchmark. Reproduce. Explain.**

The core interaction loop, referenced everywhere:

```
RUN -> COMPARE -> WHY? -> MECHANISM -> EVIDENCE -> REPRODUCE
```

The core mechanism visual, referenced everywhere a failure is explained:

```
traffic -> concentration -> capacity pressure -> committed work / chronic over-allocation
        -> queue behavior -> tail latency
```

## Explicitly NOT

Do not reposition FlashFlow as: a production CDN, a replacement for Envoy/NGINX/HAProxy, a complete
packet-level network simulator (ns-3, Mininet), a universal routing optimizer, or proof that Adaptive
always wins. This list is already the README's own "Non-Goals" section — reuse it verbatim, don't
re-derive it.

Avoid marketing language not directly supported by the repository: "revolutionary," "production-grade,"
"solves distributed systems," "guaranteed optimal routing," "AI-powered intelligence." Every claim traces
to a stage's own evidence (see `docs/PublicReleaseAudit.md` §2 for the sourced figures and their exact
citations) or it doesn't get made.

## Public vs. internal terminology

| Internal name | Public-facing name |
|---|---|
| `Stage15`, `015a`, `016-flagship` | Kept as evidence metadata / footnotes in Research History, not primary navigation |
| Failure classification tool | "Diagnose" / "Failure Report" |
| `flashflow explain` | "Why did it fail?" |
| `flashflow stress-map` | "Stress Map" (labeled exploratory) |
| Stage 11-17 sequence | "Research History" / "How FlashFlow evolved" |
| `internal/report`, `internal/backlog`, package names | Never exposed as UX concepts |

Stage IDs and experiment IDs are not hidden — they remain visible in evidence metadata, reproduction
commands, and the Research History page — they're just not what a first-time visitor sees first.

## Dashboard information architecture

Primary tabs, in order, replacing the current developer-shaped ones ("Control Room / Playground /
Experiments / Tuning"):

1. **Overview** (new) — hero identity statement, three capability cards (Benchmark / Compare / Explain),
   a "FlashFlow in numbers" stat row (sourced figures only, each linking to its stage), and a "Surprising
   Result" callout for the Adaptive-worst-P99 negative finding (see below).
2. **Compare** — existing Policy Comparison + Behavior bars, unchanged mechanics.
3. **Diagnose** — existing Why-Did-It-Fail / Explain card, plus First Divergence (counterfactual replay).
4. **Stress Map** — existing Regime Explorer, exploratory labeling kept exactly as-is.
5. **Research** (new) — the Stage 11-17 story in one line per stage (question / discovery / change in
   understanding), each linking to its full `docs/StageArtifacts/StageNN.md` artifact. Content is reused
   directly from the README's own "Key Research Findings" corrections list — nothing new is claimed here.
6. **Evidence** — existing Evidence cards, promoted to a top-level tab instead of a Control-Room sub-panel.
7. **Reproduce** — existing CLI Reference plus every view's own Reproduce box, consolidated into one place.
8. **Advanced** — the existing Playground / Experiments / Tuning tabs, kept for the "I want to verify this
   myself" tier rather than deleted; these are genuinely developer-facing and stay that way.

This is a navigation and copy reorganization, not a new data layer: every panel above already fetches from
`internal/dashboard`'s existing endpoints, which already wrap `internal/report`'s single classifier. No
new backend endpoint, no duplicated classification logic in JS.

## The negative result (must stay visible, not buried)

Stage 15's canonical scenario found Adaptive's own P99 was worst-of-six in two of the three independent
seeds, and a statistical near-tie with EWMA (within 0.3%) in the third — never among the safer half of six
policies in any seed tested — despite a comparatively strong mean. (An earlier synthesis overstated this
as "worst in every seed"; corrected via independent audit against the committed
`016-flagship-results.json` — see `docs/StageArtifacts/Stage16-ClaimLedger.md` claim C24.) A controlled
ablation traced its resistance to collapse specifically to its Load signal, not general "smartness." This
is presented as
a first-class "Surprising Result" card on the Overview tab (reusing Evidence claim C24, already tracked as
`RETIRED` — "Adaptive is safe from collapse in general" — in the existing Evidence ledger), not softened
or hidden. It's evidence FlashFlow is an investigative system, not a benchmark leaderboard.

## Diagnostic classification framing

`STABLE` / `ACUTE_COLLAPSE` / `CHRONIC_COLLAPSE` / `RECOVERY_LIMITED` are diagnostic heuristics, validated
against the canonical scenario's own six published policies — not universal physical laws. Every public
rendering of a classification already carries a plain-spoken qualifier next to the label (e.g. `STABLE` —
"no collapse mechanism detected under current diagnostic criteria," not "this policy is safe") via
`internal/report.ClassificationSubtitle` and its dashboard mirror `CLASS_SUBTITLE` in `app.js` — this was
already fixed in a prior pass; new public copy should reuse these subtitles rather than the bare label.

## Reproduction paths

Every public number or result should be traceable to one of:

```bash
go run ./cmd/flashflow report --policy <name> [--seeds N] [--json]
go run ./cmd/flashflow explain <scenario-report.json> --policy <name> [--json]
go run ./cmd/flashflow stress-map --policy <name> [--seed N] [--json]
./scripts/reproduce-flagship.sh
./scripts/reproduce-stage15.sh
./scripts/final-validation.sh
```

Every dashboard view's own "Reproduce" box already states the seed used and either the exact CLI command
or (for dashboard-only views with no CLI equivalent yet) the internal call recipe — never overclaiming
reproducibility a view doesn't actually have. Keep that convention for any new view.

## Explicitly out of scope for this pass

Extending Stress Map to a configurable target-count (3/5/8) selector: `RunStressMap` is hardcoded to
Stage 13's exact 3-target topologies, and generating new N=8 heterogeneity topologies to support a UI
selector would be new experimental surface, not productization — it contradicts this project's own rule
against rewriting the backend to manufacture a feature. If this is wanted later, it should be scoped as
its own small research task (new topology definitions, validated the way Stage 13's own topologies were),
not bundled into a presentation pass.
