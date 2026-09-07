package dashboard

import "testing"

// canonical.go (the Control Room's own backend) had zero test coverage
// before this file -- an independent audit found it, correctly noting
// this is exactly the kind of gap that let a real bug (the explain
// narrative's own contradiction, fixed separately) ship undetected.
// These tests mirror dashboard_test.go's existing style for
// playground.go: real execution against the canonical scenario via the
// actual virtual engine, asserting against already-validated
// classifications (see docs/StageArtifacts/Stage17-DiagnosticTooling.md's
// own six-policy calibration table) rather than mocks.

func TestRunCanonicalReport_ReturnsAllSixPoliciesWithKnownClassifications(t *testing.T) {
	sr, err := RunCanonicalReport(1)
	if err != nil {
		t.Fatalf("RunCanonicalReport failed: %v", err)
	}
	if len(sr.Policies) != 6 {
		t.Fatalf("expected 6 policies, got %d", len(sr.Policies))
	}
	// Seed 17000 (RunCanonicalReport's own first/only seed at seedCount=1)
	// is deterministic and its classifications have been directly
	// verified live, repeatedly, in this project's own diagnostic
	// tooling and dashboard -- see Stage17-DiagnosticTooling.md.
	want := map[string]string{
		"round-robin":          "CHRONIC_COLLAPSE",
		"weighted-round-robin": "ACUTE_COLLAPSE",
		"least-connections":    "STABLE",
		"ewma":                 "ACUTE_COLLAPSE",
		// p2c-load is STABLE on the 3-seed majority vote (Stage
		// 17-DiagnosticTooling.md's own table), but ACUTE_COLLAPSE on
		// seed 17000 alone -- single-seed classification legitimately
		// differs from the multi-seed majority, which is exactly why
		// Replication exists as its own field.
		"p2c-load": "ACUTE_COLLAPSE",
		"adaptive": "ACUTE_COLLAPSE",
	}
	for _, pr := range sr.Policies {
		wantClass, ok := want[pr.Policy]
		if !ok {
			t.Errorf("unexpected policy %q in report", pr.Policy)
			continue
		}
		if string(pr.Classification) != wantClass {
			t.Errorf("policy %q classified %s, want %s", pr.Policy, pr.Classification, wantClass)
		}
	}
	if len(sr.Seeds) != 1 || sr.Seeds[0] != 17000 {
		t.Errorf("expected Seeds=[17000] for seedCount=1, got %v", sr.Seeds)
	}
}

func TestRunCanonicalStressMap_ReturnsNineCells(t *testing.T) {
	result, err := RunCanonicalStressMap("ewma", 17900)
	if err != nil {
		t.Fatalf("RunCanonicalStressMap failed: %v", err)
	}
	if len(result.Cells) != 9 {
		t.Fatalf("expected a 3x3 (heterogeneity x workload) grid = 9 cells, got %d", len(result.Cells))
	}
	if result.Policy != "ewma" {
		t.Errorf("Policy = %q, want ewma", result.Policy)
	}
	if result.Seed != 17900 {
		t.Errorf("Seed = %d, want 17900", result.Seed)
	}
	seen := map[string]bool{}
	for _, c := range result.Cells {
		seen[c.Heterogeneity+"|"+c.Workload] = true
	}
	if len(seen) != 9 {
		t.Errorf("expected 9 distinct heterogeneity/workload pairs, got %d", len(seen))
	}
}

func TestRunCanonicalStressMap_RejectsUnknownPolicy(t *testing.T) {
	if _, err := RunCanonicalStressMap("not-a-real-policy", 17900); err == nil {
		t.Fatal("expected an error for an unknown policy name")
	}
}

// TestCompareCanonical_DetectsRealDivergence confirms two genuinely
// different policies against the identical canonical scenario/seed are
// reported as diverging -- the Control Room's First Divergence view is
// only useful if this actually fires for policies that really do decide
// differently.
func TestCompareCanonical_DetectsRealDivergence(t *testing.T) {
	summary, err := CompareCanonical("round-robin", "ewma", 17000)
	if err != nil {
		t.Fatalf("CompareCanonical failed: %v", err)
	}
	if !summary.Diverged {
		t.Fatal("expected round-robin and ewma to diverge on the canonical scenario")
	}
	if summary.DivergenceIndex <= 0 {
		t.Fatalf("expected a positive divergence index, got %d", summary.DivergenceIndex)
	}
	if len(summary.BaselineRecords) == 0 || len(summary.CounterfactualRecords) == 0 {
		t.Error("expected non-empty selection records for both sides")
	}
	if summary.Seed != 17000 {
		t.Errorf("Seed = %d, want 17000", summary.Seed)
	}
}

func TestCompareCanonical_IdenticalPoliciesDoNotDiverge(t *testing.T) {
	summary, err := CompareCanonical("round-robin", "round-robin", 17000)
	if err != nil {
		t.Fatalf("CompareCanonical failed: %v", err)
	}
	if summary.Diverged {
		t.Fatal("expected the same policy compared against itself to never diverge")
	}
}

func TestCompareCanonical_RejectsUnknownPolicy(t *testing.T) {
	if _, err := CompareCanonical("not-a-real-policy", "ewma", 17000); err == nil {
		t.Fatal("expected an error for an unknown baseline policy")
	}
	if _, err := CompareCanonical("ewma", "not-a-real-policy", 17000); err == nil {
		t.Fatal("expected an error for an unknown counterfactual policy")
	}
}

func TestRunCanonicalTimeline_ReturnsSeriesForEveryTarget(t *testing.T) {
	view, err := RunCanonicalTimeline("ewma", 17000, 40)
	if err != nil {
		t.Fatalf("RunCanonicalTimeline failed: %v", err)
	}
	if view.Policy != "ewma" {
		t.Errorf("Policy = %q, want ewma", view.Policy)
	}
	if view.Seed != 17000 {
		t.Errorf("Seed = %d, want 17000", view.Seed)
	}
	if len(view.Traffic) != 40 {
		t.Errorf("expected 40 traffic buckets, got %d", len(view.Traffic))
	}
	// The canonical scenario always has 5 targets (report.CanonicalTargetCount).
	if len(view.TargetDepths) != 5 {
		t.Fatalf("expected depth series for 5 targets, got %d", len(view.TargetDepths))
	}
	for name, series := range view.TargetDepths {
		if len(series) != 40 {
			t.Errorf("target %q depth series has %d points, want 40", name, len(series))
		}
	}
	// ewma is a known ACUTE_COLLAPSE case on this exact seed (see
	// TestRunCanonicalReport_ReturnsAllSixPoliciesWithKnownClassifications) --
	// its own Metrics should reflect real congestion, not a degenerate
	// all-zero run.
	if !view.Metrics.CongestionFound {
		t.Error("expected CongestionFound=true for ewma on the canonical scenario")
	}
}

func TestRunCanonicalTimeline_RejectsUnknownPolicy(t *testing.T) {
	if _, err := RunCanonicalTimeline("not-a-real-policy", 17000, 40); err == nil {
		t.Fatal("expected an error for an unknown policy name")
	}
}

func TestRunCanonicalTimeline_DefaultsBucketsWhenNonPositive(t *testing.T) {
	view, err := RunCanonicalTimeline("round-robin", 17000, 0)
	if err != nil {
		t.Fatalf("RunCanonicalTimeline failed: %v", err)
	}
	if len(view.Traffic) != 60 {
		t.Errorf("expected the documented default of 60 buckets for buckets<=0, got %d", len(view.Traffic))
	}
}
