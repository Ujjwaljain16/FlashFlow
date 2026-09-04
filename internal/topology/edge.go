package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"flashflow/internal/httpx"
	"flashflow/internal/transport"
)

// EdgeConfig defines parameters for an Edge forwarder instance.
type EdgeConfig struct {
	Addr            string                    `json:"addr"`
	Instance        string                    `json:"instance"` // e.g. "edge-a"
	OriginURL       string                    `json:"origin_url"`
	DefaultDelay    time.Duration             `json:"default_delay"`
	TransportConfig transport.TransportConfig `json:"transport_config"`
}

// EdgeServer is a thin forwarding service with explicit instance identity.
type EdgeServer struct {
	mu              sync.RWMutex
	config          EdgeConfig
	server          *http.Server
	listener        net.Listener
	addrPort        string
	originURL       *url.URL
	trackedTrans    *transport.TrackedTransport
	httpClient      *http.Client
	artificialDelay time.Duration
}

// NewEdgeServer constructs an Edge forwarding server.
func NewEdgeServer(cfg EdgeConfig) (*EdgeServer, error) {
	if cfg.Instance == "" {
		cfg.Instance = "edge-unknown"
	}
	if cfg.OriginURL == "" {
		return nil, fmt.Errorf("edge %s requires non-empty origin_url", cfg.Instance)
	}

	u, err := url.Parse(cfg.OriginURL)
	if err != nil {
		return nil, fmt.Errorf("invalid origin_url %q: %w", cfg.OriginURL, err)
	}

	tCfg := cfg.TransportConfig
	if tCfg.Label == "" {
		tCfg.Label = fmt.Sprintf("edge_origin_%s", cfg.Instance)
	}
	tt := transport.NewTrackedTransport(tCfg)

	return &EdgeServer{
		config:          cfg,
		originURL:       u,
		trackedTrans:    tt,
		httpClient:      tt.HTTPClient(10 * time.Second),
		artificialDelay: cfg.DefaultDelay,
	}, nil
}

// SetArtificialDelay configures application processing delay on this edge.
func (e *EdgeServer) SetArtificialDelay(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.artificialDelay = d
}

// TransportStats returns the connection metrics between this Edge and Origin.
func (e *EdgeServer) TransportStats() transport.TransportStats {
	return e.trackedTrans.Snapshot()
}

// Handler returns the HTTP router for the edge server.
func (e *EdgeServer) Handler() http.Handler {
	mux := http.NewServeMux()

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":   "healthy",
			"service":  "edge",
			"instance": e.config.Instance,
			"origin":   e.originURL.String(),
		})
	})

	// Forwarding handler for all application requests
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		reqID := httpx.ExtractOrGenerateRequestID(r)
		w.Header().Set(httpx.HeaderRequestID, reqID)
		w.Header().Set(httpx.HeaderEdgeID, e.config.Instance)

		// 1. Artificial application delay (e.g. Experiment 002-D)
		e.mu.RLock()
		delay := e.artificialDelay
		e.mu.RUnlock()

		if delayParam := r.URL.Query().Get("edge_delay_ms"); delayParam != "" {
			if ms, err := strconv.Atoi(delayParam); err == nil && ms > 0 {
				delay = time.Duration(ms) * time.Millisecond
			}
		}
		if delayHeader := r.Header.Get("X-Edge-Delay-Ms"); delayHeader != "" {
			if ms, err := strconv.Atoi(delayHeader); err == nil && ms > 0 {
				delay = time.Duration(ms) * time.Millisecond
			}
		}

		if delay > 0 {
			time.Sleep(delay)
		}

		// 2. Build upstream request to origin
		outURL := *e.originURL
		outURL.Path = r.URL.Path
		outURL.RawQuery = r.URL.RawQuery

		outReq, err := http.NewRequestWithContext(r.Context(), r.Method, outURL.String(), r.Body)
		if err != nil {
			_ = httpx.WriteJSON(w, http.StatusInternalServerError, httpx.ErrorResponse{
				Error:     fmt.Sprintf("failed to construct origin request: %v", err),
				Code:      http.StatusInternalServerError,
				RequestID: reqID,
			})
			return
		}

		// Copy headers (stripping hop-by-hop headers per RFC 7230)
		httpx.CopyEndToEndHeaders(outReq.Header, r.Header)
		outReq.Header.Set(httpx.HeaderRequestID, reqID)
		outReq.Header.Set(httpx.HeaderEdgeID, e.config.Instance)

		// 3. Dispatch to origin
		resp, err := e.httpClient.Do(outReq)
		if err != nil {
			_ = httpx.WriteJSON(w, http.StatusBadGateway, httpx.ErrorResponse{
				Error:     fmt.Sprintf("origin unreachable: %v", err),
				Code:      http.StatusBadGateway,
				RequestID: reqID,
			})
			return
		}
		defer resp.Body.Close()

		// 4. Stream response headers and body back
		httpx.CopyEndToEndHeaders(w.Header(), resp.Header)
		w.Header().Set(httpx.HeaderRequestID, reqID)
		w.Header().Set(httpx.HeaderEdgeID, e.config.Instance)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})

	return mux
}

// Start begins listening and serving edge traffic.
func (e *EdgeServer) Start() error {
	addr := e.config.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("edge %s failed to listen on %s: %w", e.config.Instance, addr, err)
	}

	e.listener = ln
	e.addrPort = ln.Addr().String()
	e.server = &http.Server{
		Handler: e.Handler(),
	}

	go func() {
		_ = e.server.Serve(ln)
	}()

	return nil
}

// AddrPort returns the bound host:port.
func (e *EdgeServer) AddrPort() string {
	return e.addrPort
}

// URL returns the HTTP URL for this edge.
func (e *EdgeServer) URL() string {
	return fmt.Sprintf("http://%s", e.addrPort)
}

// Stop gracefully terminates the edge server and closes idle origin connections.
func (e *EdgeServer) Stop(ctx context.Context) error {
	e.trackedTrans.CloseIdleConnections()
	if e.server != nil {
		return e.server.Shutdown(ctx)
	}
	return nil
}
