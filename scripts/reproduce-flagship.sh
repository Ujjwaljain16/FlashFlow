#!/usr/bin/env bash
# Reproduces FlashFlow's flagship demonstration
# (docs/StageArtifacts/Stage16-FlagshipDemo.md) from a clean checkout.
#
# The flagship scenario is fully specified in cmd/experiment-016-flagship
# (5 heterogeneous targets, 15-75ms service times, Capacity=1, a
# FlashCrowd workload peaking at t=2.5s, 8s horizon) and requires no
# external services, network access, or manual configuration -- it is a
# pure virtual-time simulation. It runs three independent seeds
# (16000, 16001, 16002) with genuine arrival-stream jitter and reports,
# for all six routing policies: mean/p99 latency, the bottleneck
# target's peak queue depth, committed backlog, fraction of the run
# spent over capacity, and whether that target's queue ever fully
# drains within the horizon -- plus an ASCII queue-depth timeline for
# one representative seed.
#
# Expected result (see docs/StageArtifacts/Stage16-FlagshipDemo.md for
# the full walkthrough): EWMA and Adaptive both show severe, often-non-
# draining acute collapse on their own bottleneck target; round-robin
# shows a different, chronic failure (low committed backlog, but the
# highest fraction-of-time-over-capacity of the six, and never drains);
# weighted-round-robin, least-connections, and P2C-load all stay
# comparatively mild. This qualitative outcome is expected to hold in
# EVERY seed -- the script does not select or hide any seed's result.
#
# Usage:
#   ./scripts/reproduce-flagship.sh

set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

echo "======================================================================"
echo " FlashFlow Flagship Reproduction"
echo " Commit: $(git rev-parse HEAD 2>/dev/null || echo 'unknown (not a git checkout)')"
echo " Go version: $(go version)"
echo "======================================================================"
echo

if ! go build ./... ; then
  echo "FAILED: go build did not succeed -- cannot proceed." >&2
  exit 1
fi

if ! go run -buildvcs=true ./cmd/experiment-016-flagship; then
  echo "FAILED: the flagship experiment did not complete successfully." >&2
  exit 1
fi

echo
echo "Result JSON: experiments/016-final-synthesis/results/016-flagship-results.json"
echo "Full narrative walkthrough: docs/StageArtifacts/Stage16-FlagshipDemo.md"
echo "Flagship reproduction complete."
