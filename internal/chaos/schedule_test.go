package chaos

import (
	"strings"
	"testing"
	"time"
)

const validSchedule = `
- at: 1s
  target: edge-b
  action: crash
- at: 2s
  target: edge-b
  action: recover
- at: 1.5s
  target: edge-a
  action: latency
  delay: 50ms
`

func TestParseYAML_ParsesValidSchedule(t *testing.T) {
	s, err := ParseYAML(strings.NewReader(validSchedule))
	if err != nil {
		t.Fatalf("ParseYAML failed: %v", err)
	}
	if len(s) != 3 {
		t.Fatalf("got %d events, want 3", len(s))
	}
	want := Event{At: time.Second, Target: "edge-b", Action: Crash}
	if s[0] != want {
		t.Errorf("event 0 = %+v, want %+v", s[0], want)
	}
	if s[2].Action != Latency || s[2].Delay != 50*time.Millisecond {
		t.Errorf("event 2 = %+v, want Latency with 50ms delay", s[2])
	}
}

func TestParseYAML_SkipsCommentsAndBlankLines(t *testing.T) {
	input := `
# a comment
- at: 1s
  target: edge-a
  action: crash

# another comment
- at: 2s
  target: edge-a
  action: recover
`
	s, err := ParseYAML(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseYAML failed: %v", err)
	}
	if len(s) != 2 {
		t.Fatalf("got %d events, want 2", len(s))
	}
}

func TestParseYAML_RejectsUnknownKey(t *testing.T) {
	input := "- at: 1s\n  taget: edge-a\n  action: crash\n"
	if _, err := ParseYAML(strings.NewReader(input)); err == nil {
		t.Fatal("expected an error for an unknown key (typo)")
	}
}

func TestParseYAML_RejectsDuplicateKey(t *testing.T) {
	input := "- at: 1s\n  at: 2s\n  target: edge-a\n  action: crash\n"
	if _, err := ParseYAML(strings.NewReader(input)); err == nil {
		t.Fatal("expected an error for a duplicate key within one event")
	}
}

func TestParseYAML_RejectsMissingRequiredKeys(t *testing.T) {
	cases := []string{
		"- target: edge-a\n  action: crash\n", // missing at
		"- at: 1s\n  action: crash\n",         // missing target
		"- at: 1s\n  target: edge-a\n",        // missing action
	}
	for _, c := range cases {
		if _, err := ParseYAML(strings.NewReader(c)); err == nil {
			t.Errorf("input %q: expected an error for a missing required key", c)
		}
	}
}

func TestParseYAML_RejectsUnknownAction(t *testing.T) {
	input := "- at: 1s\n  target: edge-a\n  action: explode\n"
	if _, err := ParseYAML(strings.NewReader(input)); err == nil {
		t.Fatal("expected an error for an unknown action")
	}
}

func TestParseYAML_LatencyRequiresPositiveDelay(t *testing.T) {
	cases := []string{
		"- at: 1s\n  target: edge-a\n  action: latency\n",              // no delay at all
		"- at: 1s\n  target: edge-a\n  action: latency\n  delay: 0s\n", // zero delay
	}
	for _, c := range cases {
		if _, err := ParseYAML(strings.NewReader(c)); err == nil {
			t.Errorf("input %q: expected an error for latency without a positive delay", c)
		}
	}
}

func TestParseYAML_RejectsDelayOnNonLatencyAction(t *testing.T) {
	input := "- at: 1s\n  target: edge-a\n  action: crash\n  delay: 50ms\n"
	if _, err := ParseYAML(strings.NewReader(input)); err == nil {
		t.Fatal("expected an error for a delay on a non-latency action")
	}
}

func TestParseYAML_RejectsFieldBeforeAnyEventMarker(t *testing.T) {
	input := "at: 1s\n- target: edge-a\n  action: crash\n"
	if _, err := ParseYAML(strings.NewReader(input)); err == nil {
		t.Fatal("expected an error for a field appearing before any \"- \" marker")
	}
}

func TestParseYAML_RejectsEmptySchedule(t *testing.T) {
	if _, err := ParseYAML(strings.NewReader("# just a comment\n")); err == nil {
		t.Fatal("expected an error for an empty schedule")
	}
}

func TestParseYAML_RejectsMalformedLine(t *testing.T) {
	input := "- this is not a key value pair\n"
	if _, err := ParseYAML(strings.NewReader(input)); err == nil {
		t.Fatal("expected an error for a line with no ':'")
	}
}
