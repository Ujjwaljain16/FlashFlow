package traffic

import (
	"math"
	"sort"
	"testing"
	"time"
)

func TestRampInverse_Endpoints(t *testing.T) {
	cases := []struct{ r0, r1 float64 }{
		{10, 10}, // constant, degenerate case
		{5, 20},  // increasing
		{20, 5},  // decreasing
		{0, 10},  // starts at zero
	}
	for _, c := range cases {
		x0 := rampInverse(c.r0, c.r1, 0)
		x1 := rampInverse(c.r0, c.r1, 1)
		if math.Abs(x0-0) > 1e-9 {
			t.Errorf("rampInverse(%v,%v,0) = %v, want 0", c.r0, c.r1, x0)
		}
		if math.Abs(x1-1) > 1e-9 {
			t.Errorf("rampInverse(%v,%v,1) = %v, want 1", c.r0, c.r1, x1)
		}
		xMid := rampInverse(c.r0, c.r1, 0.5)
		if xMid < x0 || xMid > x1 {
			t.Errorf("rampInverse(%v,%v,0.5) = %v, expected within [%v,%v]", c.r0, c.r1, xMid, x0, x1)
		}
	}
}

// TestGenerate_Constant_ExactGrid hand-computes the expected arrival
// times: Requests=4 over a 1000ms Horizon puts arrival i at
// (i+0.5)/4 * 1000ms = 125, 375, 625, 875ms.
func TestGenerate_Constant_ExactGrid(t *testing.T) {
	arrivals, err := Generate(Constant, Params{Requests: 4, Horizon: 1000 * time.Millisecond}, 1)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	want := []time.Duration{125 * time.Millisecond, 375 * time.Millisecond, 625 * time.Millisecond, 875 * time.Millisecond}
	if len(arrivals) != len(want) {
		t.Fatalf("got %d arrivals, want %d", len(arrivals), len(want))
	}
	for i, w := range want {
		got := time.Duration(arrivals[i].At.Nanoseconds())
		if got != w {
			t.Errorf("arrival %d: got %v, want %v", i, got, w)
		}
		if arrivals[i].Key != "/" {
			t.Errorf("arrival %d: got key %q, want default \"/\"", i, arrivals[i].Key)
		}
	}
}

func TestGenerate_ExactCount(t *testing.T) {
	patterns := []Pattern{Constant, RampUp, RampDown, Burst, FlashCrowd}
	for _, p := range patterns {
		params := Params{
			Requests: 137, Horizon: 10 * time.Second,
			BaseRate: 10, PeakRate: 100,
			BurstAt: 5 * time.Second, BurstWidth: 2 * time.Second,
		}
		arrivals, err := Generate(p, params, 1)
		if err != nil {
			t.Fatalf("Generate(%s) failed: %v", p, err)
		}
		if len(arrivals) != 137 {
			t.Errorf("Generate(%s): got %d arrivals, want 137", p, len(arrivals))
		}
		for _, a := range arrivals {
			if a.At < 0 || a.At.Nanoseconds() > int64(params.Horizon) {
				t.Errorf("Generate(%s): arrival at %v falls outside [0, %v]", p, a.At, params.Horizon)
			}
		}
	}
}

// TestGenerate_UnjitteredIsMonotonic checks that with zero jitter, each
// pattern's arrival grid comes out already time-sorted -- expected since
// u_i is monotonically increasing in i and every inverse-CDF used is
// itself monotonic in u.
func TestGenerate_UnjitteredIsMonotonic(t *testing.T) {
	patterns := []Pattern{Constant, RampUp, RampDown, Burst, FlashCrowd}
	for _, p := range patterns {
		params := Params{
			Requests: 50, Horizon: 10 * time.Second,
			BaseRate: 10, PeakRate: 100,
			BurstAt: 5 * time.Second, BurstWidth: 2 * time.Second,
		}
		arrivals, err := Generate(p, params, 1)
		if err != nil {
			t.Fatalf("Generate(%s) failed: %v", p, err)
		}
		if !sort.SliceIsSorted(arrivals, func(i, j int) bool { return arrivals[i].At < arrivals[j].At }) {
			t.Errorf("Generate(%s) with zero jitter produced a non-monotonic arrival grid", p)
		}
	}
}

