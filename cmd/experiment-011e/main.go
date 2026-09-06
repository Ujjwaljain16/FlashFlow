// Command experiment-011e is Stage 11's Program E: mechanistic
// attribution. Reuses experiment-011a's flagship severe/constant/none
// scenario (edge-a 15ms/edge-b 30ms/edge-c 60ms) and, for EWMA and
// Adaptive, computes an INDEPENDENTLY-measured L (time-averaged number
// of in-flight requests at a target, via direct event-timeline
// integration over dispatch/completion timestamps -- not simply
// Lambda*W, which would be circular) alongside Lambda (throughput) and W
// (mean sojourn time), then checks Little's Law (L ~= Lambda*W) via
// internal/attribution.CheckLittlesLaw.
//
// The point is not to "prove" queueing theory -- it's to state precisely
// what this attribution DOES and DOES NOT support, per Stage11's own
// instruction not to claim queueing behavior the virtual model doesn't
// simulate. The virtual engine has no queueing/contention model (Section
// 7/14), so L should equal Lambda*W almost exactly here BY CONSTRUCTION
// (no queueing means no wait time, so W IS the fixed ServiceTime, and L
// is just however many fixed-duration services happen to overlap) --
// confirming the arithmetic/bookkeeping is internally consistent, not
// independently validating anything about real queueing dynamics.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"flashflow/internal/attribution"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/traffic"
)

const outDirName = "experiments/011-research-validation/results"

type TargetAttribution struct {
	Policy      string                   `json:"policy"`
	Target      string                   `json:"target"`
	Lambda      float64                  `json:"lambda_req_per_sec"`
	W_ms        float64                  `json:"w_ms"`
	L_measured  float64                  `json:"l_measured_direct_integration"`
	LittlesLaw  attribution.ErrorMetrics `json:"littles_law_check"`
	Utilization float64                  `json:"utilization_rho"`
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}
	fmt.Println("===============================================================")
	fmt.Println(" Experiment 011-E: Mechanistic Attribution")
	fmt.Println(" scenario: severe heterogeneity, constant workload, no failure")
	fmt.Println(" (identical configuration to 011-A's flagship finding)")
	fmt.Println("===============================================================")

	targets := []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 15 * time.Millisecond},
		{Name: "edge-b", ServiceTime: 30 * time.Millisecond},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond},
	}
	horizon := 4 * time.Second
	seeds := replay.DeriveSeeds(1001) // matches 011-A's severe/constant/none root seed
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: 300, Horizon: horizon, BaseRate: 75, KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		log.Fatalf("generating traffic: %v", err)
	}
	scenario := replay.Scenario{
		Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(horizon.Nanoseconds()), Seeds: seeds,
	}
	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "011e-attribution", Scenario: scenario}

	var all []TargetAttribution
	for i, ps := range []struct {
		name string
		spec replay.PolicySpec
	}{{"ewma", replay.EWMAPolicy()}, {"adaptive", replay.AdaptivePolicy()}} {
		exp.Policy = ps.spec
		var result engine.RunResult
		var err error
		if i == 0 {
			result, err = v.Run(exp)
		} else {
			result, err = v.Replay(exp, ps.spec)
		}
		if err != nil {
			log.Fatalf("%s: %v", ps.name, err)
		}
		wr := result.WorldResult
		util, err := attribution.UtilizationFromWorld(*wr, targets, horizon)
		if err != nil {
			log.Fatalf("%s: utilization: %v", ps.name, err)
		}

		fmt.Printf("\n-- %s --\n", ps.name)
		for _, t := range targets {
			lambda, w, lMeasured := measureLambdaWL(wr, t.Name, horizon)
			sample := attribution.Sample{L: lMeasured, Lambda: lambda, W: w / 1000.0} // W must be seconds
			check, err := attribution.CheckLittlesLaw(sample)
			if err != nil {
				log.Fatalf("%s/%s: CheckLittlesLaw: %v", ps.name, t.Name, err)
			}
			ta := TargetAttribution{
				Policy: ps.name, Target: t.Name, Lambda: lambda, W_ms: w,
				L_measured: lMeasured, LittlesLaw: check, Utilization: util[t.Name],
			}
			all = append(all, ta)
			fmt.Printf("  %-8s lambda=%6.2freq/s  W=%6.2fms  L(measured)=%.3f  L(predicted=lambda*W)=%.3f  relErr=%.4f  rho=%.3f\n",
				t.Name, lambda, w, lMeasured, check.Predicted, check.RelError, util[t.Name])
		}
	}

	fmt.Println("\n--- What this attribution supports and does not ---")
	fmt.Println("SUPPORTS: internal consistency of L/Lambda/W bookkeeping (relErr near 0 confirms no")
	fmt.Println("          arithmetic/measurement inconsistency in how these three are derived).")
	fmt.Println("DOES NOT SUPPORT: any claim about real queueing/wait-time behavior -- W here is just the")
	fmt.Println("          fixed ServiceTime (no wait component exists in this model at all), so Little's")
	fmt.Println("          Law holding near-exactly is expected BY CONSTRUCTION, not an independent")
	fmt.Println("          empirical finding about queueing dynamics (see Section 7/14's no-queueing-model finding).")

	out := struct {
		Experiment string              `json:"experiment"`
		Timestamp  string              `json:"timestamp"`
		Results    []TargetAttribution `json:"results"`
	}{Experiment: "011-E-mechanistic-attribution", Timestamp: time.Now().UTC().Format(time.RFC3339), Results: all}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "011E-mechanistic-attribution.json"), b, 0644)
	fmt.Println("\nExperiment 011-E complete.")
}

