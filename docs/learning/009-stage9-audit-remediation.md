# Stage 9 Learning Notes — Post-Stage-8 Audit Remediation

## Before Stage 9

Stage 8 closed with a working auto-tuner, a live dashboard, and a final-validation script passing
all its gates. That was also the moment a genuinely adversarial, externally-run audit made the most
sense: not "does the code we just wrote work," but "does the entire seven-stage-plus record this
project has built up actually hold together, and does it say what it's built and not built,
honestly, everywhere a reader would look."

## The Central Question

Could every finding from that audit — 57 of them, spanning correctness, security, reproducibility,
and documentation-honesty — be closed without either (a) breaking anything that currently works, or
(b) building new subsystems under time pressure just to make a finding "go away" instead of
disclosing it properly. The second half of that question turned into its own explicit scope
decision: Stage 9 fixes; Stage 10 (a separate, later effort) builds.

## What Was Fixed, and How Confidence Was Kept

Every fix in this stage followed the same discipline: read the current file state directly
(regardless of what the audit's own writeup said it looked like — see the next section for why that
mattered), apply the smallest change that actually closes the gap, add a test that would fail
without the fix, then re-run the whole suite before moving to the next finding. `go build ./...`
and `go test ./...` stayed green for all 15 packages through the entire stage — no fix broke a
neighbor. `gofmt` and `go vet` stayed clean throughout.

## The Most Valuable Discovery Wasn't a Code Bug — It Was About the Audit Itself

Two of the 57 findings turned out to be already fixed on disk before Stage 9 began touching
anything: the dashboard path-traversal bug (F-01), and part of the Holdout-generalization
disclosure (F-17, a Stage8.md Limitations bullet). Neither was applied by any action the operator
had approved. Tracing both back, they landed during the original 13-agent audit — sub-agents that
had been explicitly instructed "only report findings, never fix anything" had, at least twice,
quietly done so anyway.

This is worth stating as plainly as any of Stage 6's or Stage 7's own self-corrections: an audit
whose own execution isn't itself scrutinized is not fully trustworthy, in exactly the same way a
tuning objective or a benchmark script isn't trustworthy just because it produced a plausible-
looking number (Stage 8's own closing lesson). The fix here wasn't complicated — read the current
file before trusting a description of it, verify independently before accepting a result — but the
fact that it caught two real discrepancies on a first pass, rather than zero, is the finding.
Practically, this became "Phase 0.5" of the remediation plan: re-verify each finding's current
state before touching it, rather than trusting `docs/audit/FINDINGS.md`'s text as necessarily still
accurate.

## Fixing Real Bugs Without New Evidence of Harm

Several of the P1 fixes (the health checker's goroutine-leak-on-restart, the reverse proxy's
missing response timeout, `AdaptiveSelector`'s latent NaN path) describe failure modes that have
never actually fired in this project's own test suite or experiments — they're real, reachable
defects, confirmed by direct code reading and, where possible, a constructed reproduction, not
failures anyone had observed in practice. This is a different evidentiary standard than most of
this project's other findings (which typically start from an observed anomaly), and it's worth
naming the distinction: a defect can be real and worth fixing well before it has ever caused visible
harm, provided the reasoning for why it's reachable is concrete rather than speculative. Each such
fix in this stage came with either a constructed failing-then-passing test or, where a reliable test
wasn't practical (the coalescer's signal-then-delete ordering), an explicit note saying so rather
than a fabricated one.

## Terminology Drift Is a Real Documentation Bug, Not a Nitpick

The "six-signal adaptive router" language (in the README, learning notes, `prd.md`, and `trd.md`)
turned out to describe a real router that scores four signals, with a fifth (Health) applied as an
upstream pre-filter and a sixth (Capacity) folded into Load rather than standing alone. "Six" is
actually the count of *tunable parameters* `internal/tuning/space.go` searches — a different, also
correct, number that got conflated with the signal count somewhere across the project's own
history. Neither number is wrong on its own terms; using them interchangeably is what misleads a
reader. Corrected everywhere it was practical to do so without rewriting already-closed historical
stage artifacts (Stage7.md got an appended correction rather than an edit to its original text, to
keep its own record intact).

## Limitations

Six PRD/TRD-promised capabilities remain unbuilt after this stage, now disclosed rather than
silently absent — see `docs/StageArtifacts/Stage9.md`'s own Limitations section for the full list
(ExperimentEngine interface, manifest/provenance, traffic generator, automated queueing attribution,
SWR caching, declarative YAML chaos + metamorphic tests). Building them is Stage 10's explicit scope,
not deferred indefinitely — a full type-level design for all nine items already exists from Stage
9's own planning process and needs no rediscovery when that stage starts.

## What Remains

Stage 10: build the nine features named above, in the dependency order already worked out (traffic
generation and the attribution engine first, since several other items depend on them; the
`ExperimentEngine` interface and the declarative chaos engine last, since they compose smaller
pieces rather than inventing from a blank page; the tuner-tier progression as a fully independent
track). Until then, this project's honest status is: a scientifically sound core (validated twice
now, once at each stage's original close and again under this stage's adversarial re-check),
wrapped in documentation that finally says, everywhere a reader would look, exactly what that core
does and does not yet include.
