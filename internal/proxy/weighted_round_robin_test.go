package proxy

import (
	"sync"
	"testing"
)

// TestWRR_ExactPeriodicDistribution verifies the strong invariant of smooth
// weighted round robin: over any whole number of periods (period = sum of
// effective weights), each target is selected EXACTLY weight/total times —
// not merely "approximately", and the state returns exactly to zero at the
// end of each period. This is a stronger check than sampling an approximate
// ratio, and it is the textbook nginx example (weights 5:1:1).
func TestWRR_ExactPeriodicDistribution(t *testing.T) {
	sel := NewWeightedRoundRobinSelector(TargetWeights{"a": 5, "b": 1, "c": 1})
	available := []string{"a", "b", "c"}

	const period = 7 // sum of weights
	const periods = 100

	counts := map[string]int{}
	for i := 0; i < period*periods; i++ {
		target, err := sel.SelectTarget(nil, available)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		counts[target]++
	}

	want := map[string]int{"a": 5 * periods, "b": 1 * periods, "c": 1 * periods}
	for k, v := range want {
		if counts[k] != v {
			t.Fatalf("target %s: expected exactly %d selections over %d whole periods, got %d (counts=%v)",
				k, v, periods, counts[k], counts)
		}
	}

	// State must return to exactly zero at a period boundary.
	for _, target := range available {
		st := sel.state[target]
		if st.currentWeight != 0 {
			t.Fatalf("target %s: expected currentWeight==0 at period boundary, got %d", target, st.currentWeight)
		}
	}
}

// TestWRR_NoBurstiness verifies the actual motivation for choosing smooth
// WRR over naive sequence-expansion WRR (see the doc comment on
// WeightedRoundRobinSelector): the highest-weighted target must never be
// selected as many times in a row as its full weight — naive expansion of
// weights 4:1:1 would produce the literal sequence [A,A,A,A,B,C], a run of
// 4 consecutive A's.
//
// This test originally asserted a run length <= 2. That was wrong: a
// standalone simulation of the exact algorithm (not this test — a plain
// single-threaded trace of the same select/subtract steps) showed the true
// single-period sequence for weights 4:1:1 (period 6) is [A,A,B,A,C,A], and
// because the algorithm is exactly periodic (state returns to zero at every
// period boundary — see TestWRR_ExactPeriodicDistribution), consecutive
// periods concatenate as [...,C,A | A,A,B,...] — i.e. the last A of one
// period is immediately followed by the first two A's of the next,
// producing a genuine run of 3, not 2, once every period. This is a real,
// deterministic property of the algorithm (it occurs because A's weight,
// 4, is more than half of the total, 6) and is not a bug: it is still
// strictly better than naive expansion's guaranteed run of 4, which is the
// property actually worth asserting.
func TestWRR_NoBurstiness(t *testing.T) {
	const weightA = 4
	sel := NewWeightedRoundRobinSelector(TargetWeights{"a": weightA, "b": 1, "c": 1})
	available := []string{"a", "b", "c"}

	var sequence []string
	for i := 0; i < 60; i++ {
		target, err := sel.SelectTarget(nil, available)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sequence = append(sequence, target)
	}

	maxRun := 1
	run := 1
	for i := 1; i < len(sequence); i++ {
		if sequence[i] == sequence[i-1] {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 1
		}
	}

	if maxRun >= weightA {
		t.Fatalf("expected smooth WRR's max consecutive run for weight %d to be strictly less than naive "+
			"sequence-expansion's guaranteed run of %d, got a run of %d in sequence %v",
			weightA, weightA, maxRun, sequence)
	}
}

func TestWRR_ZeroOrNegativeWeightDefaultsToOne(t *testing.T) {
	sel := NewWeightedRoundRobinSelector(TargetWeights{"a": 0, "b": -5})
	available := []string{"a", "b"}

	counts := map[string]int{}
	for i := 0; i < 20; i++ {
		target, err := sel.SelectTarget(nil, available)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		counts[target]++
	}

	// Both weight<=0 configs should have been coerced to weight 1, so the
	// 20 selections should split exactly 10/10 like plain round robin.
	if counts["a"] != 10 || counts["b"] != 10 {
		t.Fatalf("expected 10/10 split for coerced-to-1 weights, got %v", counts)
	}
}

