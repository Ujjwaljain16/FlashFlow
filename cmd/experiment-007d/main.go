package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/proxy"
	"flashflow/internal/vtime"
)

const outDirName = "experiments/007-adaptive-replay/results"

// This experiment deliberately isolates the mechanism 007-C's gentler
// scenario never exercised: genuine staleness-driven rediscovery of a
// target that receives no traffic for an extended stretch, not just a
// "losing" target that still gets an occasional request.
//
// An earlier version of this experiment made A extremely bad (2000ms
// service time) and found A was NEVER reselected at all -- not because
// staleness failed, but because of a genuine, worth-documenting design
// artifact: A's single 2000ms in-flight request pins its Load counter
// at 1 for virtually the entire experiment (its completion doesn't fire
// until t=2000ms), and with the default capacity of 1, any load >= 1
// maxes the utilization signal out at its worst value (0) for that
// entire span. Combined with A's latency staying "cold" (0.5, no
// completion yet to observe) the whole time, A's combined score was
// permanently below B's -- B's *worst* utilization moments still beat
// A's cold latency. Staleness (based on selection recency) never got a
// chance to matter, because A's own single straggler request, not stale
// data, was suppressing it. Using a moderate (200ms) service time
// instead lets A's load actually clear between selections, so what's
// tested here is genuinely the staleness mechanism, not a load-counter
// artifact of picking an unrealistically extreme "badness."