func countInWindow(arrivals []time.Duration, lo, hi time.Duration) int {
	n := 0
	for _, a := range arrivals {
		if a >= lo && a < hi {
			n++
		}
	}
	return n
}

func atDurations(t *testing.T, pattern Pattern, params Params, seed int64) []time.Duration {
	t.Helper()
	arrivals, err := Generate(pattern, params, seed)
	if err != nil {
		t.Fatalf("Generate(%s) failed: %v", pattern, err)
	}
	out := make([]time.Duration, len(arrivals))
	for i, a := range arrivals {
		out[i] = time.Duration(a.At.Nanoseconds())
	}
	return out
}

// TestGenerate_RampUp_DensityGrowsOverTime checks the bucketed-density
// property: a linearly increasing rate must put more arrivals in the
// second half of the Horizon than the first.
func TestGenerate_RampUp_DensityGrowsOverTime(t *testing.T) {
	horizon := 10 * time.Second
	arr := atDurations(t, RampUp, Params{Requests: 1000, Horizon: horizon, BaseRate: 10, PeakRate: 100}, 1)
	firstHalf := countInWindow(arr, 0, horizon/2)
	secondHalf := countInWindow(arr, horizon/2, horizon)
	if secondHalf <= firstHalf {
		t.Errorf("RampUp: expected more arrivals in the second half (rate is higher there), got first=%d second=%d", firstHalf, secondHalf)
	}
}

func TestGenerate_RampDown_DensityShrinksOverTime(t *testing.T) {
	horizon := 10 * time.Second
	arr := atDurations(t, RampDown, Params{Requests: 1000, Horizon: horizon, BaseRate: 10, PeakRate: 100}, 1)
	firstHalf := countInWindow(arr, 0, horizon/2)
	secondHalf := countInWindow(arr, horizon/2, horizon)
	if firstHalf <= secondHalf {
		t.Errorf("RampDown: expected more arrivals in the first half (rate is higher there), got first=%d second=%d", firstHalf, secondHalf)
	}
}

// TestGenerate_Burst_SpikeInsideWindow checks that the burst window's
// arrival density (count/width) is much higher than the surrounding
// baseline density.
func TestGenerate_Burst_SpikeInsideWindow(t *testing.T) {
	horizon := 10 * time.Second
	burstAt, burstWidth := 5*time.Second, 1*time.Second
	arr := atDurations(t, Burst, Params{
		Requests: 2000, Horizon: horizon, BaseRate: 10, PeakRate: 200,
		BurstAt: burstAt, BurstWidth: burstWidth,
	}, 1)

	inBurst := countInWindow(arr, burstAt-burstWidth/2, burstAt+burstWidth/2)
	outside := len(arr) - inBurst
	burstDensity := float64(inBurst) / burstWidth.Seconds()
	outsideDensity := float64(outside) / (horizon - burstWidth).Seconds()

	if burstDensity <= outsideDensity*2 {
		t.Errorf("Burst: expected burst-window density well above baseline, got burst=%.1f/s outside=%.1f/s", burstDensity, outsideDensity)
	}
}

