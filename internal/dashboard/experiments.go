package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ExperimentsRoot is the one directory the dashboard ever reads
// artifacts from -- per rule 34, the dashboard is a read-only
// projection of experiment artifacts, never a second data store. All
// path handling below is anchored to this root and validated against
// path traversal, since these functions are reachable from untrusted
// HTTP request parameters.
const ExperimentsRoot = "experiments"

// safeName matches a single path segment this package will accept from
// an HTTP request: letters, digits, dot, dash, underscore, excluding
// "/" and "\". This alone does NOT exclude "..": "." is an allowed
// character with unbounded repetition, so the literal string ".."
// fully matches the class -- a real, adversarially-confirmed gap this
// project's own Stage 8 docs incorrectly claimed was closed. isSafeName
// below adds the explicit "." / ".." rejection this regex cannot
// express on its own.
var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// isSafeName is the actual guard every path-deriving function in this
// file must use -- safeName's character class plus an explicit reject
// of "." and ".." (self/parent-directory references indistinguishable
// from a legitimate dotted name by the character class alone).
func isSafeName(name string) bool {
	return safeName.MatchString(name) && name != "." && name != ".."
}

// resolveUnderRoot joins root with the given path segments and verifies
// -- by resolving to an absolute path and checking the prefix, not by
// trusting the segments' own validation -- that the result still falls
// under root. This is defense-in-depth on top of isSafeName: even if a
// future caller ever passed an unvalidated segment, this check is what
// actually stops the escape, rather than relying solely on upstream
// input validation never having a gap.
func resolveUnderRoot(root string, segments ...string) (string, error) {
	joined := filepath.Join(append([]string{root}, segments...)...)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("dashboard: resolving root: %w", err)
	}
	absJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("dashboard: resolving path: %w", err)
	}
	if absJoined != absRoot && !strings.HasPrefix(absJoined, absRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("dashboard: resolved path %q escapes root %q", absJoined, absRoot)
	}
	return joined, nil
}

// ExperimentGroup is one experiments/<stage>/ directory.
type ExperimentGroup struct {
	Name            string `json:"name"`
	ResultFileCount int    `json:"result_file_count"`
}

// ListGroups lists every experiments/<stage>/ directory that has a
// results/ subdirectory, with how many result files it holds.
func ListGroups() ([]ExperimentGroup, error) {
	entries, err := os.ReadDir(ExperimentsRoot)
	if err != nil {
		return nil, fmt.Errorf("dashboard: reading %s: %w", ExperimentsRoot, err)
	}
	var groups []ExperimentGroup
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		resultsDir := filepath.Join(ExperimentsRoot, e.Name(), "results")
		files, err := os.ReadDir(resultsDir)
		if err != nil {
			continue // no results/ subdirectory -- not an experiment group
		}
		count := 0
		for _, f := range files {
			if !f.IsDir() && filepath.Ext(f.Name()) == ".json" {
				count++
			}
		}
		groups = append(groups, ExperimentGroup{Name: e.Name(), ResultFileCount: count})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups, nil
}

// ListResultFiles lists the JSON result files inside one experiment
// group's results/ directory. group must pass safeName -- reject
// anything else before it ever reaches the filesystem.
func ListResultFiles(group string) ([]string, error) {
	if !isSafeName(group) {
		return nil, fmt.Errorf("dashboard: invalid group name %q", group)
	}
	resultsDir, err := resolveUnderRoot(ExperimentsRoot, group, "results")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		return nil, fmt.Errorf("dashboard: reading %s: %w", resultsDir, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

// maxResultFileBytes bounds how much of a single result file this reads
// into memory per request — generous for any experiment JSON this project
// actually produces (the largest today is well under 1MB), but a real
// bound rather than none at all for a handler reachable over HTTP.
const maxResultFileBytes = 64 << 20 // 64MiB

// ReadResultFile reads and parses one result file's JSON content.
// group and file must both pass safeName.
func ReadResultFile(group, file string) (map[string]any, error) {
	if !isSafeName(group) {
		return nil, fmt.Errorf("dashboard: invalid group name %q", group)
	}
	if !isSafeName(file) || filepath.Ext(file) != ".json" {
		return nil, fmt.Errorf("dashboard: invalid file name %q", file)
	}
	path, err := resolveUnderRoot(ExperimentsRoot, group, "results", file)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("dashboard: reading %s: %w", path, err)
	}
	defer f.Close()

	if info, err := f.Stat(); err == nil && info.Size() > maxResultFileBytes {
		return nil, fmt.Errorf("dashboard: %s exceeds the %d byte read limit (%d bytes)", path, maxResultFileBytes, info.Size())
	}
	data, err := io.ReadAll(io.LimitReader(f, maxResultFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("dashboard: reading %s: %w", path, err)
	}
	if len(data) > maxResultFileBytes {
		return nil, fmt.Errorf("dashboard: %s exceeds the %d byte read limit", path, maxResultFileBytes)
	}
	var content map[string]any
	if err := json.Unmarshal(data, &content); err != nil {
		return nil, fmt.Errorf("dashboard: parsing %s: %w", path, err)
	}
	return content, nil
}
