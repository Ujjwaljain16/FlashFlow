package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"flashflow/internal/httpx"
)

// OriginConfig configures the Origin server.
type OriginConfig struct {
	Addr         string        `json:"addr"`
	Instance     string        `json:"instance"`
	DefaultDelay time.Duration `json:"default_delay"`
}

// OriginServer is a real HTTP service that acts as the source of truth backend.
type OriginServer struct {
	mu              sync.RWMutex
	config          OriginConfig
	server          *http.Server
	listener        net.Listener
	addrPort        string
	artificialDelay time.Duration

	activeRequests  atomic.Int64
	peakConcurrency atomic.Int64
}

// ConcurrencyStats reports how many requests Origin is (or, at peak, was)
// handling at once — the "how big is the burst" question a cache stampede
// experiment needs answered, and something no earlier stage needed to
// measure since nothing before Stage 4 could cause a synchronized burst of
// upstream requests in the first place.
type ConcurrencyStats struct {
	Active int64 `json:"active"`
	Peak   int64 `json:"peak"`
}

// ConcurrencyStats returns the current in-flight count and the all-time
// (since this OriginServer was created) peak concurrent request count.
func (s *OriginServer) ConcurrencyStats() ConcurrencyStats {
	return ConcurrencyStats{Active: s.activeRequests.Load(), Peak: s.peakConcurrency.Load()}
}

// NewOriginServer creates a new Origin server instance.
func NewOriginServer(cfg OriginConfig) *OriginServer {
	if cfg.Instance == "" {
		cfg.Instance = "origin-1"
	}
	return &OriginServer{
		config:          cfg,
		artificialDelay: cfg.DefaultDelay,
	}
}

// SetArtificialDelay dynamically sets processing delay for testing.
func (s *OriginServer) SetArtificialDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artificialDelay = d
}

// Handler returns the HTTP handler for the origin service.
func (s *OriginServer) Handler() http.Handler {
	mux := http.NewServeMux()

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":   "healthy",
			"service":  "origin",
			"instance": s.config.Instance,
		})
	})

	// Data and fallback endpoints
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		n := s.activeRequests.Add(1)
		defer s.activeRequests.Add(-1)
		for {
			peak := s.peakConcurrency.Load()
			if n <= peak || s.peakConcurrency.CompareAndSwap(peak, n) {
				break
			}
		}

		reqID := httpx.ExtractOrGenerateRequestID(r)
		w.Header().Set(httpx.HeaderRequestID, reqID)

		// Check for simulated application processing delay
		s.mu.RLock()
		delay := s.artificialDelay
		s.mu.RUnlock()

		if delayParam := r.URL.Query().Get("delay_ms"); delayParam != "" {
			if ms, err := strconv.Atoi(delayParam); err == nil && ms > 0 {
				delay = time.Duration(ms) * time.Millisecond
			}
		}
		if delayHeader := r.Header.Get("X-Artificial-Delay-Ms"); delayHeader != "" {
			if ms, err := strconv.Atoi(delayHeader); err == nil && ms > 0 {
				delay = time.Duration(ms) * time.Millisecond
			}
		}

		if delay > 0 {
			time.Sleep(delay)
		}

		// Check for status code override
		status := http.StatusOK
		if scParam := r.URL.Query().Get("status_code"); scParam != "" {
			if sc, err := strconv.Atoi(scParam); err == nil && sc >= 200 && sc <= 599 {
				status = sc
			}
		}
		if scHeader := r.Header.Get("X-Override-Status"); scHeader != "" {
			if sc, err := strconv.Atoi(scHeader); err == nil && sc >= 200 && sc <= 599 {
				status = sc
			}
		}

		bodyBytes, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		resp := httpx.OriginResponse{
			Service:     "origin",
			Instance:    s.config.Instance,
			RequestID:   reqID,
			Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
			PayloadSize: len(bodyBytes),
		}

		_ = httpx.WriteJSON(w, status, resp)
	})

	return mux
}

// Start listens and starts serving requests.
func (s *OriginServer) Start() error {
	addr := s.config.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("origin failed to listen on %s: %w", addr, err)
	}

	s.listener = ln
	s.addrPort = ln.Addr().String()
	s.server = &http.Server{
		Handler: s.Handler(),
	}

	go func() {
		_ = s.server.Serve(ln)
	}()

	return nil
}

// AddrPort returns the bound host:port address.
func (s *OriginServer) AddrPort() string {
	return s.addrPort
}

// URL returns the base HTTP URL for this origin server.
func (s *OriginServer) URL() string {
	return fmt.Sprintf("http://%s", s.addrPort)
}

// Stop gracefully shuts down the server.
func (s *OriginServer) Stop(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}
