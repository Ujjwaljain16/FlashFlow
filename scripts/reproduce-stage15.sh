#!/usr/bin/env bash
# Reproduces every Stage 15 (mechanism-identification) experiment from a
# clean checkout, in the exact order they were originally run. Each
# experiment is self-contained (its own scenario, its own seed(s), no
# shared state with any other), writes its own timestamped JSON result
# to experiments/015-mechanism-identification/results/, and re-running
# it is expected to reproduce the SAME numbers reported in
# docs/StageArtifacts/Stage15.md -- the virtual engine is deterministic
# given the same seed (see internal/vtime's own identity tests), so
# every field except the "timestamp" field in the output JSON should be
# byte-identical across reruns on the same machine/Go toolchain version.
#
# Usage:
#   ./scripts/reproduce-stage15.sh
#
# This script does not modify any source file. It only runs `go run`
# against existing cmd/experiment-015* binaries and writes result JSON
# to the existing experiments/ directory tree.

set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

FAILED=0
run_experiment() {
  local name="$1"
  echo "=== $name ==="
  if go run -buildvcs=true "./cmd/$name"; then
    echo "--- $name: OK ---"
  else
    echo "--- $name: FAILED ---" >&2
    FAILED=1
  fi
  echo
}

echo "Reproducing Stage 15 (internal/backlog + experiment-015a through 015f)"
echo "Commit: $(git rev-parse HEAD 2>/dev/null || echo 'unknown (not a git checkout)')"
echo

echo "--- internal/backlog unit tests (hand-computed expected values) ---"
if go test ./internal/backlog/... -v; then
  echo "--- internal/backlog: OK ---"
else
  echo "--- internal/backlog: FAILED ---" >&2
  FAILED=1
fi
echo

run_experiment experiment-015a   # canonical scenario, full 6-policy backlog dynamics
run_experiment experiment-015b   # falsification program (F1, F3, F4, F6)
run_experiment experiment-015c   # Adaptive signal ablation
run_experiment experiment-015d   # cache-affinity mechanism test
run_experiment experiment-015e   # predictor generalization across topology/workload
run_experiment experiment-015f   # real-engine validation

echo "Result artifacts written to experiments/015-mechanism-identification/results/"
if [ "$FAILED" -eq 0 ]; then
  echo "All Stage 15 experiments reproduced successfully."
  exit 0
else
  echo "One or more Stage 15 experiments FAILED to reproduce. See output above." >&2
  exit 1
fi
