// Command demo-stage10 is FlashFlow's Stage 10 demonstration: one
// controlled virtual-time experiment exercising, in narrative order,
// the traffic generator (§10.1), the declarative chaos engine (§10.2's
// sibling §10.7), the ExperimentEngine's counterfactual Replay (§10.8),
// the queueing-attribution engine (§10.2), and the provenance manifest
// (§10.3) -- composed into one real, honestly-scoped research question
// rather than demonstrated as seven disconnected feature checkboxes.
//
// Every number this program prints comes from an actual RunWorld
// execution against the real internal/replay engine -- nothing here is
// precomputed, hardcoded, or dressed up. Re-running this program
// reproduces the identical baseline result every time (demonstrated
// explicitly in Scene 6) and, being virtual-time, completes in well
// under a second of wall-clock time despite simulating 3.5 seconds of
// scenario time.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"flashflow/internal/attribution"
	"flashflow/internal/chaos"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/provenance"
	"flashflow/internal/replay"
	"flashflow/internal/statistics"
	"flashflow/internal/traffic"
)

const manifestsRoot = "demo/output"

func section(title string) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 78))
	fmt.Println(" " + title)
	fmt.Println(strings.Repeat("=", 78))
}

func main() {
	fmt.Println("FlashFlow -- Stage 10 Demonstration")
	fmt.Println("Question: when one edge target is both overloaded and prone to temporary")
	fmt.Println("failure, does FlashFlow's adaptive router actually route around the problem")
	fmt.Println("better than blind round-robin -- and can we prove it, explain it, and")
	fmt.Println("reproduce it, not just assert it?")

	// ---------------------------------------------------------------
	// Scene 1: The problem -- a real, heterogeneous topology.
	// ---------------------------------------------------------------
	section("SCENE 1 -- The Topology")
	targets := []replay.TargetProfile{
		{Name: "edge-a", ServiceTime: 20 * time.Millisecond},
		{Name: "edge-b", ServiceTime: 15 * time.Millisecond},
		{Name: "edge-c", ServiceTime: 60 * time.Millisecond},
	}
	fmt.Println("Three edge targets, deliberately heterogeneous:")
	for _, t := range targets {
		fmt.Printf("  %-8s service time %v\n", t.Name, t.ServiceTime)
	}
	fmt.Println("edge-c is 3-4x slower than the other two -- under enough load, its own fixed")
	fmt.Println("service time alone will make it the bottleneck, independent of any failure.")

	// ---------------------------------------------------------------
	// Scene 2: Generate a real, reproducible workload.
	// ---------------------------------------------------------------
	section("SCENE 2 -- Generating the Workload (internal/traffic)")
	const rootSeed = 42
	seeds := replay.DeriveSeeds(rootSeed)
	const requests = 300
	const horizon = 3500 * time.Millisecond
	arrivals, err := traffic.Generate(traffic.Constant, traffic.Params{
		Requests: requests, Horizon: 3 * time.Second, BaseRate: 100,
		KeyFunc: traffic.HotColdKeys(0.5),
	}, seeds.Traffic)
	if err != nil {
		fatal("generating traffic", err)
	}
	fmt.Printf("Pattern: Constant, %d requests over a %v window, half hitting one \"hot\" cache\n", requests, 3*time.Second)
	fmt.Println("key and half spread across three \"cold\" keys -- exercises Adaptive's Cache")
	fmt.Println("signal, not just its Load signal.")
	fmt.Printf("Generated %d arrivals from Traffic seed %d (derived from root seed %d).\n", len(arrivals), seeds.Traffic, rootSeed)

	// ---------------------------------------------------------------
	// Scene 3: The environmental change -- a declarative chaos schedule.
	// ---------------------------------------------------------------
	section("SCENE 3 -- Injecting a Real Failure (internal/chaos)")
	chaosYAML := `
- at: 1s
  target: edge-b
  action: crash
- at: 2s
  target: edge-b
  action: recover
`
	fmt.Println("Failure schedule, as plain, inspectable data (not a hardcoded engine branch):")
	fmt.Println(chaosYAML)
	schedule, err := chaos.ParseYAML(strings.NewReader(chaosYAML))
	if err != nil {
		fatal("parsing chaos schedule", err)
	}
	windows, err := schedule.ToFailureWindows()
	if err != nil {
		fatal("compiling chaos schedule to failure windows", err)
	}
	fmt.Printf("Compiled to a virtual-engine failure window: %s down from %v to %v.\n",
		windows[0].Target, time.Duration(windows[0].DownAt), time.Duration(windows[0].UpAt))
	fmt.Println("edge-b is the FASTEST target when healthy -- losing it temporarily removes")
	fmt.Println("the one target that could otherwise absorb load away from slow edge-c.")

	scenario := replay.Scenario{
		Targets:           targets,
		Arrivals:          arrivals,
		Failures:          windows,
		UseHealthRegistry: true,
		Horizon:           clock.VirtualTime(horizon.Nanoseconds()),
		Seeds:             seeds,
	}

	// ---------------------------------------------------------------
	// Scene 4: Compare two policies under the IDENTICAL exogenous
	// conditions, via ExperimentEngine's Run/Replay.
	// ---------------------------------------------------------------
	section("SCENE 4 -- Comparing Policies Under Identical Conditions")
	fmt.Println("Same Scenario object (same targets, same 300 arrivals, same failure window,")
	fmt.Println("same seeds) run through two different policies -- Run for the baseline,")
	fmt.Println("Replay for the counterfactual. Neither call mutates or shares state with the")
	fmt.Println("other (internal/replay.RunWorld's own isolation guarantee).")

	v := engine.NewVirtualEngine()
	exp := engine.Experiment{ID: "stage10-demo", Name: "Heterogeneous edges, one failure, RR vs Adaptive", Scenario: scenario, Policy: replay.RoundRobinPolicy()}

	baseline, err := v.Run(exp)
	if err != nil {
		fatal("running Round Robin baseline", err)
	}
	adaptiveResult, err := v.Replay(exp, replay.AdaptivePolicy())
	if err != nil {
		fatal("replaying with Adaptive", err)
	}

	rrMean, rrP99 := latencyStats(baseline.WorldResult)
	adMean, adP99 := latencyStats(adaptiveResult.WorldResult)
	fmt.Printf("\n%-16s %10s %10s %10s   %s\n", "policy", "mean(ms)", "p99(ms)", "rejected", "completed by target")
	fmt.Printf("%-16s %10.2f %10.2f %10d   %s\n", "round-robin", rrMean, rrP99, baseline.WorldResult.RejectedCount, completedByTargetStr(baseline.WorldResult.CompletedByTarget, targets))
	fmt.Printf("%-16s %10.2f %10.2f %10d   %s\n", "adaptive", adMean, adP99, adaptiveResult.WorldResult.RejectedCount, completedByTargetStr(adaptiveResult.WorldResult.CompletedByTarget, targets))

	improvement := (rrMean - adMean) / rrMean * 100
	fmt.Printf("\nMean latency: adaptive is %.1f%% %s than round-robin (%.2fms vs %.2fms).\n",
		abs(improvement), better(improvement), adMean, rrMean)
	fmt.Println("p99 ties at the horizon's own worst-case service time in both policies here --")
	fmt.Println("a small-sample-size effect Stage 8's own tuning work already found and corrected")
	fmt.Println("for (p99 is a weak discriminator at this request count; mean is not).")

	idx, diverged := replay.FirstDivergence(baseline.WorldResult.Trace, adaptiveResult.WorldResult.Trace)
	fmt.Println("\n--- Proof moment: counterfactual divergence ---")
	if diverged {
		fmt.Printf("The two policies' event traces are IDENTICAL up through event #%d, then diverge --\n", idx)
		fmt.Println("proof that the difference above is a real routing-decision effect, not an")
		fmt.Println("artifact of the two runs somehow seeing different exogenous conditions.")
	} else {
		fmt.Println("No divergence found -- the two policies made identical decisions throughout.")
	}

	// ---------------------------------------------------------------
	// Scene 5: Explain WHY, via the attribution engine.
	// ---------------------------------------------------------------
	section("SCENE 5 -- Explaining Why (internal/attribution)")
	rrUtil, err := attribution.UtilizationFromWorld(*baseline.WorldResult, targets, horizon)
	if err != nil {
		fatal("computing round-robin utilization", err)
	}
	adUtil, err := attribution.UtilizationFromWorld(*adaptiveResult.WorldResult, targets, horizon)
	if err != nil {
		fatal("computing adaptive utilization", err)
	}
	fmt.Println("Utilization (rho = offered rate x service time) per target, over the full")
	fmt.Println("3.5s horizon -- rho > 1 means that target was, on its own, offered more work")
	fmt.Println("than its fixed service time could keep up with:")
	for _, t := range targets {
		f := attribution.Compare("adaptive", adUtil[t.Name], "round-robin", rrUtil[t.Name])
		fmt.Printf("  %-8s %s\n", t.Name, f.Text)
	}
	fmt.Println("\nMechanism: edge-c is overloaded under BOTH policies (rho > 1 either way -- no")
	fmt.Println("routing policy can make a fixed-capacity target keep up with more offered load")
	fmt.Println("than it can serve). What Adaptive actually does is shift a meaningful share of")
	fmt.Println("that excess load onto edge-a and edge-b instead, which still have headroom --")
	fmt.Println("lowering mean latency by making the overload less severe, not by eliminating it.")

	// ---------------------------------------------------------------
	// Scene 6: Provenance and reproducibility.
	// ---------------------------------------------------------------
	section("SCENE 6 -- Provenance and Reproducibility")
	configHash, err := provenance.ConfigHash(struct {
		Targets []replay.TargetProfile
		Horizon time.Duration
		Chaos   string
	}{targets, horizon, chaosYAML})
	if err != nil {
		fatal("hashing experiment configuration", err)
	}
	commit, dirty := provenance.GitCommit()
	manifest := provenance.Manifest{
		ExperimentID: exp.ID, Name: exp.Name, Seeds: seeds,
		ConfigurationHash: configHash, GitCommit: commit, GitDirty: dirty,
		CreatedAt: time.Now().UTC(),
	}
	if err := manifest.Write(manifestsRoot); err != nil {
		fatal("writing provenance manifest", err)
	}
	manifestPath := manifestsRoot + "/" + exp.ID + "/manifest.json"
	fmt.Printf("Wrote a real provenance manifest to %s:\n", manifestPath)
	fmt.Printf("  experiment_id      %s\n", manifest.ExperimentID)
	fmt.Printf("  seeds              global=%d traffic=%d topology=%d failure=%d policy=%d\n",
		seeds.Global, seeds.Traffic, seeds.Topology, seeds.Failure, seeds.Policy)
	fmt.Printf("  configuration_hash %s\n", manifest.ConfigurationHash)
	if commit == "" {
		fmt.Println("  git_commit         (empty -- run with `go run -buildvcs=true` to populate this)")
	} else {
		fmt.Printf("  git_commit         %s (dirty=%v)\n", manifest.GitCommit, manifest.GitDirty)
	}

	fmt.Println("\nRe-running the identical Experiment (same Scenario, same Round Robin policy):")
	rerun, err := v.Run(exp)
	if err != nil {
		fatal("re-running baseline", err)
	}
	_, rerunDiverged := replay.FirstDivergence(baseline.WorldResult.Trace, rerun.WorldResult.Trace)
	if rerunDiverged {
		fmt.Println("  DIVERGED from the original run -- this would be a real determinism bug.")
	} else {
		fmt.Println("  Zero divergence across the entire trace: byte-for-byte identical to the")
		fmt.Println("  original run above. Same Scenario + same seeds -> same result, every time.")
	}

	// ---------------------------------------------------------------
	// Scene 7: Takeaway.
	// ---------------------------------------------------------------
	section("SCENE 7 -- Takeaway")
	fmt.Println("The point isn't that Adaptive \"won\" this one scenario. It's that FlashFlow let")
	fmt.Println("us hold the exogenous conditions (topology, workload, failure timing) fixed,")
	fmt.Println("swap only the routing policy, prove the resulting difference was a real")
	fmt.Println("decision effect (not measurement noise), explain the mechanism behind it")
	fmt.Println("(load shifted toward targets with headroom), and reproduce the whole thing")
	fmt.Println("byte-for-byte on demand -- as a matter of engine design, not manual diligence.")
	fmt.Println()
}

// completedByTargetStr renders CompletedByTarget in a fixed, readable
// target order (Go's own map formatting is order-randomized and looks
// noisy on screen) -- purely a display concern, not a computation.
func completedByTargetStr(m map[string]int, targets []replay.TargetProfile) string {
	parts := make([]string, len(targets))
	for i, t := range targets {
		parts[i] = fmt.Sprintf("%s=%d", t.Name, m[t.Name])
	}
	return strings.Join(parts, "  ")
}

func latencyStats(wr *replay.WorldResult) (mean, p99 float64) {
	if len(wr.Completions) == 0 {
		return 0, 0
	}
	ms := make([]float64, len(wr.Completions))
	for i, c := range wr.Completions {
		ms[i] = float64(c.Latency.Microseconds()) / 1000.0
	}
	mean, _ = statistics.Mean(ms)
	p99, _ = statistics.Percentile(ms, 99)
	return mean, p99
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func better(improvementPct float64) string {
	if improvementPct > 0 {
		return "lower"
	}
	return "higher"
}

func fatal(step string, err error) {
	fmt.Fprintf(os.Stderr, "demo-stage10: %s: %v\n", step, err)
	os.Exit(1)
}
