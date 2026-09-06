// Package telemetry hand-rolls the PRD's HdrHistogram-style latency
// histogram and a Prometheus text-exposition writer (§8.9, TRD §13),
// missing per the Stage 8 audit's F-23 -- deliberately built in place
// rather than adding the hdrhistogram-go/prometheus/client_golang
// dependencies, matching this project's existing pattern of hand-
// rolling statistical/mathematical code (see internal/statistics)
// rather than importing it. This is explicitly NOT a replacement for
// internal/statistics.Percentile, whose exact, unbounded, non-lossy
// percentile computation is what this project's own scientific claims
// are built on -- Histogram exists for cheap, live, low-overhead
// metrics EXPORT (the kind a running system exposes continuously),
// where a bounded-memory, logarithmic-bucket approximation is the
// right tradeoff and exactness is not the point.
package telemetry

import (
	"math"
	"sync"
)

// Histogram bucket range: covers roughly 1 microsecond to 10 seconds,
// wide enough for every latency this project's real or virtual engine
// has ever produced (sub-millisecond routing decisions through
// multi-second queueing-saturation latencies in 008-G's load sweep)
// without needing a dynamically-resizable range.
const (
	histogramMinNs      = 1_000          // 1µs
	histogramMaxNs      = 10_000_000_000 // 10s
	histogramNumBuckets = 2000
)

// Histogram is a fixed-range, logarithmic-bucket latency histogram:
// bucket boundaries are evenly spaced in LOG space between
// histogramMinNs and histogramMaxNs, giving roughly constant relative
// (not absolute) precision across the whole range -- the same design
// principle HdrHistogram itself uses, simplified here to one bucket
// array rather than HdrHistogram's sub-bucket/main-bucket two-level
// scheme, which is more precision than a live metrics export actually
// needs. Two extra buckets catch values outside the configured range
// entirely (index 0 for underflow, the last index for overflow) rather
// than silently clamping them into the nearest real bucket, which
// would corrupt that bucket's percentile meaning.
type Histogram struct {
	mu     sync.Mutex
	counts [histogramNumBuckets + 2]uint64
	total  uint64
}

// NewHistogram creates an empty Histogram.
func NewHistogram() *Histogram {
	return &Histogram{}
}

var (
	logMin = math.Log(float64(histogramMinNs))
	logMax = math.Log(float64(histogramMaxNs))
)

// Record adds one observation. latencyNs must be a non-negative
// nanosecond duration; a negative value (which should never occur for
// a real elapsed-time measurement) is recorded into the underflow
// bucket rather than panicking or being silently dropped.
func (h *Histogram) Record(latencyNs int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.total++
	switch {
	case latencyNs < histogramMinNs:
		h.counts[0]++
	case latencyNs >= histogramMaxNs:
		h.counts[len(h.counts)-1]++
	default:
		frac := (math.Log(float64(latencyNs)) - logMin) / (logMax - logMin)
		idx := 1 + int(frac*float64(histogramNumBuckets))
		if idx > histogramNumBuckets {
			idx = histogramNumBuckets
		}
		h.counts[idx]++
	}
}

// ValueAtPercentile returns an approximate nanosecond latency value at
// percentile p (0-100): the upper edge of the bucket containing the
// p-th smallest observation, matching HdrHistogram's own convention of
// reporting a bucket boundary rather than interpolating within it (an
// interpolated value would imply a precision the bucketing doesn't
// actually have). Returns 0 for an empty histogram -- there is no
// observation to report a percentile of.
func (h *Histogram) ValueAtPercentile(p float64) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.total == 0 {
		return 0
	}
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	target := uint64(math.Ceil(p / 100.0 * float64(h.total)))
	if target < 1 {
		target = 1
	}
	var cum uint64
	for i, c := range h.counts {
		cum += c
		if cum >= target {
			switch i {
			case 0:
				return histogramMinNs // underflow bucket: report the range floor, the best available bound
			case len(h.counts) - 1:
				return histogramMaxNs // overflow bucket: report the range ceiling
			default:
				upperLog := logMin + float64(i)/float64(histogramNumBuckets)*(logMax-logMin)
				return int64(math.Exp(upperLog))
			}
		}
	}
	return histogramMaxNs
}

// Count returns the total number of observations recorded.
func (h *Histogram) Count() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.total
}