func TestWRR_MissingWeightDefaultsToOne(t *testing.T) {
	// "c" is never configured — must behave as weight 1, same as "a" and "b".
	sel := NewWeightedRoundRobinSelector(TargetWeights{"a": 1, "b": 1})
	available := []string{"a", "b", "c"}

	const period = 3
	const periods = 50
	counts := map[string]int{}
	for i := 0; i < period*periods; i++ {
		target, _ := sel.SelectTarget(nil, available)
		counts[target]++
	}

	for _, k := range available {
		if counts[k] != periods {
			t.Fatalf("target %s: expected exactly %d selections, got %d (counts=%v)", k, periods, counts[k], counts)
		}
	}
}

func TestWRR_SingleTarget(t *testing.T) {
	sel := NewWeightedRoundRobinSelector(TargetWeights{"only": 7})
	for i := 0; i < 10; i++ {
		target, err := sel.SelectTarget(nil, []string{"only"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target != "only" {
			t.Fatalf("expected 'only', got %q", target)
		}
	}
}

func TestWRR_EmptyAvailable(t *testing.T) {
	sel := NewWeightedRoundRobinSelector(TargetWeights{"a": 1})
	_, err := sel.SelectTarget(nil, nil)
	if err != ErrNoHealthyTargets {
		t.Fatalf("expected ErrNoHealthyTargets, got %v", err)
	}
}

// TestWRR_TargetTemporaryRemoval verifies that a target dropping out of
// `available` (e.g. because health marked it UNHEALTHY) and later
// reappearing does not panic, does not corrupt other targets' state, and
// the temporarily-removed target resumes participating in rotation once it
// reappears in `available`.
func TestWRR_TargetTemporaryRemoval(t *testing.T) {
	sel := NewWeightedRoundRobinSelector(TargetWeights{"a": 1, "b": 1, "c": 1})

	full := []string{"a", "b", "c"}
	withoutC := []string{"a", "b"}

	// c participates initially.
	for i := 0; i < 6; i++ {
		if _, err := sel.SelectTarget(nil, full); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// c drops out for a while (simulating UNHEALTHY).
	for i := 0; i < 10; i++ {
		target, err := sel.SelectTarget(nil, withoutC)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target == "c" {
			t.Fatalf("target c must never be selected while absent from `available`")
		}
	}

	// c returns; it must still be selectable (not permanently excluded).
	seenC := false
	for i := 0; i < 10; i++ {
		target, err := sel.SelectTarget(nil, full)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target == "c" {
			seenC = true
		}
	}
	if !seenC {
		t.Fatalf("target c did not resume participating in rotation after reappearing in `available`")
	}
}

// TestWRR_ConcurrentSelection verifies the compound read-modify-decide-write
// sequence in SelectTarget is safe under real concurrent access: the total
// number of selections recorded must equal exactly the number of calls made
// (no lost or duplicated selections), and the long-run distribution must
// still approximate the configured weight ratio.
func TestWRR_ConcurrentSelection(t *testing.T) {
	sel := NewWeightedRoundRobinSelector(TargetWeights{"a": 3, "b": 1})
	available := []string{"a", "b"}

	const goroutines = 50
	const perGoroutine = 400 // total = 20000, period = 4, so exactly divisible

	var mu sync.Mutex
	counts := map[string]int{}
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				target, err := sel.SelectTarget(nil, available)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				mu.Lock()
				counts[target]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	total := counts["a"] + counts["b"]
	wantTotal := goroutines * perGoroutine
	if total != wantTotal {
		t.Fatalf("expected exactly %d total selections, got %d (counts=%v) — indicates a lost/duplicated selection under concurrency",
			wantTotal, total, counts)
	}

	// With weights 3:1, "a" should get ~75% of selections. Allow a
	// generous tolerance since goroutine interleaving order is
	// nondeterministic (unlike the single-threaded exact-period tests
	// above), we only assert the ratio is in the right ballpark.
	ratio := float64(counts["a"]) / float64(total)
	if ratio < 0.70 || ratio > 0.80 {
		t.Fatalf("expected 'a' to receive roughly 75%% of selections under concurrency, got %.2f%% (counts=%v)",
			ratio*100, counts)
	}
}
