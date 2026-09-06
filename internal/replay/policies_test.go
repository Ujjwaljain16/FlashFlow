package replay

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flashflow/internal/clock"
)

// TestWeightedRoundRobinPolicy_FavorsFasterTarget confirms
// PolicySpec.New actually receives the Scenario's real target profiles
// (the reason its signature grew a targets parameter) and that
// WeightedRoundRobinPolicy uses them to assign a favorable weight to
// the genuinely faster target -- not a fixed or ignored value.
func TestWeightedRoundRobinPolicy_FavorsFasterTarget(t *testing.T) {
	scenario := Scenario{
		Targets: []TargetProfile{
			{Name: "fast", ServiceTime: 10 * time.Millisecond},
			{Name: "slow", ServiceTime: 100 * time.Millisecond},
		},
	}
	spec := WeightedRoundRobinPolicy()
	sel, _ := spec.New(clock.NewWallClock(), SeedTree{}, scenario.Targets)

	counts := map[string]int{}
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	for i := 0; i < 100; i++ {
		target, err := sel.SelectTarget(r, []string{"fast", "slow"})
		if err != nil {
			t.Fatalf("SelectTarget failed: %v", err)
		}
		counts[target]++
	}

	if counts["fast"] <= counts["slow"] {
		t.Fatalf("expected the 10ms target to receive more traffic than the 100ms target under WRR weights derived from ServiceTime, got %v", counts)
	}
	// The configured ratio is ~10:1 (weight ~100 vs ~10); confirm the
	// realized distribution reflects a substantial skew, not just any
	// skew, without asserting the exact ratio (smooth WRR's ordering
	// within one full weight cycle isn't a simple modulo pattern).
	if counts["fast"] < 70 {
		t.Fatalf("expected the fast target to receive a strong majority (~90%%) of traffic, got %v", counts)
	}
}

func TestWeightedRoundRobinPolicy_IgnoresUnusedParametersSafely(t *testing.T) {
	spec := WeightedRoundRobinPolicy()
	// No targets at all: weights map ends up empty, but
	// NewWeightedRoundRobinSelector must still work (defaultWeight=1
	// for anything not in the map), since `available` at call time can
	// still list targets never seen in Targets (a defensive case, not
	// one RunWorld would normally produce).
	sel, instr := spec.New(clock.NewWallClock(), SeedTree{}, nil)
	if sel == nil || instr == nil {
		t.Fatal("expected a non-nil selector and instrumentation even with no target profiles")
	}
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	if _, err := sel.SelectTarget(r, []string{"only-target"}); err != nil {
		t.Fatalf("SelectTarget failed with an empty weights map: %v", err)
	}
}
