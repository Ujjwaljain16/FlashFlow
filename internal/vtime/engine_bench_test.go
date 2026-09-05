package vtime

import (
	"testing"

	"flashflow/internal/clock"
)

// BenchmarkEngine_EventThroughput measures the virtual-time engine's
// own per-event overhead (pop from the queue, advance the clock,
// execute the callback) in isolation from any domain logic -- the
// "events/sec" figure master context rule 51 asks the virtual engine
// to report. Events are pre-scheduled during setup (untimed) so the
// timed region measures only RunUntilEmpty's own loop, not scheduling
// cost, which every FlashFlow experiment pays once per event and would
// otherwise be conflated with the engine's steady-state throughput.
func BenchmarkEngine_EventThroughput(b *testing.B) {
	e := NewEngine(0)
	e.SetMaxEvents(uint64(b.N) + 1)
	for i := 0; i < b.N; i++ {
		if _, err := e.Schedule(clock.VirtualTime(int64(i)), func() {}); err != nil {
			b.Fatalf("scheduling event %d: %v", i, err)
		}
	}

	b.ResetTimer()
	if err := e.RunUntilEmpty(); err != nil {
		b.Fatalf("RunUntilEmpty: %v", err)
	}
	b.StopTimer()

	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "events/sec")
}

// BenchmarkEngine_ScheduleAndRun measures the more realistic combined
// cost every experiment actually pays: scheduling an event AND
// executing it, each event itself scheduling one follow-up event (the
// arrival-then-completion pattern every RunWorld call uses) -- a
// closer proxy for real per-request overhead than raw event dispatch
// alone.
func BenchmarkEngine_ScheduleAndRun(b *testing.B) {
	e := NewEngine(0)
	e.SetMaxEvents(uint64(2*b.N) + 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		at := clock.VirtualTime(int64(i) * 2)
		if _, err := e.Schedule(at, func() {
			// Each "arrival" schedules one "completion" -- the same
			// two-event-per-request shape RunWorld uses.
			if _, err := e.Schedule(at+1, func() {}); err != nil {
				b.Fatalf("scheduling completion: %v", err)
			}
		}); err != nil {
			b.Fatalf("scheduling arrival %d: %v", i, err)
		}
	}
	if err := e.RunUntilEmpty(); err != nil {
		b.Fatalf("RunUntilEmpty: %v", err)
	}
	b.StopTimer()

	b.ReportMetric(float64(2*b.N)/b.Elapsed().Seconds(), "events/sec")
}
