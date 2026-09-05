# Security, Concurrency, Reproducibility & Operations Audit

## Threat model

FlashFlow's dashboard and CLI tools are single-user, local research/dev tooling reading a
researcher's own experiment artifacts on their own machine — not multi-tenant or internet-facing
infrastructure. `prd.md`/`trd.md` never require dashboard authentication, and Stage 8's own docs
state "no authentication... appropriate for a local development tool" — an accurate characterization
of intent. Findings below are scored against *that* intent, not CDN/production standards, **except**
where the implementation itself breaks its own stated local-only assumption.

## Security

**[F-01](FINDINGS.md) — P0 — Real, demonstrated path traversal in the dashboard's experiment
browser.** The claimed protection (`safeName` regex `^[A-Za-z0-9._-]+$`) does not exclude the
literal string `..`, since `.` is itself an allowed, unbounded-repetition character. This was
proven live against a running `cmd/dashboard` instance: `GET /api/experiments/%2e%2e/proof.json`
returns 200 with content from one level above the experiments root (net/http's own mux blocks the
unencoded `../` form, but not the URL-encoded one). The existing unit test that's supposed to catch
this passes today only because no `results/` directory happens to exist at the repo root — a
false-negative test, not a working guard. Blast radius is bounded (only a single `..` segment is
possible, landing at `<CWD>/results/<file>`, not an arbitrary path), but the control itself is
broken, not just narrow.

**[F-21](FINDINGS.md) — P1 — Dashboard binds to all interfaces (`:7070`), not `127.0.0.1`.**
Combined with F-01 and the complete absence of authentication, this means the traversal exposure —
and the entire no-auth experiment browser — is reachable from anything else on the same network,
not just the operator's machine, contradicting the "local dev tool" framing under which the no-auth
design would otherwise be reasonable.

**F-41 (P2)** — `cmd/dashboard/static/app.js` builds `innerHTML` from disk-sourced experiment/file
names without escaping (one code path only — the JSON-content viewer correctly uses `textContent`).
Not remotely exploitable under the local-only threat model on its own, but it compounds with F-01:
the directory being listed there is itself HTTP-influenced.

**General hygiene — clean.** A repo-wide sweep found **zero** `os/exec` usage anywhere in the
`internal`/`cmd` Go tree (no shell-injection surface in Go code); the only shell scripts
(`scripts/*.sh`) invoke Docker/Go with entirely hardcoded arguments, no interpolation of untrusted
input. No secrets, credentials, or API keys found anywhere via a `password|secret|api[_-]?key|token`
sweep (only unrelated HTTP header-token parsing matched). `go.mod` pins an exact Go version with
zero external dependencies, eliminating a supply-chain surface entirely.

## Concurrency and resource lifecycle

Race-detector confirmation is **unavailable in this environment** — no gcc, `CGO_ENABLED=1` fails
building `runtime/cgo`, consistent with every one of the project's own Stage exit artifacts, which
disclose this honestly rather than claim untested confidence. Findings below are static-inspection
based and labeled as such; each names what verification would actually prove it.

- **[F-11](FINDINGS.md) — P1 — `health.Checker`'s Stop/Start cycle has a latent goroutine leak.**
  `runLoop` reads the `stopCh` *field* directly rather than a captured local value; a Stop-then-Start
  sequence before the old goroutine observes the close leaves it running forever, reading the new
  channel and never exiting. Currently unreachable — no code path in the repo restarts a live
  `Checker` — but a real landmine for any future dynamic-reconfiguration feature. **Verification
  path**: a `Start→Stop→Start` test on a machine with a working C toolchain, under `-race`.
- **[F-12](FINDINGS.md) — P1 — No read/write/idle timeouts on any of the three real
  `net/http.Server` instances** (`internal/proxy`, `internal/topology`, `cmd/dashboard`) — a slow or
  silent client can hold a per-connection goroutine open indefinitely (Slowloris-shaped). Requires
  an adversarial or badly-behaved client, not normal-operation growth.
- **A cache with no eviction bound is reachable from `cmd/edge`'s live handler** given a
  client-controlled key space — but the shipped `cmd/edge` binary never actually enables caching
  by default (`CacheTTL` is never set by its own flags), so this is a dormant landmine in the
  reusable `EdgeConfig` path, not a live issue in the deployed binary.
