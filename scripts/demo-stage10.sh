#!/usr/bin/env bash
# Reset-and-run helper for the Stage 10 hero demo (cmd/demo-stage10).
#
# What "reset" means here, precisely: remove only demo/output/ (the
# provenance manifest this demo writes on every run) and recreate it
# empty. Nothing under experiments/ (this project's real, historical
# research artifacts) is ever touched by this script -- a demo reset
# must never look like it could plausibly delete research history, even
# though this demo doesn't write there in the first place.
#
# Usage:
#   scripts/demo-stage10.sh           # reset + run once
#   scripts/demo-stage10.sh --no-reset  # run without clearing prior demo output

set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

if [[ "${1:-}" != "--no-reset" ]]; then
  echo "Resetting demo/output/ (does not touch experiments/ or any git-tracked file)..."
  rm -rf demo/output
fi
mkdir -p demo/output

echo "=========================================================================================="
echo " FlashFlow Stage 10 Demo"
echo "=========================================================================================="
echo ""

# -buildvcs=true: `go run`'s default (-buildvcs=auto) silently omits Go's
# VCS build-info stamping in this environment, which would leave the
# provenance manifest's git_commit/git_dirty fields empty. This is a
# real, previously-found demo-readiness gap (see
# docs/StageArtifacts/Stage10DemoValidation.md) -- not a hypothetical
# precaution.
go run -buildvcs=true ./cmd/demo-stage10
status=$?

echo ""
echo "=========================================================================================="
if [ $status -eq 0 ]; then
  echo " Demo completed successfully. Evidence written to demo/output/stage10-demo/manifest.json"
else
  echo " Demo exited with a non-zero status ($status) -- see output above for which step failed."
fi
echo "=========================================================================================="
exit $status
