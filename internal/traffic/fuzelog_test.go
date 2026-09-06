package traffic

import (
	"strings"
	"testing"
	"time"
)

// combinedLogFixture is a small, representative NCSA combined-format
// access log: one common-format line (no referer/agent) and two full
// combined-format lines, including one with a query string -- exercises
// both format variants ImportCombinedLog accepts.
const combinedLogFixture = `127.0.0.1 - frank [10/Oct/2000:13:55:36 -0700] "GET /apache_pb.gif HTTP/1.0" 200 2326
127.0.0.1 - frank [10/Oct/2000:13:55:37 -0700] "GET /apache_pb.gif HTTP/1.0" 200 2326 "http://www.example.com/start.html" "Mozilla/4.08 [en] (Win98; I ;Nav)"
10.0.0.5 - - [10/Oct/2000:13:55:39 -0700] "GET /search?q=flashflow HTTP/1.1" 200 512 "-" "curl/7.68.0"
`

func TestImportCombinedLog_ParsesFixture(t *testing.T) {
	entries, err := ImportCombinedLog(strings.NewReader(combinedLogFixture))
	if err != nil {
		t.Fatalf("ImportCombinedLog failed: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}

	want0 := FuzeLogEntry{
		Timestamp: time.Date(2000, time.October, 10, 13, 55, 36, 0, time.FixedZone("", -7*3600)),
		Method:    "GET", Path: "/apache_pb.gif",
	}
	if !entries[0].Timestamp.Equal(want0.Timestamp) {
		t.Errorf("entry 0 timestamp: got %v, want %v", entries[0].Timestamp, want0.Timestamp)
	}
	if entries[0].Method != want0.Method || entries[0].Path != want0.Path {
		t.Errorf("entry 0: got %+v, want method/path %+v", entries[0], want0)
	}

	if entries[2].Path != "/search" || entries[2].Query != "q=flashflow" {
		t.Errorf("entry 2: got path=%q query=%q, want path=/search query=q=flashflow", entries[2].Path, entries[2].Query)
	}

	if !entries[0].Timestamp.Before(entries[1].Timestamp) || !entries[1].Timestamp.Before(entries[2].Timestamp) {
		t.Error("expected fixture entries to be time-ordered")
	}
}

func TestImportCombinedLog_RejectsMalformedLine(t *testing.T) {
	bad := "this is not a log line\n"
	if _, err := ImportCombinedLog(strings.NewReader(bad)); err == nil {
		t.Fatal("expected an error for a malformed log line")
	}
}

func TestImportCombinedLog_SkipsBlankLines(t *testing.T) {
	withBlanks := "\n\n" + combinedLogFixture + "\n\n"
	entries, err := ImportCombinedLog(strings.NewReader(withBlanks))
	if err != nil {
		t.Fatalf("ImportCombinedLog failed: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 (blank lines should be skipped)", len(entries))
	}
}

func TestArrivalsFromLog_ScalesRelativeToFirstEntry(t *testing.T) {
	entries, err := ImportCombinedLog(strings.NewReader(combinedLogFixture))
	if err != nil {
		t.Fatalf("ImportCombinedLog failed: %v", err)
	}
	// Real deltas: entry1 is +1s from entry0, entry2 is +3s from entry0.
	// compress=0.1 should scale those to +100ms and +300ms.
	arrivals := ArrivalsFromLog(entries, 0.1)
	if len(arrivals) != 3 {
		t.Fatalf("got %d arrivals, want 3", len(arrivals))
	}
	if arrivals[0].At != 0 {
		t.Errorf("arrival 0: got At=%v, want 0 (first entry is time zero)", arrivals[0].At)
	}
	want1 := 100 * time.Millisecond
	got1 := time.Duration(arrivals[1].At.Nanoseconds())
	if got1 != want1 {
		t.Errorf("arrival 1: got At=%v, want %v", got1, want1)
	}
	want2 := 300 * time.Millisecond
	got2 := time.Duration(arrivals[2].At.Nanoseconds())
	if got2 != want2 {
		t.Errorf("arrival 2: got At=%v, want %v", got2, want2)
	}
	if arrivals[2].Key != "/search?q=flashflow" {
		t.Errorf("arrival 2: got key %q, want \"/search?q=flashflow\"", arrivals[2].Key)
	}
}

func TestArrivalsFromLog_EmptyInput(t *testing.T) {
	if got := ArrivalsFromLog(nil, 1.0); got != nil {
		t.Errorf("expected nil for empty entries, got %v", got)
	}
	if got := ArrivalsFromLog([]FuzeLogEntry{{}}, 0); got != nil {
		t.Errorf("expected nil for non-positive compress, got %v", got)
	}
}