- **Confirmed clean by inspection**: `internal/vtime` has no goroutines, channels, or mutexes at
  all outside `clock.MockClock`'s own lock (used only by external concurrent readers, not the
  event loop itself); the event queue's tie-break is a deterministic `(timestamp, insertion
  sequence)` comparator, never map-iteration order; `LoadTracker`/`Coalescer`/proxy request paths
  were traced for increment/decrement symmetry and body-close coverage across every return path
  (including error paths) via `defer`, with no leak found. `Coalescer.Do` cleans up its in-flight
  entry unconditionally, including on a recovered panic, before any waiter can be permanently
  blocked.
- **[F-54](FINDINGS.md) (P3)** — a bounded `time.After` timer leak in `netsim.Transport.RoundTrip`
  on context cancellation (should be `time.NewTimer`+`Stop()`); low impact, opt-in code path only.

## Reproducibility

**[F-03](FINDINGS.md) — P0 — Stage 8 is entirely uncommitted; `git clone` reproduces Stages 1–7
only.** `HEAD` predates every Stage 8 file — the dashboard, the tuner, the challenge suite, all
eight `cmd/experiment-008*` binaries, and the two Stage 8 docs are all untracked working-tree
state, and the Stage 7 files `internal/replay/{policies,world}.go` are themselves locally modified
relative to `HEAD`. This is a direct, demonstrated failure of the clean-checkout reproducibility
test this audit specifically performs — "Stage 8 declared complete" is true of one machine's disk,
not of the repository.

**[F-13](FINDINGS.md) — P1 — `internal/netsim`'s RNG falls back to a wall-clock seed in the two
real experiments that use it** (004-F, 006-C), meaning their per-request loss/jitter sequences are
not actually reproducible run-to-run — a direct tension with the project's own stated determinism
discipline. Every unit test, by contrast, passes an explicit seed, so this path is untested.

**Independently reproduced and confirmed clean**: `gofmt -l .`, `go vet ./...`, `go build ./...`,
and `go test ./...` were all re-run directly for this audit (not trusted from Stage 8's own claim)
and pass across all 15 packages, including with the current uncommitted `internal/replay` changes
in place.

## Git history

**[F-19](FINDINGS.md) — P1 — All 80 commits spanning Stage 2 through Stage 7 landed in a single
~13.5-hour window on one calendar day**, with some stage-closing commits seconds to low-minutes
apart, following 12 commits from an earlier session with a starkly different (terse vs. narrative)
commit-message style. This does not invalidate any individual technical finding in this audit —
every headline number checked traced back to real, internally consistent raw evidence — but it
means the commit history provides no independent corroboration of the "genuine, incremental
discovery and correction" development story the project's own docs repeatedly invoke, and Stage 8
itself has no commits at all (see F-03). This should be stated plainly rather than left for a
reader to discover via `git log`.

The **diffs currently sitting uncommitted** in `internal/replay/policies.go`/`world.go` (adding
`WeightedRoundRobinPolicy` and per-request `CompletionRecord` tracking for the tuner's p99/mean
latency objective) were read in full and are coherent, well-commented, and consistent with Stage
8's stated needs — not suspicious in themselves; the three modified experiment-result JSON files
change only their `timestamp` field, consistent with a legitimate deterministic re-run given the
code paths those experiments actually exercise.

## Build / CI / cross-platform

- **No CI configuration exists anywhere** in the repository ([F-32](FINDINGS.md)) — `gofmt`,
  `go vet`, `go build`, `go test`, and `scripts/final-validation.sh` are all developer-run manually
  with no automated enforcement on push/PR. Reasonable for a solo research project; a real gap if
  ever collaborated on or handed off.
- **No `.gitattributes`** ([F-33](FINDINGS.md)) — `git diff` already emits LF→CRLF warnings on
  several files; a contributor or CI runner with different `core.autocrlf` settings would see
  spurious whole-file diffs.
- **`scripts/*.sh` require actual bash** (arrays, `BASH_SOURCE`) with no README statement that Git
  Bash/WSL is required ([F-43](FINDINGS.md)) — confirmed to run correctly in this audit's own
  Git-Bash/MINGW64 environment, but a plain `cmd.exe`/PowerShell user gets an undocumented failure.
- **Docker-dependent capabilities were independently confirmed working on this exact machine**:
  Docker Desktop (WSL2-backed) is installed and running, and the NGINX reference benchmark's result
  file (21.8ms FlashFlow / 24.2ms NGINX, both just above Origin's 20ms artificial delay) is
  internally coherent — not the "impossible ~2ms" number the project's own docs describe as the
  original path-mangling bug's symptom. **Side note worth flagging**: since Docker Desktop's WSL2
  backend runs a genuine Linux kernel, real `tc netem` may now be reachable via a privileged
  container on this same host — the "no Linux host available" constraint behind building
  `internal/netsim` (Stage 4) may no longer hold, a possible stale assumption worth revisiting.
- **`deployments/nginx-bench/nginx.conf;C`** is a confirmed literal leftover directory from the
  documented Windows/Git-Bash path-mangling bug — the fix (`MSYS_NO_PATHCONV=1` on the `docker run`
  invocation) prevents *future* mangled mounts but never cleaned up the artifact the bug had
  already produced during diagnosis. Untracked, so `git status` never surfaces it. Confirmed to be
  exactly the ironic remnant the audit brief suspected, not a new issue ([F-46](FINDINGS.md)).
- **Race detection has never been run at any stage of this project** — consistently and honestly
  disclosed in every Stage's exit artifact (no gcc/CGO available on this host), rather than a
  hidden gap, but it remains a genuine, permanent coverage hole for every mutex/atomic-guarded path
  in the codebase.

## Dashboard-specific checks (beyond F-01)

- Malformed/corrupt JSON artifact handling: confirmed non-crashing — `ReadResultFile` on a
  deliberately corrupted file returns a clean error, converted to a 404 by the handler, no panic.
- Missing/incomplete experiment directories: `ListGroups` explicitly skips directories with no
  `results/` subdirectory rather than failing.
- **[F-42](FINDINGS.md) (P2)** — no response-size bound (`os.ReadFile` on arbitrary trace files) and
  no server-level `ReadTimeout`/`WriteTimeout`/`MaxHeaderBytes` — low risk given single-user local
  operation, but missing baseline hygiene.
- **[F-55](FINDINGS.md) (P3)** — `cmd/dashboard` has no graceful-shutdown wiring, unlike `cmd/proxy`
  and `cmd/edge` which both correctly wire `signal.Notify`+`srv.Shutdown`; low impact since the
  dashboard spawns no background goroutines that would otherwise leak on a hard kill.
