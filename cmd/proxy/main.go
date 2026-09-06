package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/health"
	"flashflow/internal/proxy"
	"flashflow/internal/telemetry"
	"flashflow/internal/transport"
)

// buildSelector constructs the named routing policy wired to pxy's own
// shared LoadTracker/LatencyTracker (the same instances ServeHTTP already
// updates), the pattern every cmd/experiment-* binary that exercises a
// non-default policy against a real ReverseProxy already uses (e.g.
// cmd/experiment-003c/003d/003e). Before this flag existed, this shipped
// binary could only ever run Round Robin despite every one of these
// selectors being the identical, shared code the virtual engine and every
// experiment already exercise (F-30).
func buildSelector(name string, pxy *proxy.ReverseProxy, targets []string) (proxy.TargetSelector, error) {
	switch name {
	case "round-robin":
		return proxy.NewRoundRobinSelector(), nil
	case "weighted-round-robin":
		// No per-target capacity data is available from this minimal
		// CLI (unlike an experiment scenario's known ServiceTime) --
		// equal weights here are a documented starting point, not a
		// claim of matching each target's real capacity.
		weights := make(proxy.TargetWeights, len(targets))
		for _, t := range targets {
			weights[t] = 1
		}
		return proxy.NewWeightedRoundRobinSelector(weights), nil
	case "least-connections":
		return proxy.NewLeastConnectionsSelector(pxy.LoadTracker()), nil
	case "ewma":
		return proxy.NewEWMASelector(pxy.LatencyTracker()), nil
	case "p2c-load":
		return proxy.NewP2CSelector(proxy.ScorerFromLoad(pxy.LoadTracker()), rand.New(rand.NewSource(time.Now().UnixNano()))), nil
	case "adaptive":
		return proxy.NewAdaptiveSelector(pxy.LoadTracker(), pxy.LatencyTracker(), nil, nil, nil, proxy.DefaultAdaptiveConfig()), nil
	default:
		return nil, fmt.Errorf("unknown -policy %q (want one of: round-robin, weighted-round-robin, least-connections, ewma, p2c-load, adaptive)", name)
	}
}

func main() {
	addr := flag.String("addr", ":8080", "Proxy listen address")
	targetsList := flag.String("targets", "http://127.0.0.1:8001,http://127.0.0.1:8002,http://127.0.0.1:8003", "Comma-separated upstream targets")
	checkIntervalMs := flag.Int("check-interval-ms", 500, "Health check interval in milliseconds")
	checkTimeoutMs := flag.Int("check-timeout-ms", 200, "Health check timeout in milliseconds")
	debugHeaders := flag.Bool("debug-headers", true, "Expose debug headers in response")
	policyName := flag.String("policy", "round-robin", "Routing policy: round-robin, weighted-round-robin, least-connections, ewma, p2c-load, adaptive")
	metricsAddr := flag.String("metrics-addr", "", "If set, serve Prometheus-format metrics at /metrics on this address (e.g. :9090) -- a separate listener from -addr, not a route on the proxy's own mux, so a scraper hitting it can never be confused with real proxied traffic. Empty (default) disables metrics entirely.")
	flag.Parse()

	var targets []string
	for _, t := range strings.Split(*targetsList, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			targets = append(targets, t)
		}
	}

	clk := clock.NewWallClock()
	hCfg := health.DefaultConfig()
	chkCfg := health.CheckerConfig{
		Interval: time.Duration(*checkIntervalMs) * time.Millisecond,
		Timeout:  time.Duration(*checkTimeoutMs) * time.Millisecond,
		Path:     "/health",
	}
	tCfg := transport.DefaultTransportConfig("proxy_upstream")

	cfg := proxy.Config{
		Addr:               *addr,
		Targets:            targets,
		TransportConfig:    tCfg,
		HealthConfig:       hCfg,
		ProberConfig:       chkCfg,
		ExposeDebugHeaders: *debugHeaders,
	}

	pxy := proxy.NewReverseProxy(cfg, clk, proxy.NewRoundRobinSelector())
	sel, err := buildSelector(*policyName, pxy, targets)
	if err != nil {
		log.Fatalf("%v", err)
	}
	pxy.SetSelector(sel)

	if err := pxy.Start(); err != nil {
		log.Fatalf("failed to start proxy: %v", err)
	}

	log.Printf("[FlashFlow Proxy] Listening on %s routing to [%s] using policy %q", pxy.AddrPort(), strings.Join(targets, ", "), *policyName)

	if *metricsAddr != "" {
		hist := telemetry.AttachHistogram(pxy)
		metricsMux := http.NewServeMux()
		metricsMux.Handle("/metrics", telemetry.ProxyHandler(pxy, hist))
		metricsServer := &http.Server{Addr: *metricsAddr, Handler: metricsMux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("[FlashFlow Proxy] metrics server exited unexpectedly: %v", err)
			}
		}()
		defer metricsServer.Close()
		log.Printf("[FlashFlow Proxy] Serving Prometheus metrics at http://%s/metrics", *metricsAddr)
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			st := pxy.TransportStats()
			hMap := pxy.Registry().Snapshot()
			log.Printf("[FlashFlow Proxy] Upstream Transport: dials=%d active=%d reqs=%d reqs/conn=%.2f",
				st.SuccessfulDials, st.ActiveConns, st.RequestsCompleted, st.RequestsPerConn)
			for t, h := range hMap {
				log.Printf("  Target %-30s -> State: %-10s (passes=%d fails=%d)", t, h.State, h.ConsecutivePasses, h.ConsecutiveFails)
			}
		case <-sigCh:
			log.Println("[FlashFlow Proxy] Shutting down proxy...")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := pxy.Stop(ctx); err != nil {
				log.Printf("error during proxy shutdown: %v", err)
			}
			log.Println("[FlashFlow Proxy] Stopped cleanly.")
			return
		}
	}
}
