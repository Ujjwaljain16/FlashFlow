package traffic

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"flashflow/internal/replay"
)

// FuzeLogEntry is one parsed access-log line. The PRD names this
// capability "Fuze log import" without ever defining "Fuze log"
// anywhere in the repository; per Stage 10's confirmed design decision,
// this is concretized as the NCSA/Apache "combined" access-log format
// -- the most standard, recognizable access-log shape, and a reasonable
// reading of an otherwise-undefined term.
type FuzeLogEntry struct {
	Timestamp time.Time
	Method    string
	Path      string
	Query     string
}

// combinedLogPattern matches the NCSA combined log format:
//
//	host ident authuser [timestamp] "METHOD request-target PROTOCOL" status bytes "referer" "user-agent"
//
// The trailing referer/user-agent pair is optional so the narrower
// "common log format" (no referer/agent) also parses -- this project
// only extracts timestamp and request-target, so accepting both formats
// costs nothing and avoids rejecting real-world log samples that happen
// to be common-format instead of combined.
var combinedLogPattern = regexp.MustCompile(
	`^\S+ \S+ \S+ \[([^\]]+)\] "(\S+) (\S+)(?: \S+)?"\s+\d{3}\s+\S+(?:\s+"[^"]*"\s+"[^"]*")?\s*$`,
)

const combinedLogTimeLayout = "02/Jan/2006:15:04:05 -0700"

// ImportCombinedLog parses r line by line as NCSA/Apache combined-format
// access log entries. Blank lines are skipped; a line that doesn't match
// the expected shape returns an error identifying which line, rather
// than silently dropping it -- a log import that silently discards
// unparseable lines could produce a traffic replay that looks
// plausible while missing an unknown fraction of the real arrivals.
func ImportCombinedLog(r io.Reader) ([]FuzeLogEntry, error) {
	var entries []FuzeLogEntry
	scanner := bufio.NewScanner(r)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		m := combinedLogPattern.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("traffic: line %d does not match combined log format: %q", lineNum, line)
		}
		ts, err := time.Parse(combinedLogTimeLayout, m[1])
		if err != nil {
			return nil, fmt.Errorf("traffic: line %d has an unparseable timestamp %q: %w", lineNum, m[1], err)
		}
		path, query, _ := strings.Cut(m[3], "?")
		entries = append(entries, FuzeLogEntry{Timestamp: ts, Method: m[2], Path: path, Query: query})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("traffic: reading log: %w", err)
	}
	return entries, nil
}

// ArrivalsFromLog converts imported log entries into replay.Arrivals,
// using the first entry's timestamp as time zero and scaling every
// subsequent entry's offset by compress (e.g. compress=0.01 replays a
// day of real traffic in about 14 virtual minutes, preserving the
// relative shape of the arrival pattern while fitting it into a Scenario
// short enough to actually run). compress must be positive; entries are
// not required to already be time-sorted, matching real log files that
// occasionally interleave near-simultaneous requests from concurrent
// connections.
func ArrivalsFromLog(entries []FuzeLogEntry, compress float64) []replay.Arrival {
	if len(entries) == 0 || compress <= 0 {
		return nil
	}
	base := entries[0].Timestamp
	arrivals := make([]replay.Arrival, len(entries))
	for i, e := range entries {
		delta := e.Timestamp.Sub(base)
		scaled := time.Duration(float64(delta) * compress)
		if scaled < 0 {
			scaled = 0 // an out-of-order entry earlier than the file's first line -- clamp rather than schedule "before time zero"
		}
		key := e.Path
		if e.Query != "" {
			key = key + "?" + e.Query
		}
		arrivals[i] = replay.Arrival{Key: key}
		arrivals[i].At = arrivals[i].At.Add(scaled)
	}
	return arrivals
}
