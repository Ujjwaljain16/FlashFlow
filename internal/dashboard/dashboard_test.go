package dashboard

import (
	"os"
	"path/filepath"
	"testing"

	"flashflow/internal/replay"
)

// TestMain chdirs to the repo root before running this package's
// tests. ExperimentsRoot and every path derived from it are relative
// paths that assume CWD=repo root -- true for cmd/dashboard's real
// invocation (every cmd/experiment-* binary in this project makes the
// identical assumption for its own "experiments/.../results/" output
// paths), but NOT true for `go test`, which sets CWD to the package's
// own directory. Without this, every test touching the filesystem
// would fail with a "file not found" that has nothing to do with the
// behavior under test.
func TestMain(m *testing.M) {
	if err := os.Chdir("../.."); err != nil {
		panic("dashboard_test: could not chdir to repo root: " + err.Error())
	}
	os.Exit(m.Run())
}

func TestPlaygroundScenario_IsExecutable(t *testing.T) {
	sc := PlaygroundScenario()
	if _, err := replay.RunWorld(sc, replay.RoundRobinPolicy()); err != nil {
		t.Fatalf("PlaygroundScenario failed to execute: %v", err)
	}
}

func TestPlaygroundScenario_IsDeterministic(t *testing.T) {
	a, err := replay.RunWorld(PlaygroundScenario(), replay.AdaptivePolicy())
	if err != nil {
		t.Fatalf("run 1 failed: %v", err)
	}
	b, err := replay.RunWorld(PlaygroundScenario(), replay.AdaptivePolicy())
	if err != nil {
		t.Fatalf("run 2 failed: %v", err)
	}
	if idx, diverged := replay.FirstDivergence(a.Trace, b.Trace); diverged {
		t.Fatalf("two runs of the identical PlaygroundScenario diverged at event %d", idx)
	}
}

func TestPolicyByName_ResolvesEveryListedName(t *testing.T) {
	for _, name := range PolicyNames() {
		spec, err := PolicyByName(name)
		if err != nil {
			t.Errorf("PolicyByName(%q) failed: %v", name, err)
		}
		if spec.New == nil {
			t.Errorf("PolicyByName(%q) returned a spec with a nil constructor", name)
		}
	}
}

func TestPolicyByName_RejectsUnknownName(t *testing.T) {
	if _, err := PolicyByName("not-a-real-policy"); err == nil {
		t.Fatal("expected an error for an unknown policy name")
	}
}

func TestRunPlayground_ReturnsASensibleSummary(t *testing.T) {
	summary, err := RunPlayground("adaptive")
	if err != nil {
		t.Fatalf("RunPlayground failed: %v", err)
	}
	if summary.TotalRequests != 300 {
		t.Fatalf("expected 300 total requests, got %d", summary.TotalRequests)
	}
	if len(summary.Trace) == 0 {
		t.Fatal("expected a non-empty trace")
	}
	if summary.MeanLatencyMs <= 0 {
		t.Fatalf("expected a positive mean latency, got %v", summary.MeanLatencyMs)
	}
}

func TestRunPlayground_RejectsUnknownPolicy(t *testing.T) {
	if _, err := RunPlayground("nonexistent"); err == nil {
		t.Fatal("expected an error for an unknown policy")
	}
}

// TestComparePlayground_DetectsRealDivergence confirms two genuinely
// different policies against the identical PlaygroundScenario are
// reported as diverging -- the dashboard's counterfactual view is only
// useful if this actually fires for policies that really do decide
// differently.
func TestComparePlayground_DetectsRealDivergence(t *testing.T) {
	result, err := ComparePlayground("round-robin", "adaptive")
	if err != nil {
		t.Fatalf("ComparePlayground failed: %v", err)
	}
	if !result.Diverged {
		t.Fatal("expected round-robin and adaptive to diverge on the Playground scenario")
	}
	if result.DivergenceIndex <= 0 {
		t.Fatalf("expected a positive divergence index, got %d", result.DivergenceIndex)
	}
}

func TestComparePlayground_IdenticalPoliciesDoNotDiverge(t *testing.T) {
	result, err := ComparePlayground("round-robin", "round-robin")
	if err != nil {
		t.Fatalf("ComparePlayground failed: %v", err)
	}
	if result.Diverged {
		t.Fatal("expected the same policy compared against itself to never diverge")
	}
}

