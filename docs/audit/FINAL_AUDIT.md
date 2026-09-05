# FlashFlow — Final Independent Audit (Post-Stage-8)

> **Status update (Stage 9, post-remediation):** every finding below has since been fixed or
> explicitly disclosed — see [RESOLUTION.md](RESOLUTION.md) for the full per-finding disposition and
> `docs/StageArtifacts/Stage9.md` for the narrative. This document is preserved as-written: the
> audit's own point-in-time snapshot and verdict, not retroactively edited to reflect the fixes that
> followed. Read it as "what an adversarial review found the day Stage 8 was declared complete," not
> as this repository's current state.

**Audit date:** 2026-09-05
**Scope:** Full repository as of the current working tree (HEAD `cf60268`, plus all uncommitted
Stage 8 work — see [F-03](FINDINGS.md)), against `prd.md`, `trd.md`, `research.md`, all 8
`docs/StageArtifacts/`, all 8 `docs/learning/` notes, and the actual source tree.
**Method:** 13 independent adversarial review passes covering requirements traceability,
architecture, concurrency/determinism, state machines, counterfactual replay, statistics, the
adaptive router/tuner, cache/health/network/proxy, dashboard/security/reproducibility, experiment
provenance/git history, documentation honesty, virtual-time engine internals, and real-vs-virtual
engine parity/performance — plus an independent re-run of `gofmt`/`go vet`/`go build`/`go test
./...` rather than trusting prior stage claims. Findings were cross-referenced across passes;
several were independently confirmed by 2–4 reviewers approaching from different code paths, which
materially raises confidence in those specific results.

Companion documents: [FINDINGS.md](FINDINGS.md) (full itemized register) ·
[REQUIREMENT_TRACEABILITY.md](REQUIREMENT_TRACEABILITY.md) ·
[ARCHITECTURE_AUDIT.md](ARCHITECTURE_AUDIT.md) · [SCIENTIFIC_VALIDITY.md](SCIENTIFIC_VALIDITY.md) ·
[SECURITY_AND_OPERATIONS.md](SECURITY_AND_OPERATIONS.md).

---

## Executive verdict

