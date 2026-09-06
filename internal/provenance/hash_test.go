package provenance

import "testing"

type sampleConfig struct {
	A int
	B string
}

func TestConfigHash_StableForIdenticalInput(t *testing.T) {
	a, err := ConfigHash(sampleConfig{A: 1, B: "x"})
	if err != nil {
		t.Fatalf("ConfigHash failed: %v", err)
	}
	b, err := ConfigHash(sampleConfig{A: 1, B: "x"})
	if err != nil {
		t.Fatalf("ConfigHash failed: %v", err)
	}
	if a != b {
		t.Fatalf("ConfigHash produced different hashes for identical input: %q vs %q", a, b)
	}
	if len(a) != 16 {
		t.Errorf("ConfigHash length = %d, want 16", len(a))
	}
}

func TestConfigHash_DifferentForDifferentInput(t *testing.T) {
	a, err := ConfigHash(sampleConfig{A: 1, B: "x"})
	if err != nil {
		t.Fatalf("ConfigHash failed: %v", err)
	}
	b, err := ConfigHash(sampleConfig{A: 2, B: "x"})
	if err != nil {
		t.Fatalf("ConfigHash failed: %v", err)
	}
	if a == b {
		t.Fatalf("ConfigHash produced the same hash for different input: %q", a)
	}
}

func TestConfigHash_RejectsUnmarshalableInput(t *testing.T) {
	if _, err := ConfigHash(make(chan int)); err == nil {
		t.Fatal("expected an error for a value json.Marshal cannot handle")
	}
}

// TestGitCommit_SoftChecked calls GitCommit and asserts only its
// documented contract (a dirty flag is only meaningful when a non-empty
// commit was found; the call never panics), not any specific commit
// value -- which git commit this test binary was built under varies by
// environment and CI checkout depth, and is not this package's own
// concern to pin down.
func TestGitCommit_SoftChecked(t *testing.T) {
	commit, dirty := GitCommit()
	if commit == "" && dirty {
		t.Error("GitCommit: dirty=true with an empty commit is contradictory -- dirty should only be meaningful alongside a known commit")
	}
}
