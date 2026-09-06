package telemetry

import "testing"

func TestHistogram_EmptyReturnsZero(t *testing.T) {
	h := NewHistogram()
	if got := h.ValueAtPercentile(50); got != 0 {
		t.Errorf("ValueAtPercentile(50) on empty histogram = %d, want 0", got)
	}
	if h.Count() != 0 {
		t.Errorf("Count() = %d, want 0", h.Count())
	}
}

func TestHistogram_SingleValue_EveryPercentileNearThatValue(t *testing.T) {
	h := NewHistogram()
	h.Record(10_000_000) // 10ms
	for _, p := range []float64{0, 50, 99, 100} {
		got := h.ValueAtPercentile(p)
		// The bucket's upper edge is always >= the recorded value and
		// within the histogram's per-bucket relative-precision margin
		// (histogramNumBuckets buckets spanning 1µs-10s in log space
		// gives roughly a fraction-of-a-percent width per bucket).
		if got < 10_000_000 || got > 10_100_000 {
			t.Errorf("ValueAtPercentile(%v) = %d, want within [10_000_000, 10_100_000]", p, got)
		}
	}
}

func TestHistogram_HandComputedPercentiles(t *testing.T) {
	h := NewHistogram()
	// 100 observations at 1ms, 100 at 10ms, 100 at 100ms: p25 should
	// land in the 1ms bucket, p50 right at the 1ms/10ms boundary or
	// just into 10ms, p90 in the 100ms bucket.
	for i := 0; i < 100; i++ {
		h.Record(1_000_000)
	}
	for i := 0; i < 100; i++ {
		h.Record(10_000_000)
	}
	for i := 0; i < 100; i++ {
		h.Record(100_000_000)
	}

	p25 := h.ValueAtPercentile(25)
	if p25 < 1_000_000 || p25 > 1_010_000 {
		t.Errorf("p25 = %d, want within the ~1ms bucket", p25)
	}
	p90 := h.ValueAtPercentile(90)
	if p90 < 100_000_000 || p90 > 100_500_000 {
		t.Errorf("p90 = %d, want within the ~100ms bucket", p90)
	}
	if h.Count() != 300 {
		t.Errorf("Count() = %d, want 300", h.Count())
	}
}

func TestHistogram_Monotonic(t *testing.T) {
	h := NewHistogram()
	for _, v := range []int64{500_000, 2_000_000, 5_000_000, 50_000_000, 500_000_000} {
		h.Record(v)
	}
	prev := int64(0)
	for p := 0.0; p <= 100; p += 1 {
		got := h.ValueAtPercentile(p)
		if got < prev {
			t.Fatalf("ValueAtPercentile(%v) = %d is less than ValueAtPercentile(%v) = %d -- not monotonic", p, got, p-1, prev)
		}
		prev = got
	}
}

func TestHistogram_UnderflowAndOverflow(t *testing.T) {
	h := NewHistogram()
	h.Record(1) // 1ns, far below histogramMinNs -- underflow
	if got := h.ValueAtPercentile(50); got != histogramMinNs {
		t.Errorf("underflow-only histogram: ValueAtPercentile(50) = %d, want %d", got, histogramMinNs)
	}

	h2 := NewHistogram()
	h2.Record(1_000_000_000_000) // 1000s, far above histogramMaxNs -- overflow
	if got := h2.ValueAtPercentile(50); got != histogramMaxNs {
		t.Errorf("overflow-only histogram: ValueAtPercentile(50) = %d, want %d", got, histogramMaxNs)
	}
}

func TestHistogram_NegativeLatencyGoesToUnderflow(t *testing.T) {
	h := NewHistogram()
	h.Record(-5)
	if h.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", h.Count())
	}
	if got := h.ValueAtPercentile(50); got != histogramMinNs {
		t.Errorf("ValueAtPercentile(50) = %d, want %d (underflow bucket)", got, histogramMinNs)
	}
}

func TestHistogram_PercentileClampedToValidRange(t *testing.T) {
	h := NewHistogram()
	h.Record(10_000_000)
	if h.ValueAtPercentile(-10) != h.ValueAtPercentile(0) {
		t.Error("expected a negative percentile to clamp to 0")
	}
	if h.ValueAtPercentile(150) != h.ValueAtPercentile(100) {
		t.Error("expected a percentile above 100 to clamp to 100")
	}
}
