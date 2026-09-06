package telemetry

import (
	"flashflow/internal/proxy"
	"flashflow/internal/topology"
)

// Metrics is a point-in-time snapshot suitable for a Prometheus-style
// export -- deliberately a plain aggregation struct, not a new
// instrumentation layer: every field is populated by reading an
// ALREADY-EXISTING Snapshot()-shaped getter (Registry.Snapshot,
// LatencyTracker.Snapshot, cache.Cache.Snapshot, cache.Coalescer.
// Snapshot), never by adding a new counter this project didn't already
// maintain. Histogram is the one exception: it requires live
// accumulation over time that no existing Snapshot() call captures, so
// it is nil unless a Collector (collector.go) has been attached first.
type Metrics struct {
	RequestsTotal  map[string]uint64  `json:"requests_total"`
	LatencySeconds map[string]float64 `json:"latency_seconds"`
	CacheHits      uint64             `json:"cache_hits"`
	CacheMisses    uint64             `json:"cache_misses"`
	CacheFills     uint64             `json:"cache_fills"`
	CoalesceLeads  uint64             `json:"coalesce_leads"`
	CoalesceShared uint64             `json:"coalesce_shared"`
	HealthState    map[string]string  `json:"health_state"`
	Histogram      *Histogram         `json:"-"` // not JSON-serialized; consumed by WriteText's own histogram bucket export
}

// SnapshotFromProxy builds Metrics from p's existing per-target trackers
// and health registry -- a pure aggregation, no side effects, no new
// state added to ReverseProxy. Histogram is nil; attach one via
// AttachHistogram if live latency-distribution export is needed.
func SnapshotFromProxy(p *proxy.ReverseProxy) Metrics {
	health := p.Registry().Snapshot()
	requestsTotal := make(map[string]uint64, len(health))
	healthState := make(map[string]string, len(health))
	for target, h := range health {
		requestsTotal[target] = h.TotalAppRequests
		healthState[target] = string(h.State)
	}

	latencySeconds := make(map[string]float64)
	for target, d := range p.LatencyTracker().Snapshot() {
		latencySeconds[target] = d.Seconds()
	}

	return Metrics{
		RequestsTotal:  requestsTotal,
		LatencySeconds: latencySeconds,
		HealthState:    healthState,
	}
}

// SnapshotFromEdge builds Metrics from e's existing cache/coalesce/
// transport counters. Unlike a ReverseProxy (which routes across many
// named targets), one EdgeServer forwards to a single Origin, so
// RequestsTotal/LatencySeconds carry exactly one entry, keyed by the
// edge's own configured instance name -- there is no per-target
// breakdown to report because there are no routing targets at this
// layer. LatencySeconds is intentionally left empty: EdgeServer's
// existing public surface (TransportStats/CacheStats/CoalesceStats)
// has no mean-or-percentile latency figure to aggregate without adding
// new instrumentation this function's own "pure aggregation" contract
// doesn't allow itself.
func SnapshotFromEdge(e *topology.EdgeServer) Metrics {
	ts := e.TransportStats()
	cs := e.CacheStats()
	co := e.CoalesceStats()

	return Metrics{
		RequestsTotal:  map[string]uint64{e.Instance(): ts.RequestsCompleted},
		LatencySeconds: map[string]float64{},
		CacheHits:      cs.Hits,
		CacheMisses:    cs.Misses,
		CacheFills:     cs.Fills,
		CoalesceLeads:  co.Leads,
		CoalesceShared: co.Shared,
		HealthState:    map[string]string{},
	}
}