```
OVERALL STATUS:              YELLOW

PRD COMPLIANCE:               Strong on the routing/replay/statistics/tuning core (the parts the
                               project itself calls the "Uniqueness Layer"); weak on several named
                               "core, not optional" and "Added in v3.1" feature commitments that
                               were never built and, in a few specific cases, never disclosed as
                               cut. See REQUIREMENT_TRACEABILITY.md — roughly 12 of 18 major
                               requirements land at IMPLEMENTED+VERIFIED; the rest are honest
                               partial implementations, undisclosed gaps, or documentation drift.

TRD ALIGNMENT:                The package-boundary map in TRD §1 is fully stale (no internal/router,
                               internal/traffic, internal/chaos, internal/provenance,
                               internal/analysis, internal/telemetry, internal/engine/*, or
                               cmd/flashflow exist) — but the responsibilities that DID get built
                               relocated sensibly (routing genuinely lives in internal/proxy). The
                               responsibilities that did NOT get built (traffic generation, chaos-
                               as-YAML, provenance/manifest, telemetry-as-HdrHistogram/Prometheus)
                               have no real home anywhere, not merely a renamed one.

RESEARCH ALIGNMENT:           research.md's own "Proposed" architecture is materially larger than
                               what PRD/TRD scoped or what shipped (bandits, Bayesian optimization,
                               DR-OPE/CausalSim, Parquet/OTel). PRD correctly narrows this via its
                               own Non-Goals section, so this is not itself a broken promise — but
                               research.md's "Critical Novelty Assessment" table doesn't
                               consistently mark aspirational rows as such, which is a real,
                               self-contained documentation gap.

CRITICAL BUGS:                Zero P0-class CORRECTNESS bugs found in the core scientific claims —
                               no counterfactual state leakage, no holdout leakage, no broken
                               statistical implementation, no non-monotonic routing signal. This
                               is the single most important negative result of the audit and was
                               specifically, adversarially hunted for across two dedicated passes.
                               Three P0s WERE found, but all three are process/documentation/
                               security, not scientific-correctness, defects (see below).

RELEASE BLOCKERS (P0):        1. Demonstrated, HTTP-reachable path-traversal bug in the dashboard,
                                  directly contradicting the project's own "unit-tested,
                                  path-traversal-safe" claim (F-01).
                               2. The README's literal "Resume Line" and prd.md/trd.md assert a
                                  Docker/`tc netem` real-emulation engine that was never built,
                                  contradicting the project's own honest Stage 4 documentation of
                                  the substitution (F-02).
                               3. Stage 8 — the stage being declared final — has zero git commits;
                                  a clean checkout reproduces Stages 1-7 only (F-03).

SCIENTIFIC VALIDITY:          Sound where it was tested. The replay engine's isolation/identity/
                               divergence guarantees are real and were independently re-run (not
                               just re-read); the four statistics implementations are algorithmically
                               correct with genuine (not theater) test coverage; every headline
                               number in Stage 8's exit doc was traced to raw JSON and matched
                               exactly. The one substantive caveat: the tuner's "generalization"
                               claim is narrower than its own framing implies — Holdout is drawn
                               from the identical scenario distribution as Development, differing
                               only in seed, which demonstrates robustness to sampling noise, not
                               to a genuine distribution shift (F-17), and this narrower scope is
                               undisclosed.

REPRODUCIBILITY:               Fails today, mechanically: Stage 8 is uncommitted (F-03), and
                               internal/netsim's RNG is wall-clock-seeded in the two real
                               experiments that use it (F-13), so their per-run loss/jitter
                               sequences aren't actually reproducible despite the project's stated
                               determinism discipline. The DETERMINISTIC ENGINE ITSELF (vtime,
                               replay) is genuinely reproducible and was verified as such.

SECURITY:                      One real, demonstrated finding (F-01, dashboard path traversal),
                               compounded by binding to all interfaces rather than localhost
                               (F-21). Everything else checked (shell-injection surface, secrets,
                               credential handling, supply chain) is clean. Calibrated against the
                               project's own stated local-tool threat model, not CDN standards.

PERFORMANCE:                   No accidental O(n²) found anywhere, confirmed by an independent
                               benchmark sweep to n=500 targets. Untested beyond n≈5 in the
                               committed suite, which is an honest scale boundary rather than a
                               defect given the project's explicit small-topology scope.

DOCUMENTATION:                 The project is UNUSUALLY self-critical and honest in its
                               stage-internal documentation (Stage exit artifacts and learning
                               notes actively disclose limitations, including several corrections
                               to its own earlier mistakes). The failures are concentrated almost
                               entirely in the documents that get the least maintenance churn:
                               README.md's status table and Resume Line, prd.md/trd.md's
                               architecture sections, and research.md's novelty table — none of
                               which were updated as the project's actual, honestly-tracked scope
                               narrowed stage by stage.

BIGGEST RISKS:                 1. A technical reviewer who reads only README.md/prd.md/trd.md
                                   would materially overestimate what was built (Docker/tc netem,
                                   YAML chaos, a manifest/provenance system, an automated queueing-
                                   attribution engine, HdrHistogram/Prometheus, a 3-tier tuner) —
                                   while a reviewer who reads only docs/StageArtifacts/ would get
                                   an accurate and appropriately humble picture. The gap between
                                   these two readings is the project's central risk.
                               2. The dashboard's security posture doesn't match its own stated
                                   claims or its own stated threat model.
                               3. "Final" is asserted about a stage that isn't in version control.

WHAT SHOULD BE FIXED BEFORE
RELEASE:                        The three P0s (F-01, F-02, F-03) and the reproducibility-breaking
                               F-13 netsim seed. All are cheap, mechanical fixes relative to the
                               engineering the project has already done — none require new science.

WHAT CAN SAFELY BE DEFERRED:   Building the actually-missing PRD/TRD features (traffic generator,
                               SWR, YAML chaos, manifest/provenance, HdrHistogram/Prometheus,
                               LHS/Bayesian tuning) can be deferred exactly as the project's own
                               "earn-the-abstraction" philosophy would suggest — PROVIDED the docs
                               are corrected to stop claiming they exist. Documentation honesty and
                               feature completeness are separable problems; only the former is
                               urgent.
```

## Can FlashFlow be considered final?

**NO — significant fixes required, but they are fast and mechanical, not scientific.**

