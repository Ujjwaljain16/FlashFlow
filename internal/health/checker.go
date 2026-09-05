package health

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/transport"
)

// Checker periodically probes upstream target health endpoints.
type Checker struct {
	mu        sync.Mutex
	registry  *Registry
	clock     clock.Clock
	client    *http.Client
	transport *transport.TrackedTransport
	interval  time.Duration
	timeout   time.Duration
	path      string
	targets   []string
	stopCh    chan struct{}
	running   bool
}

// CheckerConfig configures the background prober.
type CheckerConfig struct {
	Interval time.Duration `json:"interval"`
	Timeout  time.Duration `json:"timeout"`
	Path     string        `json:"path"` // default "/health"
}

// DefaultCheckerConfig returns reasonable background prober defaults.
func DefaultCheckerConfig() CheckerConfig {
	return CheckerConfig{
		Interval: 500 * time.Millisecond,
		Timeout:  200 * time.Millisecond,
		Path:     "/health",
	}
}

// NewChecker creates a background health checker.
func NewChecker(reg *Registry, clk clock.Clock, cfg CheckerConfig, targets []string) *Checker {
	if cfg.Interval <= 0 {
		cfg.Interval = 500 * time.Millisecond
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 200 * time.Millisecond
	}
	if cfg.Path == "" {
		cfg.Path = "/health"
	}
	if clk == nil {
		clk = clock.NewWallClock()
	}

	tCfg := transport.DefaultTransportConfig("health_prober")
	tCfg.DialTimeout = cfg.Timeout
	tt := transport.NewTrackedTransport(tCfg)

	c := &Checker{
		registry:  reg,
		clock:     clk,
		transport: tt,
		client:    tt.HTTPClient(cfg.Timeout),
		interval:  cfg.Interval,
		timeout:   cfg.Timeout,
		path:      cfg.Path,
		targets:   targets,
		stopCh:    make(chan struct{}),
	}

	for _, t := range targets {
		reg.RegisterTarget(t)
	}

	return c
}

// Start launches the background health checking loop.
func (c *Checker) Start() {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	stopCh := make(chan struct{})
	c.stopCh = stopCh
	c.mu.Unlock()

	// stopCh is captured here, not read from c.stopCh inside runLoop --
	// a Stop() followed immediately by another Start() reassigns
	// c.stopCh to a new, open channel, and a goroutine reading the
	// field directly would never observe the close it was actually
	// told to wait for, leaking a second, permanently-running probe
	// loop. Passing the channel as a parameter makes this goroutine's
	// stop signal immutable for its own lifetime.
	go c.runLoop(stopCh)
}

// Stop gracefully terminates the prober loop.
func (c *Checker) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return
	}
	c.running = false
	close(c.stopCh)
}

// ProbeOnce executes a single synchronous health check pass across all targets.
func (c *Checker) ProbeOnce(ctx context.Context) {
	var wg sync.WaitGroup
	for _, target := range c.targets {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			success := c.probeTarget(ctx, t)
			c.registry.RecordProbeResult(t, success)
		}(target)
	}
	wg.Wait()
}

func (c *Checker) probeTarget(ctx context.Context, target string) bool {
	probeURL := target
	// Parse target URL or host
	u, err := url.Parse(target)
	if err == nil && u.Scheme != "" {
		u.Path = c.path
		probeURL = u.String()
	} else {
		probeURL = fmt.Sprintf("http://%s%s", target, c.path)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return false
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func (c *Checker) runLoop(stop chan struct{}) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Initial probe immediately
	c.ProbeOnce(context.Background())

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
			c.ProbeOnce(ctx)
			cancel()
		}
	}
}
