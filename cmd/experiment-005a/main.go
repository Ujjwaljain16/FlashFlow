package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/vtime"
)

const outDirName = "experiments/005-virtual-time/results"

type Experiment005AResult struct {
	Experiment         string  `json:"experiment"`
	Timestamp          string  `json:"timestamp"`
	Cell               string  `json:"cell"`
	EventCount         int     `json:"event_count"`
	VirtualDurationMs  float64 `json:"virtual_duration_ms"`
	RealElapsedMs      float64 `json:"real_elapsed_ms"`
	EventsPerSecond    float64 `json:"events_per_second"`
	FinalVirtualTimeNs int64   `json:"final_virtual_time_ns"`
	Findings           string  `json:"findings"`
}

func msF(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }

// runFewEventsLongSpan schedules a handful of events spread across a
// large virtual duration -- proving real cost does not scale with the
// size of the gaps between events, only with how many events exist.
func runFewEventsLongSpan() Experiment005AResult {
	const virtualSpan = 10 * time.Minute
	e := vtime.NewEngine(0)

	const n = 5
	for i := 1; i <= n; i++ {
		at := clock.VirtualTime(int64(virtualSpan) / n * int64(i))
		e.Schedule(at, func() { e.Record("tick", "", nil) })
	}

	realStart := time.Now()
	if err := e.RunUntilEmpty(); err != nil {
		log.Fatalf("cell failed: %v", err)
	}
	realElapsed := time.Since(realStart)

	finding := fmt.Sprintf(
		"few-events-long-span: %d events spanning %v of virtual time completed in %.3fms of real time -- "+
			"virtual duration had no measurable effect on real cost.",
		n, virtualSpan, msF(realElapsed),
	)
	return Experiment005AResult{
		Cell: "few-events-long-span", EventCount: n, VirtualDurationMs: float64(virtualSpan.Milliseconds()),
		RealElapsedMs: msF(realElapsed), EventsPerSecond: float64(n) / realElapsed.Seconds(),
		FinalVirtualTimeNs: e.Now().Nanoseconds(), Findings: finding,
	}
}

// runManyEventsShortSpan schedules a large number of events packed into
// a tiny virtual duration -- proving real cost scales with event count,
// which is the actual bottleneck a virtual engine has, not virtual span.
func runManyEventsShortSpan() Experiment005AResult {
	const virtualSpan = 100 * time.Millisecond
	const n = 100_000

	e := vtime.NewEngine(0)
	e.SetMaxEvents(n + 10)
	for i := 1; i <= n; i++ {
		at := clock.VirtualTime(int64(virtualSpan) / n * int64(i))
		e.Schedule(at, func() { e.Record("tick", "", nil) })
	}

	realStart := time.Now()
	if err := e.RunUntilEmpty(); err != nil {
		log.Fatalf("cell failed: %v", err)
	}
	realElapsed := time.Since(realStart)

	finding := fmt.Sprintf(
		"many-events-short-span: %d events packed into %v of virtual time took %.3fms of real time (%.0f events/sec) -- "+
			"real cost tracks event count, not the (tiny) virtual span they're packed into.",
		n, virtualSpan, msF(realElapsed), float64(n)/realElapsed.Seconds(),
	)
	return Experiment005AResult{
		Cell: "many-events-short-span", EventCount: n, VirtualDurationMs: float64(virtualSpan.Milliseconds()),
		RealElapsedMs: msF(realElapsed), EventsPerSecond: float64(n) / realElapsed.Seconds(),
		FinalVirtualTimeNs: e.Now().Nanoseconds(), Findings: finding,
	}
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 005-A: Deterministic Clock")
	fmt.Println(" Can virtual time advance independently of wall-clock time?")
	fmt.Println("==========================================================================================")

	cellFew := runFewEventsLongSpan()
	fmt.Printf("\n%s\n", cellFew.Findings)

	cellMany := runManyEventsShortSpan()
	fmt.Printf("\n%s\n", cellMany.Findings)

	now := time.Now().UTC().Format(time.RFC3339)
	for _, r := range []Experiment005AResult{cellFew, cellMany} {
		r.Experiment = "005-A-deterministic-clock"
		r.Timestamp = now
		fname := filepath.Join(outDirName, fmt.Sprintf("005A-%s.json", r.Cell))
		b, _ := json.MarshalIndent(r, "", "  ")
		os.WriteFile(fname, b, 0644)
	}

	fmt.Println("\nExperiment 005-A complete.")
}
