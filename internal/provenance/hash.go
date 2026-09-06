package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime/debug"
)

// ConfigHash produces a short, stable identifier for v by JSON-
// marshaling it and SHA-256-hashing the result, truncated to 16 hex
// characters -- the same "marshal, hash, truncate" pattern this
// project already uses for config/scenario-set hashes (see
// internal/tuning/space.go's Hash and scenario.go's ScenarioSetHash),
// applied here as a generic, any-typed helper so a Manifest can record
// a stable fingerprint of whatever configuration produced it (an
// AdaptiveConfig, a ScenarioSpace, or any other JSON-marshalable value)
// without this package needing to know its shape.
func ConfigHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("provenance: marshaling config for hashing: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16], nil
}

// GitCommit reads the git revision and dirty-worktree flag Go's own
// toolchain embeds into the binary at build time (via
// runtime/debug.ReadBuildInfo, populated automatically for a binary
// built from within a VCS checkout since Go 1.18) -- deliberately not
// shelling out to `git` via os/exec, matching this project's existing
// zero-os/exec discipline (the Stage 8 audit's F-24 findings register
// confirmed zero os/exec usage anywhere in this codebase; this function
// preserves that). Returns ("", false) if build info isn't available at
// all (e.g. `go run`, in some CI/sandboxed environments, or a binary
// built with -buildvcs=false) -- a Manifest with an empty GitCommit is
// an honest "unknown," not treated as an error, since provenance
// recording should never fail an experiment run just because build-info
// stamping wasn't available.
func GitCommit() (commit string, dirty bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return commit, dirty
}
