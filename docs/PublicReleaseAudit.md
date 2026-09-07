# Public Release Audit

Audit performed against commit `2382110` (2026-09-07). This is a snapshot, not a standing guarantee —
re-run the checks below before any future public push if the tree has changed materially.

**Update, same day**: §1's "uncommitted work" and §7's first "remaining issue" both describe the
`Concentrated`-field fix as in-progress -- it was completed and committed in `2382110` itself (this
audit's own reference commit). Since then, a from-scratch independent audit (12 parallel agents) found
and this repository fixed a further 3 P0s, 5 P1s, and 9 P2/P3s across the diagnostic classifier, the real
engine's chaos injection, the SWR cache, Prometheus metrics, and several Stage 11-16 documentation
overclaims -- see the commit log between `2382110` and the current `HEAD` for the full list. §7's other two
items (a full-tone read of `docs/audit/*.md`, and Stress Map's fixed 3-target topology) remain open exactly
as scoped here.

## 1. Repository audit

| Check | Result |
|---|---|
| Tracked files | 764 (415 JSON result/fixture files, 258 Go, 73 Markdown, 5 shell scripts, 1 JS, 1 CSS, 1 HTML, plus config files) |
| Unexpected binary/IDE/artifact files | None found — no `.exe`/`.dll`/`.pyc`/`.class`/`.zip`/IDE project files tracked |
| Working tree at audit time | Not clean — see below |
| `.gitignore` coverage | Covered: build artifacts, test/profiling output, IDE folders, OS files, scratch dirs. **Gap found and fixed by this pass**: `.env`/`.env.*`/`*.pem`/`*.key`/`id_rsa*`/`*.pfx` were not previously ignored — added as defense-in-depth even though none are currently tracked (see §3). |

**Uncommitted work at audit time**: in-progress bug fix to `internal/report`'s concentration/diversion
narrative logic (adding a shared `Concentrated` field to `Metrics` so the CLI, dashboard JS, and
`Classify()` read one boolean instead of each re-deriving a threshold), plus a new
`internal/report/scenario_report_test.go`. No secrets or personal paths in this diff. A public snapshot
should have this either committed or explicitly resolved — it is not part of this productization pass and
is left as the repository owner's own decision.

## 2. Claims audit

Every number this pass surfaces publicly (README "FlashFlow in numbers," dashboard Overview stat row) was
checked against its cited source rather than taken from a draft. Two draft figures were wrong and were
**not** used:

| Claim | Verified figure | Source | Note |
|---|---|---|---|
| Virtual engine throughput | **~2.53M events/sec** (2,531,300 exact) | `docs/StageArtifacts/Stage5.md`, "Performance"; `experiments/005-virtual-time/README.md` §3 | Confirmed correct as drafted. |
| HTTP keep-alive throughput | **3.06×** | `docs/StageArtifacts/Stage2.md`, Experiment 002-A1; `experiments/002-http-reverse-proxy/README.md` §3 | Draft said "~3.1×" — corrected. Measured at c=100 only; other concurrency levels show different ratios (~2.97× at c=1). |
| EWMA vs Adaptive at Capacity=1 | **8×** mean-latency gap, Cliff's Delta=1.000, 12-seed confirmation | `docs/StageArtifacts/Stage12.md`; `README.md`; `Stage16-ClaimLedger.md` claim C15 | Draft also proposed "~4.7×" for this same comparison — **that figure does not exist anywhere in this repository** for EWMA-vs-Adaptive at Capacity=1 and was not used. (An unrelated "~4.5×" figure exists in Stage 14, describing Adaptive's own latency degradation under FlashCrowd vs. its own constant-load baseline — a different comparison entirely; not substituted in.) |
| P2C vs EWMA seed separation | **8/8 vs 0/8** seeds show `committed_backlog>50` | `Stage16-ClaimLedger.md` claim C25 | Confirmed exact. |
| Deterministic replay | Two distinct claims, not one "zero divergence" claim | `Stage16-ClaimLedger.md` claims C13 (two policies replayed on the identical trace diverge *only after* their own decisions differ) and C31 (byte-for-byte reproducible, scope-limited to same machine/toolchain, virtual engine only — the real engine is explicitly **not** byte-identical across reruns) | A blanket "zero trace divergence" framing would overstate C31's own stated scope; both claims are used with their actual qualifiers intact. |
| N=3/5/8 generalization | Rank/ordering agreement generalizes; the raw ρ≈0.89-0.97 threshold does **not** | `docs/StageArtifacts/Stage14.md` ("CENTRAL FINDING"); `Stage16-ClaimLedger.md` claims C20/C22 | Framed everywhere as "which policy handles concentration better" generalizing, never as a stable numeric threshold — Stage 14 is explicit that "rho's own predictive power decreases as target count grows even as severity worsens." |

No number appears in this pass's public copy without a named source stage and, where practical, a direct
link to that stage's artifact.

## 3. Security audit

Read-only scan of the tracked working tree and full git history (`--all`, 200 commits — small enough that
the scan was exhaustive, not sampled).

| Pattern searched | Tracked tree | Full history (`git log --all -p`) |
|---|---|---|
| `password`, `secret`, `api[_-]?key` | No real hits (only the English word "secretly" in simulation prose, and audit-doc sentences stating no secrets were found) | Same — no additional hits |
| Private-key headers (`BEGIN RSA/OPENSSH/PGP/PRIVATE`) | None | None |
| AWS access-key-shaped strings (`AKIA[0-9A-Z]{16}`) | None | None |
| `C:\Users\`, `/home/`, `/Users/` (personal/machine paths) | None | — |
| `mongodb://`, `postgres://`, credentials embedded in a URL | None | — |
| Files ever added named `.env`, `.pem`, `.key`, or containing `credentials`/`secret` | — | None — no such file was ever added on any branch |
| Real-looking personal email addresses | None | — |

**Conclusion: clean.** No secrets, private keys, cloud credentials, embedded-URL credentials, or
personal/machine-specific paths were found in the current tree or anywhere in git history. This
corroborates the project's own prior internal audit (`docs/audit/SECURITY_AND_OPERATIONS.md`), re-run here
independently rather than taken on faith.

No scratch/internal-only-named files are tracked (`git ls-files` has no path containing
`scratch`/`todo`/`prompt`/`agent-notes`/`.local`). No private AI-agent prompts, internal task instructions,
or planning-conversation transcripts are tracked anywhere in the repository — the Stage artifact documents
describe research methodology and findings, not the tooling used to produce them.

## 4. Frontend audit

Confirmed by direct read of `cmd/dashboard/static/index.html`, `app.js`, and every file under
`internal/dashboard/`: all classification (`STABLE`/`ACUTE_COLLAPSE`/`CHRONIC_COLLAPSE`/
`RECOVERY_LIMITED`, mechanism, reason, concentration) happens exclusively in Go
(`internal/report.Classify`/`AnalyzeTarget`/`Mechanism`), called only from `internal/dashboard/canonical.go`.
The dashboard is a thin renderer of that JSON — there is one source of diagnostic truth, not two. The one
JS-side artifact worth naming: `CLASS_SUBTITLE` in `app.js` is a hand-maintained *display caption* mirror
of `report.ClassificationSubtitle` (plain text, not logic) — it could in principle drift from the Go
source if one side is edited without the other; noted here rather than fixed, since collapsing it into a
single source would require either an extra API round-trip for static caption text or embedding Go-served
strings into every badge render, neither clearly better than the current small, explicit duplication.

Before this pass: no landing/hero experience existed (the dashboard loaded directly into a dense
"Control Room" of data panels), and navigation used developer-shaped tab names ("Control Room / Playground
/ Experiments / Tuning") rather than public-facing task names. This pass's dashboard IA rework (see
`docs/PublicReleaseGuide.md`) addresses both without touching any of the Go endpoints or classification
logic above.

## 5. Reproducibility audit

Before this pass, `cmd/flashflow`'s three subcommands (`report`/`explain`/`stress-map`) had a working
top-level `--help`, but no subcommand-level help (`flashflow report -h` fell through to Go's bare
auto-generated flag dump, not a description of what the command does), and no `--json` stdout mode (only
`report` wrote a JSON file to disk as a side effect; there was no way to pipe any subcommand's output into
another tool). This pass added a real `Usage` func per subcommand and a `--json` flag to all three,
verified by running each `--help` and `--json` invocation directly (see commit for this pass).

## 6. Public/internal boundary

| Internal | Public-facing equivalent |
|---|---|
| `Stage11`...`Stage17`, `015a`, `016-flagship` | Kept as evidence metadata / links in a "Research History" section, not primary navigation |
| `internal/report`, `internal/backlog`, package names | Not exposed as UX concepts — dashboard tabs are named by user intent (Compare, Diagnose, Stress Map, Evidence, Reproduce) |
| Stage artifact markdown (`docs/StageArtifacts/`, `docs/learning/`, `docs/audit/`) | Remain public on GitHub (legitimate project history, no sensitive material — confirmed by §3) but are not the landing experience |
| AI-assisted development process | Not part of the public narrative anywhere — the Stage documents describe research methodology and evidence, never the tooling or prompts used to produce them (confirmed no such material is tracked, §3) |

## 7. Remaining issues (not addressed by this pass)

- The in-progress `Concentrated`-field bug fix noted in §1 is uncommitted; resolve before treating the
  tree as release-ready.
- `docs/audit/*.md` (the project's own prior internal audits) are public and were skimmed for this pass
  but not read line-by-line for tone — they read as legitimate technical audit documents, not internal
  scratch notes, but a final human pass before a public push is still worthwhile.
- Stress Map's target-count is fixed at 3 (Stage 13's own topologies); extending it to a user-facing
  3/5/8 selector was explicitly scoped out of this pass (see `docs/PublicReleaseGuide.md`) since it would
  require generating new heterogeneity topologies — new experimental surface, not productization.
