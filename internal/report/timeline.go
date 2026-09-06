package report

import (
	"flashflow/internal/backlog"
	"flashflow/internal/replay"
)

// SeriesPoint is one sample in a bucketed time series -- the shared
// shape the dashboard's event-timeline view renders for both a
// target's own queue depth and the scenario's overall traffic rate.
type SeriesPoint struct {
	TimeMs float64 `json:"time_ms"`
	Value  float64 `json:"value"`
}

// DepthSeries samples target's own queue-depth Timeline at `buckets`
// evenly-spaced points across [0, horizonMs] -- the same bucketing
// approach cmd/experiment-016-flagship's own `sparkline` helper already
// established (bucket midpoints, not bucket starts, so a depth change
// exactly at a bucket boundary doesn't ambiguously belong to either
// side), generalized here to return plain data a caller can render
// however it likes, rather than a pre-rendered ASCII string.
func DepthSeries(wr *replay.WorldResult, target string, buckets int, horizonMs float64) []SeriesPoint {
	if buckets <= 0 || horizonMs <= 0 {
		return nil
	}
	tl := backlog.BuildTimeline(wr.Records, wr.Completions, target)
	points := make([]SeriesPoint, buckets)
	step := horizonMs / float64(buckets)
	for i := 0; i < buckets; i++ {
		t := (float64(i) + 0.5) * step
		points[i] = SeriesPoint{TimeMs: t, Value: float64(tl.DepthAt(t))}
	}
	return points
}

// TrafficSeries counts dispatches to ANY target within each of
// `buckets` evenly-spaced windows across [0, horizonMs] -- the overall
// offered-load shape (e.g. a FlashCrowd's rise and fall), independent
// of which target ends up receiving the load.
func TrafficSeries(wr *replay.WorldResult, buckets int, horizonMs float64) []SeriesPoint {
	if buckets <= 0 || horizonMs <= 0 {
		return nil
	}
	counts := make([]int, buckets)
	step := horizonMs / float64(buckets)
	for _, r := range wr.Records {
		if r.VirtualTimeMs < 0 || r.VirtualTimeMs >= horizonMs {
			continue
		}
		idx := int(r.VirtualTimeMs / step)
		if idx >= buckets {
			idx = buckets - 1
		}
		counts[idx]++
	}
	points := make([]SeriesPoint, buckets)
	for i, c := range counts {
		points[i] = SeriesPoint{TimeMs: (float64(i) + 0.5) * step, Value: float64(c)}
	}
	return points
}
