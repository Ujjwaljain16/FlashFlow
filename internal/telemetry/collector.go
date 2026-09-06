package telemetry

import "flashflow/internal/proxy"

// AttachHistogram registers p's TelemetryCallback to feed every
// completed request's upstream duration into a new Histogram -- the one
// piece of Metrics that genuinely requires live accumulation over time
// rather than a pure read of an already-existing Snapshot() call (see
// metrics.go's own doc comment on why SnapshotFromProxy itself cannot
// populate this field). Call this once per ReverseProxy, before serving
// traffic; assign the returned Histogram to a later SnapshotFromProxy
// result's Histogram field before passing it to WriteText.
//
// p.SetTelemetryCallback holds exactly one callback (overwrite, not
// append) -- safe to call here since no other part of this project
// currently registers one, but a caller that already uses
// SetTelemetryCallback for its own purpose must not also call
// AttachHistogram without composing the two callbacks itself.
func AttachHistogram(p *proxy.ReverseProxy) *Histogram {
	h := NewHistogram()
	p.SetTelemetryCallback(func(r proxy.RequestRecord) {
		h.Record(r.UpstreamDurationNs)
	})
	return h
}