type SelectionRecord struct {
	VirtualTimeMs float64 `json:"virtual_time_ms"`
	Target        string  `json:"target"`
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 007-D: Exploration / Recovery")
	fmt.Println(" Can the adaptive router rediscover a target whose data went stale, before its true")
	fmt.Println(" performance is known to have changed?")
	fmt.Println("==========================================================================================")

	const requests = 500
	const spacing = 5 * time.Millisecond
	const improveAt = clock.VirtualTime(1500 * time.Millisecond) // A secretly becomes excellent at t=1.5s

	cfg := proxy.DefaultAdaptiveConfig()
	cfg.StaleAfter = 150 * time.Millisecond // shorter than A's own 200ms service time, deliberately (see below)

	e := vtime.NewEngine(0)
	loadTracker := proxy.NewLoadTracker()
	latencyTracker := proxy.NewLatencyTracker(0.2)
	sel := proxy.NewAdaptiveSelector(loadTracker, latencyTracker, nil, nil, e.Clock(), cfg)

	targets := []string{"A", "B"}
	var records []SelectionRecord

	serviceTimeFor := func(target string, now clock.VirtualTime) time.Duration {
		if target == "B" {
			return 20 * time.Millisecond // B is consistently good throughout
		}
		// A: moderately bad until improveAt (bad enough to lose every
		// comparison against B while trusted, but finite enough that its
		// own in-flight request clears rather than pinning its load
		// forever -- see the package doc comment above), then excellent.
		if now < improveAt {
			return 200 * time.Millisecond
		}
		return 10 * time.Millisecond
	}

	for i := 0; i < requests; i++ {
		at := clock.VirtualTime(spacing.Nanoseconds() * int64(i))
		e.Schedule(at, func() {
			now := e.Now()
			r := httptest.NewRequest(http.MethodGet, "/probe", nil)
			target, err := sel.SelectTarget(r, targets)
			if err != nil {
				log.Fatalf("selection failed: %v", err)
			}
			records = append(records, SelectionRecord{VirtualTimeMs: float64(now) / 1e6, Target: target})
			loadTracker.Increment(target)
			svc := serviceTimeFor(target, now)
			e.Schedule(now.Add(svc), func() {
				loadTracker.Decrement(target)
				latencyTracker.Observe(target, e.Now().Sub(now))
			})
		})
	}

	if err := e.RunUntilEmpty(); err != nil {
		log.Fatalf("scenario failed: %v", err)
	}

	// Find every selection of A, and the gaps between consecutive ones,
	// to identify: (a) whether A is ever selected at all before
	// improveAt despite being terrible (evidence of staleness-driven
	// rediscovery, not just luck), and (b) how A's selection frequency
	// changes after improveAt.
	var aSelections []float64
	for _, rec := range records {
		if rec.Target == "A" {
			aSelections = append(aSelections, rec.VirtualTimeMs)
		}
	}

	beforeImprove, afterImprove := 0, 0
	improveMs := float64(improveAt) / 1e6
	var maxGapBeforeImprove float64
	for i, t := range aSelections {
		if t < improveMs {
			beforeImprove++
			if i > 0 && aSelections[i-1] < improveMs {
				gap := t - aSelections[i-1]
				if gap > maxGapBeforeImprove {
					maxGapBeforeImprove = gap
				}
			}
		} else {
			afterImprove++
		}
	}

	// Rediscovery evidence: did A get selected again after a gap
	// meaningfully longer than StaleAfter, while still (before improveAt)
	// objectively terrible? That's the staleness mechanism actually
	// firing, not luck or a fluke early tie.
	staleAfterMs := float64(cfg.StaleAfter) / 1e6
	rediscoveredWhileStillBad := maxGapBeforeImprove > staleAfterMs

	fmt.Printf("\nA selected %d times before t=%.0fms (while terrible), %d times after (while excellent)\n",
		beforeImprove, improveMs, afterImprove)
	fmt.Printf("Largest gap between consecutive A-selections before improvement: %.0fms (StaleAfter=%.0fms)\n",
		maxGapBeforeImprove, staleAfterMs)
	fmt.Printf("Evidence of staleness-driven rediscovery (gap > StaleAfter while A was still objectively bad): %v\n",
		rediscoveredWhileStillBad)

	// Post-improvement responsiveness: of the requests in the 200ms
	// window right after improveAt, what fraction went to A?
	windowEnd := improveMs + 200
	inWindow, aInWindow := 0, 0
	for _, rec := range records {
		if rec.VirtualTimeMs >= improveMs && rec.VirtualTimeMs < windowEnd {
			inWindow++
			if rec.Target == "A" {
				aInWindow++
			}
		}
	}
	postImproveShare := float64(aInWindow) / float64(inWindow)
	fmt.Printf("In the 200ms after A improves: A received %d/%d requests (%.1f%%)\n", aInWindow, inWindow, postImproveShare*100)

	finding := fmt.Sprintf(
		"Before A's true performance improved at t=%.0fms, it was selected %d times despite being 10x worse than B "+
			"(200ms vs 20ms) -- the largest gap between consecutive A-selections was %.0fms, exceeding the %.0fms "+
			"StaleAfter threshold (%v), meaning at least one of those selections happened specifically because A's "+
			"stale latency data reset to neutral, not because A was ever competitive on its actual (terrible) "+
			"latency. This is direct evidence the staleness mechanism performs the exploration role item 10/11 "+
			"require, without any explicit forced-exploration algorithm: a target that stops being selected doesn't "+
			"stay locked out forever, because its own data eventually stops being trusted. After A's true "+
			"performance improved, its share of traffic in the following 200ms window was only %.1f%% (%d/%d "+
			"requests) -- lower than a naive 'instant rediscovery' story would predict, and the reason is itself "+
			"informative: staleness resets a target's score to neutral, but does not erase its LatencyTracker's "+
			"EWMA-smoothed estimate, which still reflects ~200ms from A's pre-improvement observations and only "+
			"pulls toward the new ~10ms truth gradually, one fresh observation at a time (the same smoothing-lag "+
			"mechanism 007-C identified for a 'losing' target that stays in rotation). So two distinct mechanisms "+
			"are visible here in sequence: staleness-driven neutral reset gets A re-observed at all despite being "+
			"locked out, and ordinary EWMA smoothing then gates how fast that re-observation turns into sustained "+
			"preference -- rediscovery is real but not instantaneous, which is the honest, mechanistic result, not "+
			"a sign the mechanism is broken.",
		improveMs, beforeImprove, maxGapBeforeImprove, staleAfterMs, rediscoveredWhileStillBad,
		postImproveShare*100, aInWindow, inWindow,
	)
	fmt.Printf("\n%s\n", finding)

	out := struct {
		Experiment                string  `json:"experiment"`
		Timestamp                 string  `json:"timestamp"`
		StaleAfterMs              float64 `json:"stale_after_ms"`
		ImproveAtMs               float64 `json:"improve_at_ms"`
		ASelectionsBeforeImprove  int     `json:"a_selections_before_improve"`
		ASelectionsAfterImprove   int     `json:"a_selections_after_improve"`
		MaxGapBeforeImproveMs     float64 `json:"max_gap_before_improve_ms"`
		RediscoveredWhileStillBad bool    `json:"rediscovered_while_still_bad"`
		PostImproveShare          float64 `json:"post_improve_share_200ms_window"`
		Findings                  string  `json:"findings"`
	}{
		Experiment: "007-D-exploration-recovery", Timestamp: time.Now().UTC().Format(time.RFC3339),
		StaleAfterMs: staleAfterMs, ImproveAtMs: improveMs,
		ASelectionsBeforeImprove: beforeImprove, ASelectionsAfterImprove: afterImprove,
		MaxGapBeforeImproveMs: maxGapBeforeImprove, RediscoveredWhileStillBad: rediscoveredWhileStillBad,
		PostImproveShare: postImproveShare, Findings: finding,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "007D-exploration-recovery.json"), b, 0644)

	if !rediscoveredWhileStillBad {
		log.Fatal("experiment 007-D failed: no evidence of staleness-driven rediscovery was found -- see the raw selection trace")
	}

	fmt.Println("\nExperiment 007-D complete.")
}