This is not a verdict driven by any defect in FlashFlow's actual research contribution. The
project's hardest, highest-risk engineering claims — that its counterfactual replay engine
genuinely isolates endogenous state, that its adaptive router's tuning is genuinely holdout-clean,
that its statistics are genuinely correct — all survived two dedicated adversarial audit passes
each, one of which independently re-ran the test suite rather than trusting prior claims. Nothing
found in that core would embarrass the project in front of a skeptical technical reviewer who asks
"show me the code, show me the test, show me the raw evidence" — the traceability from headline
number to raw JSON is real and was checked exhaustively.

What blocks a "final" verdict is narrower and more fixable:

1. **A real security defect exists exactly where the project claims one doesn't** (F-01) — this is
   the kind of finding that, left uncorrected, is far more damaging on discovery than the schedule
   cost of fixing it now.
2. **The project's most externally-visible sentence (the README Resume Line) makes a specific,
   checkable, false technical claim** (F-02) that the project's own internal docs already know is
   false. This is a five-minute fix with an outsized reputational cost if left as-is.
3. **"Final" is asserted about ungoverned state** (F-03) — Stage 8 needs to actually be committed,
   in the incremental style the rest of the project already uses, before the word "complete" means
   what it's supposed to mean.

None of these require new research, new algorithms, or revisiting any of the project's actual
findings. They require: fixing one regex, editing three sentences across README/prd/trd, and
running `git add`/`git commit` in logical chunks. Once done, the honest remaining gap is a
**documentation-completeness** one, not a correctness one: several PRD/TRD "core, not optional"
features were never built, most with a defensible earn-the-abstraction rationale, but a few with no
disclosure trail at all (traffic generator, SWR, the automated queueing-attribution engine, the
manifest/provenance system). Closing that gap means either building those features or — consistent
with how well the project has handled every *other* scope cut — writing down, in one place, that
they weren't built and why. That is a documentation task, not a re-audit trigger.

## Assume we are wrong: the strongest counter-argument

*The strongest case that this audit overstates FlashFlow's problems*: nearly every "undisclosed
gap" found here is undisclosed only in the sense that no single document says "we chose not to
build X" — but the absence of X is trivially discoverable by anyone who runs `grep` the way this
audit's agents did, and the project was never marketed as a production system to an audience who
wouldn't check. Under this reading, F-04 through F-10 are closer to P2 documentation-completeness
items than P1 "serious" findings, and the real signal is simply that PRD v3.1 was written before
implementation started and was never revised — a completely ordinary artifact of iterative
development, not evidence of dishonesty.

This argument is largely correct for the *code-level* gaps (traffic generator, SWR, manifest
system) — they are kept at P1 here specifically because the project holds itself, in its own
stage-internal docs, to a stated standard of disclosing every deliberate cut, and these particular
cuts fall through that standard's own cracks rather than being exempt from it. It does **not**
apply to F-01 (a demonstrated security defect) or F-02 (a specific, external-facing, checkable
false claim in the one sentence explicitly written for outside readers) — those remain P0 under
any reasonable reading, discoverability of the underlying truth notwithstanding, because the
audit's job is to check what a document *claims*, not to assume a skeptical reader will always
verify it themselves.

## Structure of this audit

- **[FINDINGS.md](FINDINGS.md)** — every finding (57 total: 3 P0, 18 P1, 24 P2, 12 P3), in the
  required ID/severity/location/evidence/repro/fix/regression-test format, plus a dedicated
  "confirmed correct" section recording what was specifically hunted for and not found.
- **[REQUIREMENT_TRACEABILITY.md](REQUIREMENT_TRACEABILITY.md)** — the PRD/TRD requirement-by-
  requirement matrix with code/test/experiment/doc citations and status classification.
- **[ARCHITECTURE_AUDIT.md](ARCHITECTURE_AUDIT.md)** — architectural separation, abstraction
  ledger, dead-code sweep, real-vs-virtual engine parity, and performance/scale findings.
- **[SCIENTIFIC_VALIDITY.md](SCIENTIFIC_VALIDITY.md)** — the statistics, counterfactual-replay, and
  adaptive-router/tuner deep dives, including the specific adversarial tests each subsystem was put
  through.
- **[SECURITY_AND_OPERATIONS.md](SECURITY_AND_OPERATIONS.md)** — security, concurrency/resource
  lifecycle, reproducibility, git history, and build/CI/cross-platform findings.
