package provenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"flashflow/internal/replay"
)

func TestManifest_WriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{
		ExperimentID:      "008-tuning-validation",
		Name:              "random-search-v1",
		Seeds:             replay.DeriveSeeds(42),
		ConfigurationHash: "abc123",
		GitCommit:         "deadbeef",
		GitDirty:          true,
		TunerVersion:      "random-search-v1",
		CreatedAt:         time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
	}
	if err := m.Write(dir); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	path := filepath.Join(dir, "008-tuning-validation", "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written manifest: %v", err)
	}
	var got Manifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parsing written manifest: %v", err)
	}
	// Compared field-by-field rather than via == on the whole struct:
	// time.Time carries an internal monotonic-clock reading/representation
	// that a JSON round-trip through RFC3339 legitimately normalizes away,
	// so a raw struct comparison would be a flaky, representation-
	// sensitive check rather than the semantic one .Equal() gives.
	if got.ExperimentID != m.ExperimentID || got.Name != m.Name || got.Seeds != m.Seeds ||
		got.ConfigurationHash != m.ConfigurationHash || got.GitCommit != m.GitCommit ||
		got.GitDirty != m.GitDirty || got.TunerVersion != m.TunerVersion {
		t.Fatalf("round-tripped manifest differs: got %+v, want %+v", got, m)
	}
	if !got.CreatedAt.Equal(m.CreatedAt) {
		t.Fatalf("CreatedAt differs: got %v, want %v", got.CreatedAt, m.CreatedAt)
	}
}

func TestManifest_Write_RejectsPathTraversalExperimentID(t *testing.T) {
	dir := t.TempDir()
	cases := []string{"..", "../escape", "a/b", "."}
	for _, id := range cases {
		m := Manifest{ExperimentID: id}
		if err := m.Write(dir); err == nil {
			t.Errorf("Write with ExperimentID=%q: expected an error", id)
		}
	}
}

func TestManifest_Write_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{ExperimentID: "fresh-experiment", CreatedAt: time.Now()}
	if err := m.Write(dir); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fresh-experiment", "manifest.json")); err != nil {
		t.Fatalf("expected manifest.json to exist: %v", err)
	}
}
