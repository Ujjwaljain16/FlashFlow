package vtime

import (
	"bufio"
	"encoding/json"
	"io"
	"os"

	"flashflow/internal/clock"
)

// TraceEvent is one entry in a deterministic execution trace: enough to
// answer "did two runs actually execute the same experiment," not a
// general-purpose telemetry record. Time and Seq together give every
// entry a strict total order — Seq breaks ties between multiple entries
// recorded at the identical virtual time (e.g. a single event callback
// that records "request_arrived" then "route_selected" both at the same
// timestamp), the same reason EventQueue itself needs a tie-breaker.
type TraceEvent struct {
	Time   clock.VirtualTime `json:"t"`
	Seq    uint64            `json:"seq"`
	Type   string            `json:"type"`
	Entity string            `json:"entity,omitempty"`
	Fields map[string]any    `json:"fields,omitempty"`
}

// Trace is an ordered, append-only record of TraceEvents, held entirely
// in memory with no capacity bound. A known, accepted limit at this
// project's actual scale (experiments here run hundreds to ~100K events —
// a few MB at most): a hypothetical much-longer-running or much-higher-
// arrival-rate experiment could grow this unboundedly. Not a live issue
// for anything this project currently runs; worth revisiting only if that
// scale assumption changes.
type Trace struct {
	events []TraceEvent
	seq    uint64
}

// record appends a new entry stamped with t and the trace's own
// monotonic sequence counter. Unexported: only Engine.Record calls this,
// so every entry is guaranteed to carry a real virtual timestamp from
// the engine that produced it rather than one a caller made up.
func (tr *Trace) record(t clock.VirtualTime, typ, entity string, fields map[string]any) {
	tr.events = append(tr.events, TraceEvent{
		Time: t, Seq: tr.seq, Type: typ, Entity: entity, Fields: fields,
	})
	tr.seq++
}

// Events returns the recorded trace. The returned slice is owned by the
// caller to read, not to mutate — Trace does not defensively copy it.
func (tr *Trace) Events() []TraceEvent {
	return tr.events
}

// Len returns the number of recorded events.
func (tr *Trace) Len() int {
	return len(tr.events)
}

// WriteJSONL writes the trace as newline-delimited JSON to w, one
// TraceEvent per line, matching this project's existing experiment
// result conventions (plain JSON files under experiments/.../results).
// JSONL specifically because a trace is a sequence of independent
// records appended over the course of a run, not one document — exactly
// what JSONL is for, and there's no evidence yet that this project needs
// anything heavier (e.g. Parquet) than that.
func (tr *Trace) WriteJSONL(w io.Writer) error {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	for _, e := range tr.events {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// WriteJSONLFile is a convenience wrapper creating (or truncating) path
// and writing the trace to it.
func (tr *Trace) WriteJSONLFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return tr.WriteJSONL(f)
}
