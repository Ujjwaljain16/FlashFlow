package vtime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

func TestTrace_RecordAssignsMonotonicSequence(t *testing.T) {
	var tr Trace
	tr.record(10, "a", "e1", nil)
	tr.record(10, "b", "e1", nil)
	tr.record(20, "c", "e2", nil)

	events := tr.Events()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	for i, want := range []uint64{0, 1, 2} {
		if events[i].Seq != want {
			t.Fatalf("event %d: expected seq %d, got %d", i, want, events[i].Seq)
		}
	}
	if events[0].Time != 10 || events[1].Time != 10 || events[2].Time != 20 {
		t.Fatalf("unexpected timestamps: %+v", events)
	}
}

func TestTrace_WriteJSONL_ProducesOneLinePerEvent(t *testing.T) {
	var tr Trace
	tr.record(10, "request_arrived", "r1", map[string]any{"target": "edge-a"})
	tr.record(20, "request_completed", "r1", nil)

	var buf bytes.Buffer
	if err := tr.WriteJSONL(&buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	scanner := bufio.NewScanner(&buf)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d: %v", len(lines), lines)
	}

	var first TraceEvent
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("line 1 did not parse as JSON: %v", err)
	}
	if first.Time != 10 || first.Type != "request_arrived" || first.Entity != "r1" {
		t.Fatalf("unexpected first event: %+v", first)
	}
	if first.Fields["target"] != "edge-a" {
		t.Fatalf("expected fields.target=edge-a, got %+v", first.Fields)
	}
}

func TestEngine_RecordStampsCurrentVirtualTime(t *testing.T) {
	e := NewEngine(0)
	e.Schedule(10, func() { e.Record("event_a", "", nil) })
	e.Schedule(20, func() { e.Record("event_b", "", nil) })

	if err := e.RunUntilEmpty(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := e.Trace().Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 recorded events, got %d", len(events))
	}
	if events[0].Time != 10 || events[0].Type != "event_a" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Time != 20 || events[1].Type != "event_b" {
		t.Fatalf("unexpected second event: %+v", events[1])
	}
}

func TestEngine_RecordOrdersMultipleEntriesWithinOneCallback(t *testing.T) {
	e := NewEngine(0)
	e.Schedule(10, func() {
		e.Record("first", "", nil)
		e.Record("second", "", nil)
	})

	if err := e.RunUntilEmpty(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := e.Trace().Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Time != 10 || events[1].Time != 10 {
		t.Fatalf("expected both events at t=10, got %+v", events)
	}
	if events[0].Type != "first" || events[1].Type != "second" {
		t.Fatalf("expected recording order first,second within the same timestamp, got %+v", events)
	}
	if events[0].Seq >= events[1].Seq {
		t.Fatalf("expected strictly increasing Seq, got %d then %d", events[0].Seq, events[1].Seq)
	}
}
