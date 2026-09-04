package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"flashflow/internal/vtime"
)

const outDirName = "experiments/005-virtual-time/results"

type Experiment005BResult struct {
	Experiment      string             `json:"experiment"`
	Timestamp       string             `json:"timestamp"`
	Runs            int                `json:"runs"`
	EventsPerRun    int                `json:"events_per_run"`
	AllIdentical    bool               `json:"all_identical"`
	FirstDivergence string             `json:"first_divergence,omitempty"`
	BaselineTrace   []vtime.TraceEvent `json:"baseline_trace"`
	Findings        string             `json:"findings"`
}

// buildScenario deliberately schedules several batches of same-timestamp
// events from different, interleaved call sites -- simulating a cache
// subsystem, a traffic generator, and a health prober all reacting to
// the same virtual instant, plus one event whose own callback schedules
// two more same-timestamp events downstream. This exercises both of
// H2's predictions: same-timestamp order at the top level, and
// same-timestamp order among events scheduled dynamically during a run.
func buildScenario() *vtime.Engine {
	e := vtime.NewEngine(0)

	e.Schedule(50, func() {
		e.Record("burst_start", "", nil)
		e.Schedule(300, func() { e.Record("burst_a", "", nil) })
		e.Schedule(300, func() { e.Record("burst_b", "", nil) })
	})

	// t=100: cache subsystem, traffic generator, and health prober all
	// fire at the identical virtual timestamp.
	e.Schedule(100, func() { e.Record("cache_expired", "key-a", nil) })
	e.Schedule(100, func() { e.Record("request_arrived", "r1", nil) })
	e.Schedule(100, func() { e.Record("probe_fired", "edge-a", nil) })

	// t=200: the same three subsystems, scheduled in a different relative
	// order than the t=100 batch, so the test isn't accidentally only
	// checking "the same fixed pattern happens to look ordered."
	e.Schedule(200, func() { e.Record("probe_fired", "edge-b", nil) })
	e.Schedule(200, func() { e.Record("request_arrived", "r2", nil) })
	e.Schedule(200, func() { e.Record("cache_expired", "key-b", nil) })

	return e
}

func runOnce() []vtime.TraceEvent {
	e := buildScenario()
	if err := e.RunUntilEmpty(); err != nil {
		log.Fatalf("scenario failed: %v", err)
	}
	return e.Trace().Events()
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 005-B: Deterministic Event Ordering")
	fmt.Println(" Are equal-time events processed in a deterministic order, across repeated runs?")
	fmt.Println("==========================================================================================")

	const runs = 50
	baseline := runOnce()

	fmt.Println("\nBaseline trace (run 0):")
	for _, ev := range baseline {
		fmt.Printf("  t=%-6d seq=%-2d %-16s %s\n", ev.Time, ev.Seq, ev.Type, ev.Entity)
	}

	allIdentical := true
	firstDivergence := ""
	for i := 1; i < runs; i++ {
		trace := runOnce()
		if !reflect.DeepEqual(trace, baseline) {
			allIdentical = false
			firstDivergence = fmt.Sprintf("run %d produced a different trace than run 0", i)
			break
		}
	}

	var finding string
	if allIdentical {
		finding = fmt.Sprintf(
			"All %d runs of the identical scenario produced byte-for-byte identical traces (%d events each), "+
				"including three separate same-timestamp batches scheduled in different relative orders and one "+
				"dynamically-scheduled same-timestamp pair. Same-timestamp ordering is deterministic, not accidental.",
			runs, len(baseline),
		)
	} else {
		finding = fmt.Sprintf("DETERMINISM VIOLATED: %s", firstDivergence)
	}
	fmt.Printf("\n%s\n", finding)

	res := Experiment005BResult{
		Experiment: "005-B-deterministic-event-ordering", Timestamp: time.Now().UTC().Format(time.RFC3339),
		Runs: runs, EventsPerRun: len(baseline), AllIdentical: allIdentical, FirstDivergence: firstDivergence,
		BaselineTrace: baseline, Findings: finding,
	}
	fname := filepath.Join(outDirName, "005B-event-ordering.json")
	b, _ := json.MarshalIndent(res, "", "  ")
	os.WriteFile(fname, b, 0644)

	if !allIdentical {
		log.Fatal("experiment 005-B failed: determinism was violated -- see findings above")
	}

	fmt.Println("\nExperiment 005-B complete.")
}
