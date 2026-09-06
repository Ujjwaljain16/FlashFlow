// Command flashflow is the diagnostic-tooling CLI built on top of
// Stages 11-16's completed research: "report" generates a Failure
// Mechanism Report for the canonical scenario, "explain" reads one
// back and produces a causal narrative with counterfactual comparisons,
// and "stress-map" classifies one policy across a compact grid of
// heterogeneity/workload conditions. See internal/report for the
// shared classifier and docs/StageArtifacts/Stage17-DiagnosticTooling.md
// for the decision tree these commands render.
//
// No unified CLI existed in this project before this command; every
// other capability is its own cmd/experiment-NNNx binary. A dispatcher
// is introduced here specifically because these three subcommands
// share one data model and because "flashflow explain <file>" reads
// far better as a product surface than a fourth oddly-named separate
// binary would.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"flashflow/internal/backlog"
	"flashflow/internal/clock"
	"flashflow/internal/engine"
	"flashflow/internal/replay"
	"flashflow/internal/report"
)

const outDirName = "experiments/016-final-synthesis/results"

// congestionConfig is the same convention every Stage 14-16 canonical-
// scenario experiment already used (ratio threshold 1.0, a 20-decision
// trailing window, share threshold scaled to 1.5x the topology's own
// fair share -- see cmd/experiment-016-flagship's own comment for why
// this generalizes, not replaces, the fixed 0.5 Stage 14/15 used at
// N=3).
func congestionConfig(targetCount int) backlog.CongestionConfig {
	return backlog.CongestionConfig{RatioThreshold: 1.0, DiversionWindow: 20, DiversionShareThreshold: 1.5 / float64(targetCount)}
}

// worstFirst orders classifications from most to least concerning, used
// to pick a sensible default policy when the caller doesn't name one.
var worstFirst = []report.Classification{report.ChronicCollapse, report.AcuteCollapse, report.RecoveryLimited, report.Stable}

func pickDefaultPolicy(sr report.ScenarioReport) string {
	for _, want := range worstFirst {
		for _, p := range sr.Policies {
			if p.Classification == want {
				return p.Policy
			}
		}
	}
	if len(sr.Policies) > 0 {
		return sr.Policies[0].Policy
	}
	return ""
}

func buildCanonicalReport(seedCount int) report.ScenarioReport {
	targets := report.CanonicalTargets()
	capacity := report.CanonicalCapacity
	horizonMs := float64(report.CanonicalHorizon.Milliseconds())
	cfg := congestionConfig(len(targets))

	perPolicy := map[string][]*replay.WorldResult{}
	for i := 0; i < seedCount; i++ {
		seed := int64(17000 + i)
		arrivals, seeds := report.CanonicalArrivals(seed, 0.3)
		scenario := replay.Scenario{Targets: targets, Arrivals: arrivals, Horizon: clock.VirtualTime(report.CanonicalHorizon.Nanoseconds()), Seeds: seeds}
		v := engine.NewVirtualEngine()
		for j, name := range report.PolicyNames() {
			spec, err := report.PolicyByName(name)
			if err != nil {
				log.Fatalf("flashflow report: %v", err)
			}
			exp := engine.Experiment{ID: fmt.Sprintf("flashflow-report-seed%d-%s", seed, name), Scenario: scenario, Policy: spec}
			var result engine.RunResult
			var err2 error
			if j == 0 {
				result, err2 = v.Run(exp)
			} else {
				result, err2 = v.Replay(exp, spec)
			}
			if err2 != nil {
				log.Fatalf("flashflow report: seed %d/%s: %v", seed, name, err2)
			}
			perPolicy[name] = append(perPolicy[name], result.WorldResult)
		}
	}
	return report.BuildScenarioReport(report.CanonicalScenarioLabel, targets, capacity, horizonMs, cfg, perPolicy, report.PolicyNames())
}

