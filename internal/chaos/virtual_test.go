package chaos

import (
	"testing"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/replay"
)

func TestToFailureWindows_PairsUpCrashAndRecover(t *testing.T) {
	s := Schedule{
		{At: time.Second, Target: "edge-b", Action: Crash},
		{At: 2 * time.Second, Target: "edge-b", Action: Recover},
	}
	windows, err := s.ToFailureWindows()
	if err != nil {
		t.Fatalf("ToFailureWindows failed: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("got %d windows, want 1", len(windows))
	}
	want := replay.FailureWindow{
		Target: "edge-b",
		DownAt: clock.VirtualTime(time.Second.Nanoseconds()),
		UpAt:   clock.VirtualTime((2 * time.Second).Nanoseconds()),
	}
	if windows[0] != want {
		t.Errorf("got %+v, want %+v", windows[0], want)
	}
}

func TestToFailureWindows_HandlesOutOfOrderInput(t *testing.T) {
	// Recover listed BEFORE crash in slice order -- ToFailureWindows
	// must sort by At first, not trust parse order.
	s := Schedule{
		{At: 2 * time.Second, Target: "edge-b", Action: Recover},
		{At: time.Second, Target: "edge-b", Action: Crash},
	}
	windows, err := s.ToFailureWindows()
	if err != nil {
		t.Fatalf("ToFailureWindows failed: %v", err)
	}
	if len(windows) != 1 || windows[0].DownAt >= windows[0].UpAt {
		t.Fatalf("got %+v, want a single window with DownAt < UpAt", windows)
	}
}

func TestToFailureWindows_MultipleTargetsIndependent(t *testing.T) {
	s := Schedule{
		{At: time.Second, Target: "edge-a", Action: Crash},
		{At: time.Second, Target: "edge-b", Action: Crash},
		{At: 2 * time.Second, Target: "edge-a", Action: Recover},
		{At: 3 * time.Second, Target: "edge-b", Action: Recover},
	}
	windows, err := s.ToFailureWindows()
	if err != nil {
		t.Fatalf("ToFailureWindows failed: %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("got %d windows, want 2", len(windows))
	}
}

func TestToFailureWindows_RejectsLatencyAction(t *testing.T) {
	s := Schedule{{At: time.Second, Target: "edge-a", Action: Latency, Delay: 50 * time.Millisecond}}
	if _, err := s.ToFailureWindows(); err == nil {
		t.Fatal("expected an error: the virtual engine cannot express a latency action")
	}
}

func TestToFailureWindows_RejectsUnmatchedCrash(t *testing.T) {
	s := Schedule{{At: time.Second, Target: "edge-a", Action: Crash}}
	if _, err := s.ToFailureWindows(); err == nil {
		t.Fatal("expected an error for a crash with no matching recover")
	}
}

func TestToFailureWindows_RejectsUnmatchedRecover(t *testing.T) {
	s := Schedule{{At: time.Second, Target: "edge-a", Action: Recover}}
	if _, err := s.ToFailureWindows(); err == nil {
		t.Fatal("expected an error for a recover with no matching crash")
	}
}

func TestToFailureWindows_RejectsDoubleCrash(t *testing.T) {
	s := Schedule{
		{At: time.Second, Target: "edge-a", Action: Crash},
		{At: 2 * time.Second, Target: "edge-a", Action: Crash},
	}
	if _, err := s.ToFailureWindows(); err == nil {
		t.Fatal("expected an error for two crashes with no recover between them")
	}
}