// TestGenerate_FlashCrowd_DecayCarriesProportionallyMoreTraffic checks
// the property this piecewise-LINEAR model actually has: since both the
// rise (BaseRate->PeakRate) and decay (PeakRate->BaseRate) segments are
// straight ramps spanning the identical rate range, each has the same
// AVERAGE rate (Base+Peak)/2 regardless of width -- so arrival DENSITY
// (count per second) comes out equal on both sides, and what actually
// differs is the TOTAL count, which scales with width. Decay gets 3x
// the width (see flashCrowdInverter's doc comment), so it should carry
// roughly 3x the rise segment's arrival count. (A literal "instantaneous
// spike then slow decay" shape was considered and rejected: a
// near-zero-width rise segment would contain close to none of the
// discretely-sampled arrival grid points, making the shape untestable
// at any achievable Requests count.)
func TestGenerate_FlashCrowd_DecayCarriesProportionallyMoreTraffic(t *testing.T) {
	horizon := 20 * time.Second
	burstAt, burstWidth := 10*time.Second, 4*time.Second
	arr := atDurations(t, FlashCrowd, Params{
		Requests: 4000, Horizon: horizon, BaseRate: 10, PeakRate: 200,
		BurstAt: burstAt, BurstWidth: burstWidth,
	}, 1)

	riseWidth := burstWidth / 4
	decayWidth := burstWidth - riseWidth
	riseCount := countInWindow(arr, burstAt-riseWidth, burstAt)
	decayCount := countInWindow(arr, burstAt, burstAt+decayWidth)

	if riseCount == 0 {
		t.Fatal("expected at least some arrivals in the rise window")
	}
	ratio := float64(decayCount) / float64(riseCount)
	wantRatio := decayWidth.Seconds() / riseWidth.Seconds() // 3.0
	if math.Abs(ratio-wantRatio) > 0.3 {
		t.Errorf("FlashCrowd: decay/rise count ratio = %.2f, want close to %.2f (proportional to width, since density is equal on both sides)", ratio, wantRatio)
	}

	riseDensity := float64(riseCount) / riseWidth.Seconds()
	decayDensity := float64(decayCount) / decayWidth.Seconds()
	if math.Abs(riseDensity-decayDensity)/decayDensity > 0.2 {
		t.Errorf("FlashCrowd: expected rise and decay densities to be close (same average rate (Base+Peak)/2 on both sides), got rise=%.1f/s decay=%.1f/s", riseDensity, decayDensity)
	}

	baseDensity := float64(countInWindow(arr, 0, burstAt-riseWidth)) / (burstAt - riseWidth).Seconds()
	if decayDensity <= baseDensity*2 {
		t.Errorf("FlashCrowd: expected elevated density during the crowd window well above baseline, got elevated=%.1f/s baseline=%.1f/s", decayDensity, baseDensity)
	}
}

func TestGenerate_Determinism(t *testing.T) {
	params := Params{Requests: 100, Horizon: 10 * time.Second, BaseRate: 10, PeakRate: 100, JitterFraction: 0.5}
	a, err := Generate(RampUp, params, 42)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	b, err := Generate(RampUp, params, 42)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	for i := range a {
		if a[i].At != b[i].At {
			t.Fatalf("same seed produced different arrival %d: %v vs %v", i, a[i].At, b[i].At)
		}
	}
	c, err := Generate(RampUp, params, 43)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	same := true
	for i := range a {
		if a[i].At != c[i].At {
			same = false
			break
		}
	}
	if same {
		t.Fatal("expected a different seed to produce at least one different jittered arrival time")
	}
}

func TestGenerate_RejectsInvalidParams(t *testing.T) {
	cases := []struct {
		name    string
		pattern Pattern
		params  Params
	}{
		{"zero requests", Constant, Params{Requests: 0, Horizon: time.Second}},
		{"negative requests", Constant, Params{Requests: -1, Horizon: time.Second}},
		{"zero horizon", Constant, Params{Requests: 10, Horizon: 0}},
		{"unknown pattern", Pattern("bogus"), Params{Requests: 10, Horizon: time.Second}},
		{"both rates zero", RampUp, Params{Requests: 10, Horizon: time.Second, BaseRate: 0, PeakRate: 0}},
		{"negative rate", RampUp, Params{Requests: 10, Horizon: time.Second, BaseRate: -1, PeakRate: 10}},
	}
	for _, c := range cases {
		if _, err := Generate(c.pattern, c.params, 1); err == nil {
			t.Errorf("%s: expected an error, got none", c.name)
		}
	}
}

func TestHotColdKeys(t *testing.T) {
	allHot := HotColdKeys(1.0)
	for i := 0; i < 10; i++ {
		if got := allHot(i); got != "/hot" {
			t.Errorf("HotColdKeys(1.0)(%d) = %q, want \"/hot\"", i, got)
		}
	}
	allCold := HotColdKeys(0.0)
	for i := 0; i < 10; i++ {
		if got := allCold(i); got == "/hot" {
			t.Errorf("HotColdKeys(0.0)(%d) = %q, want a cold key", i, got)
		}
	}
}