func runReport(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	policy := fs.String("policy", "", "policy to print the full report for (default: the worst-classified policy)")
	seeds := fs.Int("seeds", 3, "number of independent seeds to run (matches the flagship's own convention)")
	fs.Parse(args)

	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("flashflow report: creating results dir: %v", err)
	}

	sr := buildCanonicalReport(*seeds)
	selected := *policy
	if selected == "" {
		selected = pickDefaultPolicy(sr)
	}
	if _, ok := sr.FindPolicy(selected); !ok {
		log.Fatalf("flashflow report: unknown policy %q; choose one of %v", selected, report.PolicyNames())
	}

	fmt.Println(sr.RenderText(selected))

	outPath := filepath.Join(outDirName, fmt.Sprintf("flashflow-report-%d.json", time.Now().UTC().Unix()))
	b, err := json.MarshalIndent(sr, "", "  ")
	if err != nil {
		log.Fatalf("flashflow report: marshaling report: %v", err)
	}
	if err := os.WriteFile(outPath, b, 0644); err != nil {
		log.Fatalf("flashflow report: writing %s: %v", outPath, err)
	}
	fmt.Printf("\nFull scenario report (all %d policies, %d seeds) written to %s\n", len(sr.Policies), *seeds, outPath)
	fmt.Printf("Explain it: go run ./cmd/flashflow explain %s --policy %s\n", outPath, selected)
}

// splitExplainArgs pulls the positional file path out of args wherever
// it appears, since Go's flag package only recognizes flags BEFORE the
// first positional argument -- a real bug found while testing this
// command by hand: "explain <file> --policy X" silently ignored
// --policy entirely (flag.Parse stopped at <file> and treated
// "--policy"/"X" as two more positional args) even though that's the
// exact ordering the command's own usage text originally implied was
// fine. This recognizes the one flag this subcommand has by name so
// both orderings work.
func splitExplainArgs(args []string) (path string, flagArgs []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--policy" || a == "-policy" {
			flagArgs = append(flagArgs, a)
			if i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
			continue
		}
		if strings.HasPrefix(a, "--policy=") || strings.HasPrefix(a, "-policy=") {
			flagArgs = append(flagArgs, a)
			continue
		}
		if path == "" {
			path = a
		}
	}
	return path, flagArgs
}

func runExplain(args []string) {
	fs := flag.NewFlagSet("explain", flag.ExitOnError)
	policy := fs.String("policy", "", "policy to explain (default: the worst-classified policy)")
	path, flagArgs := splitExplainArgs(args)
	fs.Parse(flagArgs)
	if path == "" {
		log.Fatal("flashflow explain: usage: flashflow explain <scenario-report.json> [--policy NAME]")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("flashflow explain: reading %s: %v", path, err)
	}
	var sr report.ScenarioReport
	if err := json.Unmarshal(data, &sr); err != nil {
		log.Fatalf("flashflow explain: parsing %s: %v", path, err)
	}

	selected := *policy
	if selected == "" {
		selected = pickDefaultPolicy(sr)
	}
	if _, ok := sr.FindPolicy(selected); !ok {
		names := make([]string, 0, len(sr.Policies))
		for _, p := range sr.Policies {
			names = append(names, p.Policy)
		}
		log.Fatalf("flashflow explain: unknown policy %q in %s; choose one of %v", selected, path, names)
	}
	fmt.Println(sr.RenderExplanation(selected))
}

func runStressMap(args []string) {
	fs := flag.NewFlagSet("stress-map", flag.ExitOnError)
	policy := fs.String("policy", "", "policy to run the stress map for (required)")
	seed := fs.Int64("seed", 17900, "root seed for the grid's own cells")
	fs.Parse(args)
	if *policy == "" {
		log.Fatalf("flashflow stress-map: --policy is required; choose one of %v", report.PolicyNames())
	}

	cells, err := report.RunStressMap(*policy, *seed)
	if err != nil {
		log.Fatalf("flashflow stress-map: %v", err)
	}
	fmt.Println(report.RenderStressMap(*policy, cells))
}

func usage() {
	fmt.Fprintln(os.Stderr, `flashflow -- FlashFlow diagnostic tooling

Usage:
  flashflow report [--policy NAME] [--seeds N]
  flashflow explain <scenario-report.json> [--policy NAME]
  flashflow stress-map --policy NAME [--seed N]

All three subcommands operate on Stage 15/16's own canonical scenario
(5 heterogeneous targets, Capacity=1, FlashCrowd workload) unless noted
otherwise. See docs/StageArtifacts/Stage17-DiagnosticTooling.md.`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "report":
		runReport(os.Args[2:])
	case "explain":
		runExplain(os.Args[2:])
	case "stress-map":
		runStressMap(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "flashflow: unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}
