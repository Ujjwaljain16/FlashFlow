// Package chaos hand-rolls a declarative chaos-engineering schedule
// parser (PRD §8.7, TRD §12), missing per the Stage 8 audit's F-10 --
// deliberately a flat, 4-key YAML subset with its own hand-rolled
// parser rather than adding a YAML dependency (this project's existing
// pattern: no dependency was ever earned by anything more complex than
// a flat list-of-maps schema, per this project's own earn-the-
// abstraction discipline). It compiles down to the SAME primitives
// both engines already use -- replay.FailureWindow for the virtual
// engine, EdgeServer.SetDown/SetArtificialDelay for the real one --
// rather than inventing a second, parallel chaos-injection mechanism.
package chaos

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"
)

// Action is one of the three chaos actions this schedule format
// understands.
type Action string

const (
	Crash   Action = "crash"
	Recover Action = "recover"
	Latency Action = "latency"
)

// Event is one scheduled chaos action: at time At, apply Action to
// Target. Delay is only meaningful for Latency.
type Event struct {
	At     time.Duration
	Target string
	Action Action
	Delay  time.Duration
}

// Schedule is an ordered (by parse order, not necessarily by At --
// ToFailureWindows/ToRealSchedule both handle out-of-order input)
// list of Events.
type Schedule []Event

// allowedKeys is this format's entire schema -- flat, 4 keys, no
// nesting. A key outside this set is a parse error, not a silently
// ignored field, so a typo in a schedule file (e.g. "taget" instead of
// "target") is caught immediately rather than producing a schedule
// that silently omits the intended event.
var allowedKeys = map[string]bool{"at": true, "target": true, "action": true, "delay": true}

// ParseYAML parses r as this package's flat chaos-schedule format: a
// YAML list of maps, one map per event, each map using only the keys
// at/target/action/delay. For example:
//
//   - at: 1s
//     target: edge-b
//     action: crash
//   - at: 2s
//     target: edge-b
//     action: recover
//   - at: 1.5s
//     target: edge-a
//     action: latency
//     delay: 50ms
//
// This is a deliberately restricted subset of YAML -- no nested
// structures, no multi-line strings, no anchors/aliases -- hand-parsed
// line by line rather than pulling in a general YAML library for a
// four-key flat schema, matching this project's earn-the-abstraction
// discipline. A line that doesn't fit this subset is a parse error
// identifying the offending line number, never a silently-skipped or
// partially-interpreted one.
func ParseYAML(r io.Reader) (Schedule, error) {
	scanner := bufio.NewScanner(r)
	var schedule Schedule
	var current map[string]string
	lineNum := 0

	flush := func() error {
		if current == nil {
			return nil
		}
		ev, err := eventFromFields(current)
		if err != nil {
			return err
		}
		schedule = append(schedule, ev)
		current = nil
		return nil
	}

	for scanner.Scan() {
		lineNum++
		trimmed := strings.TrimSpace(scanner.Text())
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if rest, ok := strings.CutPrefix(trimmed, "- "); ok {
			if err := flush(); err != nil {
				return nil, fmt.Errorf("chaos: line %d: %w", lineNum, err)
			}
			current = map[string]string{}
			if err := parseKV(rest, current); err != nil {
				return nil, fmt.Errorf("chaos: line %d: %w", lineNum, err)
			}
			continue
		}

		if current == nil {
			return nil, fmt.Errorf("chaos: line %d: field %q appears before any \"- \" event marker", lineNum, trimmed)
		}
		if err := parseKV(trimmed, current); err != nil {
			return nil, fmt.Errorf("chaos: line %d: %w", lineNum, err)
		}
	}
	if err := flush(); err != nil {
		return nil, fmt.Errorf("chaos: %w", err)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("chaos: reading schedule: %w", err)
	}
	if len(schedule) == 0 {
		return nil, fmt.Errorf("chaos: schedule is empty")
	}
	return schedule, nil
}

func parseKV(s string, into map[string]string) error {
	key, value, ok := strings.Cut(s, ":")
	if !ok {
		return fmt.Errorf("expected \"key: value\", got %q", s)
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" {
		return fmt.Errorf("empty key in %q", s)
	}
	if !allowedKeys[key] {
		return fmt.Errorf("unknown key %q -- only at/target/action/delay are recognized (flat 4-key schema)", key)
	}
	if _, exists := into[key]; exists {
		return fmt.Errorf("duplicate key %q within one event", key)
	}
	into[key] = value
	return nil
}

func eventFromFields(fields map[string]string) (Event, error) {
	atStr, ok := fields["at"]
	if !ok {
		return Event{}, fmt.Errorf("event missing required key \"at\"")
	}
	at, err := time.ParseDuration(atStr)
	if err != nil {
		return Event{}, fmt.Errorf("invalid \"at\" duration %q: %w", atStr, err)
	}

	target := fields["target"]
	if target == "" {
		return Event{}, fmt.Errorf("event missing required key \"target\"")
	}

	actionStr, ok := fields["action"]
	if !ok {
		return Event{}, fmt.Errorf("event missing required key \"action\"")
	}
	action := Action(actionStr)
	switch action {
	case Crash, Recover, Latency:
	default:
		return Event{}, fmt.Errorf("unknown action %q -- want one of crash, recover, latency", actionStr)
	}

	delayStr, hasDelay := fields["delay"]
	var delay time.Duration
	if hasDelay {
		delay, err = time.ParseDuration(delayStr)
		if err != nil {
			return Event{}, fmt.Errorf("invalid \"delay\" duration %q: %w", delayStr, err)
		}
	}
	if action == Latency && delay <= 0 {
		return Event{}, fmt.Errorf("action \"latency\" requires a positive \"delay\"")
	}
	if action != Latency && hasDelay {
		return Event{}, fmt.Errorf("\"delay\" is only meaningful for action \"latency\", got action %q", action)
	}

	return Event{At: at, Target: target, Action: action, Delay: delay}, nil
}
