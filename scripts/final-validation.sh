#!/usr/bin/env bash
# FlashFlow final validation: the one command master context rule 36
# asks for to establish release readiness. Every step below maps
# directly onto rule 36's own checklist (formatting, static checks,
# tests, deterministic replay tests, statistical validation, core
# benchmark suite, holdout evaluation, final challenge suite), plus the
# same "validate the validation machinery" gates this project has
# applied at every stage (006-A, 007-A, 007-F, 008-A, 008-E all have
# explicit, internal correctness assertions with a real failure
# condition -- not just "did it run without crashing").
#
# Exit code 0 means every gate passed. A non-zero exit means at least
# one did; the printed matrix at the end says exactly which.
#
# Usage:
#   ./scripts/final-validation.sh          # full run (~2-3 minutes)
#   ./scripts/final-validation.sh --quick  # skips the slower Stage 8
#                                           # informational reruns (008-B/D/F/G)
#                                           # and the real-engine load sweep

set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

QUICK=0
for arg in "$@"; do
  case "$arg" in
    --quick) QUICK=1 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

STEP_NAMES=()
STEP_RESULTS=()
FAILED=0

# step runs one gate, records PASS/FAIL, and continues regardless --
# the whole point of a validation matrix is seeing every result in one
# run, not stopping at the first failure.
step() {
  local name="$1"; shift
  echo ""
  echo "--- ${name} ---"
  if "$@"; then
    STEP_NAMES+=("$name")
    STEP_RESULTS+=("PASS")
  else
    STEP_NAMES+=("$name")
    STEP_RESULTS+=("FAIL")
    FAILED=1
  fi
}

check_gofmt() {
  local unformatted
  unformatted="$(gofmt -l .)"
  if [ -n "$unformatted" ]; then
    echo "unformatted files:"
    echo "$unformatted"
    return 1
  fi
  return 0
}

echo "=========================================================================================="
echo " FlashFlow Final Validation"
echo "=========================================================================================="

# --- formatting, static checks, tests (master context rule 36) ---
step "Formatting (gofmt)" check_gofmt
step "Build (go build)" go build ./...
step "Static checks (go vet)" go vet ./...
step "Tests, all packages (go test)" go test ./... -count=1

# --- deterministic replay tests ---
step "Deterministic replay (identity/divergence/isolation)" go test ./internal/replay/... -run "TestRunWorld_(IdentityDeterministic|DivergenceOnlyAfterInterventionPoint|Isolation)" -v -count=1

# --- statistical validation ---
step "Statistical validation (006-A)" go run ./cmd/experiment-006a
step "Adaptive signal validation (007-A)" go run ./cmd/experiment-007a
step "Counterfactual identity (007-F)" go run ./cmd/experiment-007f
step "Tuning machinery validation (008-A)" go run ./cmd/experiment-008a
step "Adversarial tuner test (008-E)" go run ./cmd/experiment-008e

# --- final challenge suite (golden scenario + adversarial cases) ---
step "Golden scenario + challenge suite" go test ./internal/challenge/... -v -count=1

# --- core benchmark suite ---
step "Core benchmark suite (go test -bench)" go test ./... -bench=. -benchmem -run=^$

if [ "$QUICK" -eq 0 ]; then
  # --- holdout evaluation, and the other Stage 8 experiments: these are
  # informational reruns of research results, not correctness gates --
  # each one's own numbers may legitimately shift as the codebase
  # evolves (a routing change could genuinely change the tuner's
  # winner). They're included here because rule 36 explicitly names
  # "holdout evaluation" as part of final validation, and rerunning
  # them confirms the whole pipeline still executes end to end, not
  # that any specific number stays fixed.
  step "Random search (008-B, informational)" go run ./cmd/experiment-008b
  step "Holdout evaluation (008-C)" go run ./cmd/experiment-008c
  step "Sensitivity analysis (008-D, informational)" go run ./cmd/experiment-008d
  step "Final policy evaluation (008-F, informational)" go run ./cmd/experiment-008f
  step "Open-loop load sweep (008-G, informational, real HTTP)" go run ./cmd/experiment-008g
else
  echo ""
  echo "--quick: skipping 008-B/C/D/F/G informational reruns and the real-engine load sweep."
fi

echo ""
echo "=========================================================================================="
echo " Final Validation Matrix"
echo "=========================================================================================="
for i in "${!STEP_NAMES[@]}"; do
  printf "%-65s %s\n" "${STEP_NAMES[$i]}" "${STEP_RESULTS[$i]}"
done

echo ""
if [ "$FAILED" -ne 0 ]; then
  echo "FINAL VALIDATION FAILED -- see the matrix above for which gate(s) failed."
  exit 1
fi
echo "FINAL VALIDATION PASSED"
