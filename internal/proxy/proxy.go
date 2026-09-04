package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"

	"flashflow/internal/clock"
	"flashflow/internal/health"
	"flashflow/internal/httpx"
	"flashflow/internal/transport"
)

// RequestRecord stores an internal execution telemetry event for experiments.
type RequestRecord struct {
	RequestID          string               `json:"request_id"`
	Target             string               `json:"target"`
	Method             string               `json:"method"`
	Path               string               `json:"path"`
	StatusCode         int                  `json:"status_code"`
	Timings            httpx.RequestTimings `json:"timings"`
	ProxyDurationNs    int64                `json:"proxy_duration_ns"`
	UpstreamDurationNs int64                `json:"upstream_duration_ns"`
	Error              string               `json:"error,omitempty"`
}

// TelemetryCallback is invoked when an upstream transaction completes.
type TelemetryCallback func(record RequestRecord)

// Config configures the FlashFlow reverse proxy.
type Config struct {
	Addr               string                    `json:"addr"`
	Targets            []string                  `json:"targets"`
	TransportConfig    transport.TransportConfig `json:"transport_config"`
	HealthConfig       health.Config             `json:"health_config"`
	ProberConfig       health.CheckerConfig      `json:"prober_config"`
	ExposeDebugHeaders bool                      `json:"expose_debug_headers"`
	// EWMAAlpha is the always-present LatencyTracker's smoothing factor;
	// 0 uses defaultEWMAAlpha (see NewLatencyTracker). Harmless for
	// selectors that don't read LatencyTracker.
	EWMAAlpha float64 `json:"ewma_alpha"`
}

// ReverseProxy is a custom, observable HTTP reverse proxy.
type ReverseProxy struct {
	mu             sync.RWMutex
	config         Config
	clock          clock.Clock
	registry       *health.Registry
	checker        *health.Checker
	selector       TargetSelector
	loadTracker    *LoadTracker
	latencyTracker *LatencyTracker
	transport      *transport.TrackedTransport
	server         *http.Server
	listener       net.Listener
	addrPort       string
	onRecord       TelemetryCallback
}

// NewReverseProxy creates a fully configured ReverseProxy instance.
func NewReverseProxy(cfg Config, clk clock.Clock, sel TargetSelector) *ReverseProxy {
	if clk == nil {
		clk = clock.NewWallClock()
	}
	if sel == nil {
		sel = NewRoundRobinSelector()
	}

	reg := health.NewRegistry(clk, cfg.HealthConfig)
	for _, t := range cfg.Targets {
		reg.RegisterTarget(t)
	}

	chk := health.NewChecker(reg, clk, cfg.ProberConfig, cfg.Targets)

	tCfg := cfg.TransportConfig
	if tCfg.Label == "" {
		tCfg.Label = "proxy_upstream"
	}
	tt := transport.NewTrackedTransport(tCfg)

	return &ReverseProxy{
		config:         cfg,
		clock:          clk,
		registry:       reg,
		checker:        chk,
		selector:       sel,
		loadTracker:    NewLoadTracker(),
		latencyTracker: NewLatencyTracker(cfg.EWMAAlpha),
		transport:      tt,
	}
}

// SetSelector dynamically swaps the target selector policy.
func (p *ReverseProxy) SetSelector(sel TargetSelector) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.selector = sel
}

// SetTelemetryCallback registers a listener for completed request timing records.
func (p *ReverseProxy) SetTelemetryCallback(cb TelemetryCallback) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onRecord = cb
}

// TransportStats returns the connection metrics for the proxy-to-upstream pool.
func (p *ReverseProxy) TransportStats() transport.TransportStats {
	return p.transport.Snapshot()
}

// Registry returns the active health registry.
func (p *ReverseProxy) Registry() *health.Registry {
	return p.registry
}

