package provenance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"flashflow/internal/replay"
)

// Manifest is one experiment run's provenance record: the seeds that
// produced it, a hash of its configuration, and the git commit it ran
// under. This is deliberately narrower than TRD §9's full runs/<id>/
// directory (config.yaml, events.jsonl, metrics.csv, summary.json,
// statistics.json, replay.json) -- it builds exactly what the Stage 8
// audit's F-05 recommended fix asked for (seed + git commit + config
// hash, via a real SeedTree rather than a single flat seed), not the
// complete TRD file set. Pointing existing output writers at
// runs/<id>/ is a natural follow-up, not required to close F-05.
type Manifest struct {
	ExperimentID      string          `json:"experiment_id"`
	Name              string          `json:"name"`
	Seeds             replay.SeedTree `json:"seeds"`
	ConfigurationHash string          `json:"configuration_hash"`
	GitCommit         string          `json:"git_commit"`
	GitDirty          bool            `json:"git_dirty"`
	TunerVersion      string          `json:"tuner_version,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
}

// safeExperimentID rejects path-separator and parent-directory segments
// in ExperimentID before it is ever used to build a filesystem path --
// ExperimentID isn't attacker-controlled HTTP input the way
// internal/dashboard's group/file parameters are, but this project's
// own Stage 8 audit (F-01) found real value in not trusting a
// string-typed path SEGMENT just because its current callers are
// trusted, so the same discipline is applied here from the start rather
// than retrofitted later.
var safeExperimentID = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func isSafeExperimentID(id string) bool {
	return safeExperimentID.MatchString(id) && id != "." && id != ".."
}

// Write serializes m to runsRoot/<ExperimentID>/manifest.json, creating
// the directory if needed.
func (m Manifest) Write(runsRoot string) error {
	if !isSafeExperimentID(m.ExperimentID) {
		return fmt.Errorf("provenance: invalid experiment id %q", m.ExperimentID)
	}
	dir := filepath.Join(runsRoot, m.ExperimentID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("provenance: creating %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("provenance: marshaling manifest: %w", err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, b, 0644); err != nil {
		return fmt.Errorf("provenance: writing %s: %w", path, err)
	}
	return nil
}
