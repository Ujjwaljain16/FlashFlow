package challenge

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/proxy"
	"flashflow/internal/replay"
)

func routingKeyFor(i int) string {
	if i%2 == 0 {
		return "/hot"
	}
	return fmt.Sprintf("/cold-%d", i%3)
}

func identicalTargetsScenario(n int) replay.Scenario {
	const requests = 300
	const spacing = 5 * time.Millisecond
	targets := make([]replay.TargetProfile, n)
	names := []string{"x", "y", "z", "w", "v"}
	for i := 0; i < n; i++ {
		targets[i] = replay.TargetProfile{Name: names[i], ServiceTime: 20 * time.Millisecond}
	}
	arrivals := make([]replay.Arrival, requests)
	for i := 0; i < requests; i++ {
		arrivals[i] = replay.Arrival{At: clock.VirtualTime(spacing.Nanoseconds() * int64(i)), Key: routingKeyFor(i)}
	}
	return replay.Scenario{Targets: targets, Arrivals: arrivals}
}

// TestRoutingChallenge_IdenticalTargets exercises every policy against
// 3 genuinely equal targets -- 006-B/007-B's own negative case, given a
// permanent regression home here rather than only living inside a
// point-in-time experiment. Round Robin, Least Connections, P2C, and
// Adaptive should all split traffic roughly evenly; EWMA is EXPECTED to
// lock in hard (its own documented, evidence-based limitation, not a
// bug) -- asserting that explicitly is what would catch a future change
// accidentally "fixing" or worsening it without anyone noticing.
func TestRoutingChallenge_IdenticalTargets(t *testing.T) {
	sc := identicalTargetsScenario(3)

	maxShare := func(spec replay.PolicySpec) float64 {
		result, err := replay.RunWorld(sc, spec)
		if err != nil {
			t.Fatalf("%s: RunWorld failed: %v", spec.Name, err)
		}
		counts := map[string]int{}
		for _, rec := range result.Records {
			counts[rec.Target]++
		}
		max := 0
		for _, c := range counts {
			if c > max {
				max = c
			}
		}
		return float64(max) / float64(len(result.Records))
	}

	const fairThreshold = 0.45 // comfortably above 1/3 (0.333) but well below lock-in
	fair := map[string]replay.PolicySpec{
		"round-robin":       replay.RoundRobinPolicy(),
		"least-connections": replay.LeastConnectionsPolicy(),
		"p2c-load":          replay.P2CLoadPolicy(),
		"adaptive":          replay.AdaptivePolicy(),
	}
	for name, spec := range fair {
		if share := maxShare(spec); share > fairThreshold {
			t.Errorf("%s: expected a roughly even split among 3 identical targets (max share <= %.2f), got %.3f", name, fairThreshold, share)
		}
	}

	// EWMA's lock-in is the documented, evidence-based exception (006-B):
	// asserting it happens is a regression anchor for a KNOWN limitation,
	// not a bug report.
	if share := maxShare(replay.EWMAPolicy()); share < 0.8 {
		t.Errorf("EWMA: expected its well-established lock-in among identical targets (max share >= 0.80), got %.3f -- if this legitimately changed, 006-B's finding needs revisiting, not just this test", share)
	}
}

// TestRoutingChallenge_ExtremeCapacityRatio_WRR confirms
// WeightedRoundRobinSelector actually honors an extreme (1000:1)
// configured weight ratio -- the smooth weighted round-robin algorithm
// must not degrade toward even distribution at extreme ratios the way
// a naive or overflow-prone implementation might.
func TestRoutingChallenge_ExtremeCapacityRatio_WRR(t *testing.T) {
	weights := proxy.TargetWeights{"heavy": 1000, "light": 1}
	sel := proxy.NewWeightedRoundRobinSelector(weights)
	r := httptest.NewRequest(http.MethodGet, "/x", nil)

	counts := map[string]int{}
	for i := 0; i < 1001; i++ {
		target, err := sel.SelectTarget(r, []string{"heavy", "light"})
		if err != nil {
			t.Fatalf("SelectTarget failed: %v", err)
		}
		counts[target]++
	}
	if counts["light"] != 1 {
		t.Fatalf("expected exactly 1 selection of the 1x-weighted target per 1001-request cycle, got %d (heavy=%d)", counts["light"], counts["heavy"])
	}
	if counts["heavy"] != 1000 {
		t.Fatalf("expected exactly 1000 selections of the 1000x-weighted target per cycle, got %d", counts["heavy"])
	}
}

// TestRoutingChallenge_ExtremeCapacityRatio_Adaptive confirms Adaptive
// avoids a target with an extreme (50x) service-time disadvantage
// overwhelmingly, not just "somewhat better than even."
func TestRoutingChallenge_ExtremeCapacityRatio_Adaptive(t *testing.T) {
	const requests = 300
	const spacing = 5 * time.Millisecond
	arrivals := make([]replay.Arrival, requests)
	for i := 0; i < requests; i++ {
		arrivals[i] = replay.Arrival{At: clock.VirtualTime(spacing.Nanoseconds() * int64(i)), Key: routingKeyFor(i)}
	}
	sc := replay.Scenario{
		Targets: []replay.TargetProfile{
			{Name: "fast", ServiceTime: 4 * time.Millisecond},
			{Name: "slow", ServiceTime: 200 * time.Millisecond}, // 50x
		},
		Arrivals: arrivals,
	}

	result, err := replay.RunWorld(sc, replay.AdaptivePolicy())
	if err != nil {
		t.Fatalf("RunWorld failed: %v", err)
	}
	slowCount := 0
	for _, rec := range result.Records {
		if rec.Target == "slow" {
			slowCount++
		}
	}
	slowShare := float64(slowCount) / float64(len(result.Records))
	if slowShare > 0.10 {
		t.Fatalf("expected Adaptive to send well under 10%% of traffic to a 50x-slower target, got %.1f%%", slowShare*100)
	}
}