// LoadTracker exposes the tracker ServeHTTP updates, for constructing a
// state-aware TargetSelector (e.g. LeastConnectionsSelector) to attach via
// SetSelector — must be the same instance the proxy writes to.
func (p *ReverseProxy) LoadTracker() *LoadTracker {
	return p.loadTracker
}

// LatencyTracker exposes the tracker ServeHTTP updates, for constructing
// a latency-aware TargetSelector (e.g. EWMASelector) to attach via
// SetSelector — must be the same instance the proxy writes to.
func (p *ReverseProxy) LatencyTracker() *LatencyTracker {
	return p.latencyTracker
}

// ServeHTTP handles incoming client requests, executes forwarding, and measures latencies.
func (p *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t1 := p.clock.Now()
	reqID := httpx.ExtractOrGenerateRequestID(r)
	w.Header().Set(httpx.HeaderRequestID, reqID)

	// Filter available targets
	p.mu.RLock()
	selector := p.selector
	cb := p.onRecord
	p.mu.RUnlock()

	var available []string
	for _, t := range p.config.Targets {
		if p.registry.IsAvailable(t) {
			available = append(available, t)
		}
	}

	target, err := selector.SelectTarget(r, available)
	if err != nil {
		t4 := p.clock.Now()
		_ = httpx.WriteJSON(w, http.StatusServiceUnavailable, httpx.ErrorResponse{
			Error:     "no healthy edge targets available",
			Code:      http.StatusServiceUnavailable,
			RequestID: reqID,
		})
		if cb != nil {
			cb(RequestRecord{
				RequestID:  reqID,
				Target:     "",
				Method:     r.Method,
				Path:       r.URL.Path,
				StatusCode: http.StatusServiceUnavailable,
				Timings: httpx.RequestTimings{
					T1: t1, T4: t4,
				},
				ProxyDurationNs: t4.Sub(t1).Nanoseconds(),
				Error:           err.Error(),
			})
		}
		return
	}

	// One defer covers every remaining return path below (malformed
	// target, request construction failure, RoundTrip error, success) so
	// no error branch can forget to decrement.
	p.loadTracker.Increment(target)
	defer p.loadTracker.Decrement(target)

	// Safe target URL parsing
	targetURL, parseErr := url.Parse(target)
	if parseErr != nil || (targetURL.Host == "" && targetURL.Scheme == "") {
		targetURL, parseErr = url.Parse("http://" + target)
	}
	if parseErr != nil || targetURL.Host == "" {
		t4 := p.clock.Now()
		_ = httpx.WriteJSON(w, http.StatusBadGateway, httpx.ErrorResponse{
			Error:     fmt.Sprintf("malformed target URL %q: %v", target, parseErr),
			Code:      http.StatusBadGateway,
			RequestID: reqID,
		})
		if cb != nil {
			cb(RequestRecord{
				RequestID:       reqID,
				Target:          target,
				Method:          r.Method,
				Path:            r.URL.Path,
				StatusCode:      http.StatusBadGateway,
				Timings:         httpx.RequestTimings{T1: t1, T4: t4},
				ProxyDurationNs: t4.Sub(t1).Nanoseconds(),
				Error:           "malformed target URL",
			})
		}
		return
	}

	outURL := *targetURL
	outURL.Path = r.URL.Path
	outURL.RawQuery = r.URL.RawQuery

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, outURL.String(), r.Body)
	if err != nil {
		t4 := p.clock.Now()
		_ = httpx.WriteJSON(w, http.StatusInternalServerError, httpx.ErrorResponse{
			Error:     fmt.Sprintf("failed to construct upstream request: %v", err),
			Code:      http.StatusInternalServerError,
			RequestID: reqID,
		})
		if cb != nil {
			cb(RequestRecord{
				RequestID:  reqID,
				Target:     target,
				Method:     r.Method,
				Path:       r.URL.Path,
				StatusCode: http.StatusInternalServerError,
				Timings: httpx.RequestTimings{
					T1: t1, T4: t4,
				},
				ProxyDurationNs: t4.Sub(t1).Nanoseconds(),
				Error:           err.Error(),
			})
		}
		return
	}

	// Copy only end-to-end headers (stripping hop-by-hop headers per RFC 7230)
	httpx.CopyEndToEndHeaders(outReq.Header, r.Header)
	outReq.Header.Set(httpx.HeaderRequestID, reqID)

	t2 := p.clock.Now()
	resp, err := p.transport.RoundTrip(outReq)
	t3 := p.clock.Now()

	if err != nil {
		t4 := p.clock.Now()
		p.registry.RecordAppResult(target, http.StatusBadGateway)
		_ = httpx.WriteJSON(w, http.StatusBadGateway, httpx.ErrorResponse{
			Error:     fmt.Sprintf("upstream %s unreachable: %v", target, err),
			Code:      http.StatusBadGateway,
			RequestID: reqID,
		})
		if cb != nil {
			cb(RequestRecord{
				RequestID:  reqID,
				Target:     target,
				Method:     r.Method,
				Path:       r.URL.Path,
				StatusCode: http.StatusBadGateway,
				Timings: httpx.RequestTimings{
					T1: t1, T2: t2, T3: t3, T4: t4,
				},
				ProxyDurationNs:    t4.Sub(t1).Nanoseconds(),
				UpstreamDurationNs: t3.Sub(t2).Nanoseconds(),
				Error:              err.Error(),
			})
		}
		return
	}
	defer resp.Body.Close()

	p.registry.RecordAppResult(target, resp.StatusCode)
	// Recorded regardless of resp.StatusCode — a non-2xx response still
	// measured real latency; only a RoundTrip error (handled above,
	// returns before this line) skips it. See LatencyTracker for why.
	p.latencyTracker.Observe(target, t3.Sub(t2))

	// Copy only end-to-end response headers back to downstream client
	httpx.CopyEndToEndHeaders(w.Header(), resp.Header)
	w.Header().Set(httpx.HeaderRequestID, reqID)

	if p.config.ExposeDebugHeaders {
		w.Header().Set(httpx.HeaderSelectedEdge, target)
		w.Header().Set(httpx.HeaderUpstreamLatency, fmt.Sprintf("%.3f", float64(t3.Sub(t2).Microseconds())/1000.0))
	}

	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	t4 := p.clock.Now()

	if cb != nil {
		cb(RequestRecord{
			RequestID:  reqID,
			Target:     target,
			Method:     r.Method,
			Path:       r.URL.Path,
			StatusCode: resp.StatusCode,
			Timings: httpx.RequestTimings{
				T1: t1, T2: t2, T3: t3, T4: t4,
			},
			ProxyDurationNs:    t4.Sub(t1).Nanoseconds(),
			UpstreamDurationNs: t3.Sub(t2).Nanoseconds(),
		})
	}
}

// Start launches the background prober and proxy HTTP server.
func (p *ReverseProxy) Start() error {
	addr := p.config.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("proxy failed to listen on %s: %w", addr, err)
	}

	p.listener = ln
	p.addrPort = ln.Addr().String()
	p.server = &http.Server{
		Handler: p,
	}

	p.checker.Start()

	go func() {
		_ = p.server.Serve(ln)
	}()

	return nil
}

// AddrPort returns the bound host:port string.
func (p *ReverseProxy) AddrPort() string {
	return p.addrPort
}

// URL returns the base HTTP URL for this proxy.
func (p *ReverseProxy) URL() string {
	return fmt.Sprintf("http://%s", p.addrPort)
}

// Stop gracefully stops the prober, closes connection pool, and shuts down server.
func (p *ReverseProxy) Stop(ctx context.Context) error {
	p.checker.Stop()
	p.transport.CloseIdleConnections()
	if p.server != nil {
		return p.server.Shutdown(ctx)
	}
	return nil
}
