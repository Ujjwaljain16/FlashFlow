package telemetry

import (
	"net/http"

	"flashflow/internal/proxy"
)

// ProxyHandler returns an http.HandlerFunc that snapshots p and writes
// it in Prometheus text-exposition format on every request -- the
// standard "scrape this URL" contract a Prometheus-compatible collector
// expects. hist may be nil (no histogram section is written in that
// case); pass the value returned by AttachHistogram to include one.
func ProxyHandler(p *proxy.ReverseProxy, hist *Histogram) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := SnapshotFromProxy(p)
		m.Histogram = hist
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_ = WriteText(w, m)
	}
}
