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

	"flashflow/internal/report"
)

// outDirName is flashflow's own results directory -- deliberately NOT
// 016-final-synthesis/results (Stage 16's own artifact directory,
// where this used to write), which mixed flashflow-report-*.json
// alongside 016-flagship-results.json's own, incompatible schema.
// Pointing "explain" at the wrong file there produced a genuinely
// confusing failure ("unknown policy \"ewma\"... choose one of []"),
// found in an independent audit. flashflow is itself Stage 17's own
// deliverable (docs/StageArtifacts/Stage17-DiagnosticTooling.md), so
// it gets Stage 17's own results directory, matching every other
// stage's naming convention instead of borrowing Stage 16's.
const outDirName = "experiments/017-diagnostic-tooling/results"

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

func runReport(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	policy := fs.String("policy", "", "policy to print the full report for (default: the worst-classified policy)")
	seeds := fs.Int("seeds", 3, "number of independent seeds to run (matches the flagship's own convention)")
	jsonOut := fs.Bool("json", false, "print the selected policy's own report as JSON to stdout, instead of the text render (the full scenario JSON is still written to disk either way)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `flashflow report -- classify every policy on the canonical scenario, print one policy's own failure report

Usage:
  flashflow report [--policy NAME] [--seeds N] [--json]

Runs Stage 15/16's own canonical scenario (5 heterogeneous targets, Capacity=1,
FlashCrowd workload) for every one of the six routing policies, classifies
each (STABLE / ACUTE_COLLAPSE / CHRONIC_COLLAPSE / RECOVERY_LIMITED), writes
the full scenario report as JSON, and prints one policy's own report -- by
default, the worst-classified one.`)
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("flashflow report: creating results dir: %v", err)
	}

	sr, err := report.RunCanonicalScenario(*seeds)
	if err != nil {
		log.Fatalf("flashflow report: %v", err)
	}
	selected := *policy
	if selected == "" {
		selected = pickDefaultPolicy(sr)
	}
	pr, ok := sr.FindPolicy(selected)
	if !ok {
		log.Fatalf("flashflow report: unknown policy %q; choose one of %v", selected, report.PolicyNames())
	}

	if *jsonOut {
		b, err := json.MarshalIndent(pr, "", "  ")
		if err != nil {
			log.Fatalf("flashflow report: marshaling policy report: %v", err)
		}
		fmt.Println(string(b))
	} else {
		fmt.Println(sr.RenderText(selected))
	}

	outPath := filepath.Join(outDirName, fmt.Sprintf("flashflow-report-%d.json", time.Now().UTC().Unix()))
	b, err := json.MarshalIndent(sr, "", "  ")
	if err != nil {
		log.Fatalf("flashflow report: marshaling report: %v", err)
	}
	if err := os.WriteFile(outPath, b, 0644); err != nil {
		log.Fatalf("flashflow report: writing %s: %v", outPath, err)
	}
	if !*jsonOut {
		fmt.Printf("\nFull scenario report (all %d policies, %d seeds) written to %s\n", len(sr.Policies), *seeds, outPath)
		fmt.Printf("Explain it: go run ./cmd/flashflow explain %s --policy %s\n", outPath, selected)
	}
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
		if a == "--json" || a == "-json" || a == "-h" || a == "--help" || a == "-help" {
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
	jsonOut := fs.Bool("json", false, "print the selected policy's own report as JSON to stdout, instead of the causal-narrative text render")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `flashflow explain -- read a scenario report back and explain one policy's own diagnosed mechanism

Usage:
  flashflow explain <scenario-report.json> [--policy NAME] [--json]

Reads the JSON a prior "flashflow report" run wrote, and prints a numbered
causal narrative (traffic concentrated -> capacity crossed -> work committed
-> queue grew -> diverted or not -> drained or not -> classified) for one
policy, plus a counterfactual comparison against every other policy in that
same run. --policy works before OR after the file path.`)
		fs.PrintDefaults()
	}
	path, flagArgs := splitExplainArgs(args)
	fs.Parse(flagArgs)
	if path == "" {
		fs.Usage()
		os.Exit(2)
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
	if len(sr.Policies) == 0 {
		// A far more likely explanation than "this report genuinely has
		// zero policies" (BuildScenarioReport never produces that): %s is
		// valid JSON but not a flashflow ScenarioReport at all -- e.g. a
		// different experiment's own result file. json.Unmarshal doesn't
		// error on a structurally different JSON object; it silently
		// leaves ScenarioReport's fields at their zero values, which used
		// to surface only as "unknown policy \"ewma\" in <file>; choose
		// one of []" -- true, but useless for diagnosing WHY. Found
		// confusing in an independent audit.
		log.Fatalf("flashflow explain: %s has no policies -- it doesn't look like a scenario report `flashflow report` wrote (wrong file?)", path)
	}
	pr, ok := sr.FindPolicy(selected)
	if !ok {
		names := make([]string, 0, len(sr.Policies))
		for _, p := range sr.Policies {
			names = append(names, p.Policy)
		}
		log.Fatalf("flashflow explain: unknown policy %q in %s; choose one of %v", selected, path, names)
	}

	if *jsonOut {
		b, err := json.MarshalIndent(pr, "", "  ")
		if err != nil {
			log.Fatalf("flashflow explain: marshaling policy report: %v", err)
		}
		fmt.Println(string(b))
		return
	}
	fmt.Println(sr.RenderExplanation(selected))
}

func runStressMap(args []string) {
	fs := flag.NewFlagSet("stress-map", flag.ExitOnError)
	policy := fs.String("policy", "", "policy to run the stress map for (required)")
	seed := fs.Int64("seed", 17900, "root seed for the grid's own cells")
	jsonOut := fs.Bool("json", false, "print the grid as JSON to stdout, instead of the text table")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `flashflow stress-map -- classify one policy across a compact grid of conditions

Usage:
  flashflow stress-map --policy NAME [--seed N] [--json]

Runs one policy across a 3x3 grid (3 topology heterogeneities x 3 workload
shapes, one seed per cell) and classifies each cell. This is a small,
EXPLORATORY analysis -- a new run, not a reproduction of any specific
Stage 13-16 experiment; its numbers should not be cited as a Stage 13-16
finding. See docs/StageArtifacts/Stage17-DiagnosticTooling.md.`)
		fs.PrintDefaults()
	}
	fs.Parse(args)
	if *policy == "" {
		fs.Usage()
		os.Exit(2)
	}

	cells, err := report.RunStressMap(*policy, *seed)
	if err != nil {
		log.Fatalf("flashflow stress-map: %v", err)
	}

	if *jsonOut {
		b, err := json.MarshalIndent(cells, "", "  ")
		if err != nil {
			log.Fatalf("flashflow stress-map: marshaling grid: %v", err)
		}
		fmt.Println(string(b))
		return
	}
	fmt.Println(report.RenderStressMap(*policy, cells))
}

func usage() {
	fmt.Fprintln(os.Stderr, `FlashFlow -- routing failure analysis laboratory

Runs controlled routing experiments, classifies how a policy behaved
(STABLE / ACUTE_COLLAPSE / CHRONIC_COLLAPSE / RECOVERY_LIMITED), and explains
the mechanism behind the result -- built on top of Stages 11-16's completed
research, not a new research stage.

Commands:
  report       classify every policy on the canonical scenario, print one policy's own failure report
  explain      read a report back and explain one policy's own diagnosed mechanism
  stress-map   classify one policy across a compact grid of conditions (exploratory)

Run "flashflow <command> --help" for a command's own usage and flags.
All commands operate on Stage 15/16's own canonical scenario (5 heterogeneous
targets, Capacity=1, FlashCrowd workload) unless noted otherwise. See
docs/StageArtifacts/Stage17-DiagnosticTooling.md.`)
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