// measureLambdaWL computes, for one target:
//   - Lambda: completed requests to this target / horizon (req/s)
//   - W: mean latency (ms) of completions at this target
//   - L: an INDEPENDENTLY measured time-average number of in-flight
//     requests at this target, via exact event-timeline integration
//     (a +1 step at each dispatch, a -1 step at each completion, area
//     under the resulting step function divided by horizon) -- not
//     derived from Lambda*W, so comparing it against Lambda*W via
//     CheckLittlesLaw is a genuine (if, per this model, expected-to-pass)
//     independent check.
func measureLambdaWL(wr *replay.WorldResult, target string, horizon time.Duration) (lambda, wMs, l float64) {
	// Dispatch times for this target, in order (fixed service time per
	// target means completions preserve dispatch order -- no request can
	// ever overtake an earlier one to the same target).
	var dispatchMs []float64
	for _, r := range wr.Records {
		if r.Target == target {
			dispatchMs = append(dispatchMs, r.VirtualTimeMs)
		}
	}
	var completions []replay.CompletionRecord
	for _, c := range wr.Completions {
		if c.Target == target {
			completions = append(completions, c)
		}
	}
	sort.Slice(completions, func(i, j int) bool { return completions[i].VirtualTimeMs < completions[j].VirtualTimeMs })
	sort.Float64s(dispatchMs)

	n := len(completions)
	if n == 0 {
		return 0, 0, 0
	}
	lambda = float64(n) / horizon.Seconds()

	var sumLatencyMs float64
	type event struct {
		tMs   float64
		delta int
	}
	events := make([]event, 0, n*2)
	for i := 0; i < n && i < len(dispatchMs); i++ {
		sumLatencyMs += float64(completions[i].Latency.Microseconds()) / 1000.0
		events = append(events, event{dispatchMs[i], +1})
		events = append(events, event{completions[i].VirtualTimeMs, -1})
	}
	wMs = sumLatencyMs / float64(n)

	sort.Slice(events, func(i, j int) bool { return events[i].tMs < events[j].tMs })
	var area float64
	occupancy := 0
	lastT := 0.0
	for _, e := range events {
		area += float64(occupancy) * (e.tMs - lastT)
		occupancy += e.delta
		lastT = e.tMs
	}
	area += float64(occupancy) * (horizon.Seconds()*1000 - lastT)
	l = area / (horizon.Seconds() * 1000)
	return lambda, wMs, l
}
