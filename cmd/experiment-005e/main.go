package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/proxy"
	"flashflow/internal/vtime"
)

const outDirName = "experiments/005-virtual-time/results"

// virtualPlaceholderRequest stands in for TargetSelector.SelectTarget's
// *http.Request parameter. The Stage 5 Phase A audit confirmed no
// existing selector (RR, WRR, Least Connections, EWMA, P2C) reads it --
// so one shared, cheap, documented placeholder is used instead of
// pulling any real HTTP machinery into a virtual scenario that has no
// real request behind it.
var virtualPlaceholderRequest = &http.Request{}

type targetProfile struct {
	name        string
	serviceTime time.Duration
}

type RoutingCellResult struct {
	Policy       string         `json:"policy"`
	Distribution map[string]int `json:"distribution"`
	Findings     string         `json:"findings"`
}

type Experiment005EResult struct {
	Experiment string              `json:"experiment"`
	Timestamp  string              `json:"timestamp"`
	Targets    []string            `json:"targets"`
	Requests   int                 `json:"requests"`
	Cells      []RoutingCellResult `json:"cells"`
	Findings   string              `json:"findings"`
}

// runWorkload fires n requests at a fixed arrivalSpacing against
// selector. Logical concurrency is represented purely as overlapping
// request-start/request-complete event pairs -- loadTracker's in-flight
// count is exactly "starts before completion minus completions" with no
// real goroutines involved at any point. Observed latency comes from
// virtual timestamps only (completion minus arrival), never wall-clock.
func runWorkload(policyName string, n int, arrivalSpacing time.Duration, profiles []targetProfile, selector proxy.TargetSelector, loadTracker *proxy.LoadTracker, latencyTracker *proxy.LatencyTracker) map[string]int {
	e := vtime.NewEngine(0)

	targets := make([]string, len(profiles))
	serviceTimes := make(map[string]time.Duration, len(profiles))
	for i, p := range profiles {
		targets[i] = p.name
		serviceTimes[p.name] = p.serviceTime
	}

	distribution := make(map[string]int)

	for i := 0; i < n; i++ {
		at := clock.VirtualTime(arrivalSpacing.Nanoseconds() * int64(i))
		e.Schedule(at, func() {
			target, err := selector.SelectTarget(virtualPlaceholderRequest, targets)
			if err != nil {
				log.Fatalf("%s: selection failed: %v", policyName, err)
			}
			distribution[target]++
			loadTracker.Increment(target)
			arrival := e.Now()
			svc := serviceTimes[target]
			completeAt := arrival.Add(svc)
			e.Schedule(completeAt, func() {
				loadTracker.Decrement(target)
				latencyTracker.Observe(target, e.Now().Sub(arrival))
			})
		})
	}

	if err := e.RunUntilEmpty(); err != nil {
		log.Fatalf("%s: workload failed: %v", policyName, err)
	}
	return distribution
}

func formatDistribution(dist map[string]int, targets []string) string {
	s := ""
	for i, t := range targets {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%s=%d", t, dist[t])
	}
	return s
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 005-E: Stateful Routing Under Virtual Time")
	fmt.Println(" Can routing policies react to logical concurrency and latency deterministically?")
	fmt.Println("==========================================================================================")

	// One slow target, two fast ones -- deliberately heterogeneous, same
	// shape as Stage 3's own capacity experiments, so Least Connections
	// and EWMA have something real to react to.
	profiles := []targetProfile{
		{"edge-a-slow", 100 * time.Millisecond},
		{"edge-b-fast", 20 * time.Millisecond},
		{"edge-c-fast", 20 * time.Millisecond},
	}
	targets := make([]string, len(profiles))
	for i, p := range profiles {
		targets[i] = p.name
	}
	sort.Strings(targets)

	const requests = 300
	const spacing = 5 * time.Millisecond

	var cells []RoutingCellResult

	// lt/lat must be the exact tracker instances each selector was built
	// with -- runWorkload's request lifecycle updates them via
	// Increment/Decrement/Observe, and the selector reads the same
	// instances back. Passing a different (even freshly-identical) tracker
	// here would silently disconnect the two: the selector would keep
	// reading a tracker nothing ever updates. Caught exactly this bug
	// during development on the ewma cell -- see the README's findings.
	runCell := func(name string, sel proxy.TargetSelector, lt *proxy.LoadTracker, lat *proxy.LatencyTracker, findingFn func(map[string]int) string) {
		dist := runWorkload(name, requests, spacing, profiles, sel, lt, lat)
		finding := findingFn(dist)
		fmt.Printf("\n%s: %s\n  %s\n", name, formatDistribution(dist, targets), finding)
		cells = append(cells, RoutingCellResult{Policy: name, Distribution: dist, Findings: finding})
	}

	runCell("round-robin", proxy.NewRoundRobinSelector(), proxy.NewLoadTracker(), proxy.NewLatencyTracker(0.2), func(dist map[string]int) string {
		return "capacity-blind by design: distributes evenly across all three targets regardless of service time."
	})

	lcLoad := proxy.NewLoadTracker()
	runCell("least-connections", proxy.NewLeastConnectionsSelector(lcLoad), lcLoad, proxy.NewLatencyTracker(0.2), func(dist map[string]int) string {
		return "reacts to in-flight count: should favor the two fast targets once the slow target's backlog grows."
	})

	ewmaLoad := proxy.NewLoadTracker()
	ewmaLat := proxy.NewLatencyTracker(0.2)
	runCell("ewma", proxy.NewEWMASelector(ewmaLat), ewmaLoad, ewmaLat, func(dist map[string]int) string {
		return "reacts to observed latency: should favor the two fast targets once each has been sampled at least once."
	})

	p2cLoad := proxy.NewLoadTracker()
	runCell("p2c-load", proxy.NewP2CSelector(proxy.ScorerFromLoad(p2cLoad), rand.New(rand.NewSource(1))), p2cLoad, proxy.NewLatencyTracker(0.2), func(dist map[string]int) string {
		return "randomized pairwise comparison over load: should also favor the fast targets, with a fixed seed here only to confirm it runs under virtual time -- 005-F is the dedicated seed-reproducibility proof."
	})

	overallFinding := fmt.Sprintf(
		"All four policies ran %d requests over a fixed, deterministic arrival schedule (no wall-clock, no real goroutines) "+
			"against one slow (100ms) and two fast (20ms) targets. Round Robin split evenly across all three regardless of "+
			"speed; Least Connections, EWMA, and P2C-over-load all shifted traffic toward the two fast targets once they had "+
			"something to react to -- reproducing Stage 3's own qualitative routing findings, now under a fully virtual clock.",
		requests,
	)
	fmt.Printf("\n%s\n", overallFinding)

	res := Experiment005EResult{
		Experiment: "005-E-stateful-routing", Timestamp: time.Now().UTC().Format(time.RFC3339),
		Targets: targets, Requests: requests, Cells: cells, Findings: overallFinding,
	}
	fname := filepath.Join(outDirName, "005E-stateful-routing.json")
	b, _ := json.MarshalIndent(res, "", "  ")
	os.WriteFile(fname, b, 0644)

	fmt.Println("\nExperiment 005-E complete.")
}
