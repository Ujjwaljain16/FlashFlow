// Package provenance builds the experiment manifest/provenance record
// the PRD promises (§8.8, TRD §9) -- missing per the Stage 8 audit's
// F-05, whose recommended fix this package implements exactly: a
// per-run record of the seeds that produced it, a hash of its
// configuration, and the git commit it ran under. TRD §9 also lists
// config.yaml/events.jsonl/metrics.csv/summary.json/statistics.json/
// replay.json under runs/<id>/ -- deliberately out of scope here (see
// manifest.go's own doc comment); pointing existing output writers
// (vtime.Trace.WriteJSONLFile, tuning.SearchResult) at runs/<id>/ is a
// natural follow-up, not required to close F-05.
package provenance

import (
	"fmt"

	"flashflow/internal/replay"
)

// SeedFingerprint renders a SeedTree as a short, human-readable string
// for manifest/log display -- not a hash or an identity, just a compact
// way to show "which five numbers produced this run" without printing
// five separate int64 fields inline everywhere a Manifest gets logged.
func SeedFingerprint(s replay.SeedTree) string {
	return fmt.Sprintf("global=%d traffic=%d topology=%d failure=%d policy=%d",
		s.Global, s.Traffic, s.Topology, s.Failure, s.Policy)
}
