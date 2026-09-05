package transport

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// TransportConfig defines connection pooling and lifecycle knobs.
type TransportConfig struct {
	Label               string        `json:"label"` // e.g. "proxy_upstream" or "edge_origin"
	DisableKeepAlives   bool          `json:"disable_keep_alives"`
	MaxIdleConns        int           `json:"max_idle_conns"`
	MaxIdleConnsPerHost int           `json:"max_idle_conns_per_host"`
	MaxConnsPerHost     int           `json:"max_conns_per_host"`
	IdleConnTimeout     time.Duration `json:"idle_conn_timeout"`
	DialTimeout         time.Duration `json:"dial_timeout"`
	TLSHandshakeTimeout time.Duration `json:"tls_handshake_timeout"`
	// ResponseHeaderTimeout bounds how long RoundTrip waits for the
	// upstream's response headers after fully writing the request. Without
	// it, a target that accepts a connection but never responds (a
	// realistic failure mode this project's own health checks otherwise
	// anticipate) hangs the request until the caller's own context
	// deadline, if any — unbounded when a caller (e.g. proxy.ReverseProxy)
	// calls RoundTrip directly rather than through an http.Client.Timeout.
	ResponseHeaderTimeout time.Duration `json:"response_header_timeout"`
}

// DefaultTransportConfig returns standard defaults for proxy transports.
func DefaultTransportConfig(label string) TransportConfig {
	return TransportConfig{
		Label:                 label,
		DisableKeepAlives:     false,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       0, // 0 = unlimited
		IdleConnTimeout:       90 * time.Second,
		DialTimeout:           5 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

// TransportStats contains an atomic snapshot of transport-level connection activity.
type TransportStats struct {
	Label             string `json:"label"`
	TotalDials        uint64 `json:"total_dials"`
	SuccessfulDials   uint64 `json:"successful_dials"`
	FailedDials       uint64 `json:"failed_dials"`
	ActiveConns       int64  `json:"active_conns"`
	ClosedConns       uint64 `json:"closed_conns"`
	RequestsCompleted uint64 `json:"requests_completed"`
	// RequestsPerConn represents the cumulative ratio of completed requests to successful dials (lifetime reuse metric).
	RequestsPerConn float64 `json:"requests_per_conn"`
}

// TrackedTransport wraps http.Transport with explicit atomic socket & connection instrumentation.
type TrackedTransport struct {
	label             string
	config            TransportConfig
	transport         *http.Transport
	totalDials        atomic.Uint64
	successfulDials   atomic.Uint64
	failedDials       atomic.Uint64
	activeConns       atomic.Int64
	closedConns       atomic.Uint64
	requestsCompleted atomic.Uint64
}

// trackedConn wraps net.Conn to detect when a TCP socket is physically closed.
type trackedConn struct {
	net.Conn
	parent *TrackedTransport
	closed atomic.Bool
}

func (c *trackedConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		c.parent.activeConns.Add(-1)
		c.parent.closedConns.Add(1)
	}
	return c.Conn.Close()
}

// NewTrackedTransport constructs a fully instrumented Transport with given configuration.
func NewTrackedTransport(cfg TransportConfig) *TrackedTransport {
	if cfg.Label == "" {
		cfg.Label = "transport"
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}
	if cfg.IdleConnTimeout == 0 {
		cfg.IdleConnTimeout = 90 * time.Second
	}
	if cfg.ResponseHeaderTimeout == 0 {
		cfg.ResponseHeaderTimeout = 30 * time.Second
	}

	tt := &TrackedTransport{
		label:  cfg.Label,
		config: cfg,
	}

	dialer := &net.Dialer{
		Timeout:   cfg.DialTimeout,
		KeepAlive: 30 * time.Second,
	}

	tt.transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			tt.totalDials.Add(1)
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				tt.failedDials.Add(1)
				return nil, err
			}
			tt.successfulDials.Add(1)
			tt.activeConns.Add(1)
			return &trackedConn{Conn: conn, parent: tt}, nil
		},
		DisableKeepAlives:     cfg.DisableKeepAlives,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:       cfg.MaxConnsPerHost,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
	}

	return tt
}

// RoundTrip executes an HTTP transaction and increments request completion counters.
func (tt *TrackedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := tt.transport.RoundTrip(req)
	if err == nil {
		tt.requestsCompleted.Add(1)
	}
	return resp, err
}

// HTTPTransport returns the underlying *http.Transport for use in http.Client.
func (tt *TrackedTransport) HTTPTransport() *http.Transport {
	return tt.transport
}

// HTTPClient returns an *http.Client configured with this instrumented transport.
func (tt *TrackedTransport) HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: tt,
		Timeout:   timeout,
	}
}

// CloseIdleConnections forces the pool to close all currently idle connections.
func (tt *TrackedTransport) CloseIdleConnections() {
	tt.transport.CloseIdleConnections()
}

// Snapshot returns a point-in-time copy of connection and socket metrics.
func (tt *TrackedTransport) Snapshot() TransportStats {
	succ := tt.successfulDials.Load()
	reqs := tt.requestsCompleted.Load()
	var rpc float64
	if succ > 0 {
		rpc = float64(reqs) / float64(succ)
	}

	return TransportStats{
		Label:             tt.label,
		TotalDials:        tt.totalDials.Load(),
		SuccessfulDials:   succ,
		FailedDials:       tt.failedDials.Load(),
		ActiveConns:       tt.activeConns.Load(),
		ClosedConns:       tt.closedConns.Load(),
		RequestsCompleted: reqs,
		RequestsPerConn:   rpc,
	}
}
