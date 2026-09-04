package httpx

import (
	"time"

	"flashflow/internal/clock"
)

// RequestTimings captures the precise lifecycle timestamps across the request path.
// T0: Client begins request
// T1: Proxy receives request
// T2: Proxy dispatches upstream
// T3: Upstream response arrives at proxy
// T4: Proxy sends response to client
// T5: Client receives complete response
type RequestTimings struct {
	T0 clock.VirtualTime `json:"t0"` // Client start
	T1 clock.VirtualTime `json:"t1"` // Proxy receive
	T2 clock.VirtualTime `json:"t2"` // Proxy dispatch upstream
	T3 clock.VirtualTime `json:"t3"` // Upstream response arrive
	T4 clock.VirtualTime `json:"t4"` // Proxy send response
	T5 clock.VirtualTime `json:"t5"` // Client receive complete
}

// ProxyProcessingLatency returns the time spent in proxy logic (T4 - T1) - (T3 - T2).
func (t RequestTimings) ProxyProcessingLatency() time.Duration {
	totalProxy := t.T4.Sub(t.T1)
	upstream := t.T3.Sub(t.T2)
	if totalProxy >= upstream {
		return totalProxy - upstream
	}
	return 0
}

// UpstreamLatency returns the time from dispatch to upstream response arrival (T3 - T2).
func (t RequestTimings) UpstreamLatency() time.Duration {
	return t.T3.Sub(t.T2)
}

// EndToEndLatency returns the full client-perceived round-trip duration (T5 - T0).
func (t RequestTimings) EndToEndLatency() time.Duration {
	return t.T5.Sub(t.T0)
}