func TestListGroups_FindsKnownStages(t *testing.T) {
	groups, err := ListGroups()
	if err != nil {
		t.Fatalf("ListGroups failed: %v", err)
	}
	found := false
	for _, g := range groups {
		if g.Name == "008-tuning-validation" {
			found = true
			if g.ResultFileCount == 0 {
				t.Error("expected 008-tuning-validation to have result files")
			}
		}
	}
	if !found {
		t.Fatal("expected 008-tuning-validation among the listed experiment groups")
	}
}

func TestListResultFiles_RejectsPathTraversal(t *testing.T) {
	cases := []string{"../../etc", "..", ".", "foo/../bar", "a/b"}
	for _, c := range cases {
		if _, err := ListResultFiles(c); err == nil {
			t.Errorf("expected ListResultFiles(%q) to reject a path-traversal attempt", c)
		}
	}
}

// TestListResultFiles_RejectsPathTraversalEvenWhenTargetExists is the
// regression test for a real, adversarially-confirmed bug: the old
// safeName regex (`^[A-Za-z0-9._-]+$`) fully matched the literal string
// "..", since "." is an allowed character with unbounded repetition.
// ListResultFiles("..") only "passed" its rejection test before this
// fix because no experiments/../results directory happened to exist in
// this repo -- a false-negative test, not a real guard. This test
// creates that directory for real, so the rejection can only pass now
// because "." and ".." are explicitly rejected (isSafeName), not
// because the escape target happens to be absent.
func TestListResultFiles_RejectsPathTraversalEvenWhenTargetExists(t *testing.T) {
	if err := os.MkdirAll("results", 0755); err != nil {
		t.Fatalf("setting up escape-target directory: %v", err)
	}
	defer os.RemoveAll("results")
	if err := os.WriteFile("results/proof.json", []byte(`{"escaped":true}`), 0644); err != nil {
		t.Fatalf("writing escape-target file: %v", err)
	}

	if _, err := ListResultFiles(".."); err == nil {
		t.Fatal("expected ListResultFiles(\"..\") to be rejected even though experiments/../results now exists on disk")
	}
	if _, err := ReadResultFile("..", "proof.json"); err == nil {
		t.Fatal("expected ReadResultFile(\"..\", ...) to be rejected even though the escape target now exists on disk")
	}
}

func TestReadResultFile_RejectsPathTraversal(t *testing.T) {
	if _, err := ReadResultFile("008-tuning-validation", "../../../etc/passwd"); err == nil {
		t.Fatal("expected ReadResultFile to reject a path-traversal file name")
	}
	if _, err := ReadResultFile("../escape", "008A-tuning-machinery-validation.json"); err == nil {
		t.Fatal("expected ReadResultFile to reject a path-traversal group name")
	}
}

func TestReadResultFile_ReadsARealFile(t *testing.T) {
	content, err := ReadResultFile("008-tuning-validation", "008A-tuning-machinery-validation.json")
	if err != nil {
		t.Fatalf("ReadResultFile failed: %v", err)
	}
	if content["experiment"] == nil {
		t.Fatal("expected the parsed content to have an 'experiment' field")
	}
}

// TestReadResultFile_RejectsOversizedFile regression-tests F-42:
// ReadResultFile used to call os.ReadFile with no size bound at all on a
// path reachable from an HTTP request parameter.
func TestReadResultFile_RejectsOversizedFile(t *testing.T) {
	group := "zz-test-oversized-" + t.Name()
	dir := filepath.Join(ExperimentsRoot, group, "results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create test fixture dir: %v", err)
	}
	defer os.RemoveAll(filepath.Join(ExperimentsRoot, group))

	oversized := make([]byte, maxResultFileBytes+1)
	for i := range oversized {
		oversized[i] = ' '
	}
	// Still syntactically valid JSON padding, so a size failure (not a
	// parse failure) is unambiguously what's being tested.
	oversized[0] = '{'
	oversized[len(oversized)-1] = '}'
	path := filepath.Join(dir, "huge.json")
	if err := os.WriteFile(path, oversized, 0o644); err != nil {
		t.Fatalf("failed to write oversized fixture: %v", err)
	}

	if _, err := ReadResultFile(group, "huge.json"); err == nil {
		t.Fatalf("expected ReadResultFile to reject a file exceeding maxResultFileBytes")
	}
}

func TestLoadTuningSummary_HandlesRealArtifacts(t *testing.T) {
	summary := LoadTuningSummary()
	if !summary.Available {
		t.Skip("008B-search-ledger.json not present in this environment -- skipping rather than failing, since this is a real-artifact integration check")
	}
	if len(summary.Evaluations) == 0 {
		t.Error("expected a non-empty evaluations list when a ledger is available")
	}
}
