package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/health"
	"flashflow/internal/proxy"
	"flashflow/internal/transport"
)

func main() {
	addr := flag.String("addr", ":8080", "Proxy listen address")
	targetsList := flag.String("targets", "http://127.0.0.1:8001,http://127.0.0.1:8002,http://127.0.0.1:8003", "Comma-separated upstream targets")
	checkIntervalMs := flag.Int("check-interval-ms", 500, "Health check interval in milliseconds")
	checkTimeoutMs := flag.Int("check-timeout-ms", 200, "Health check timeout in milliseconds")
	debugHeaders := flag.Bool("debug-headers", true, "Expose debug headers in response")
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
	if err := pxy.Start(); err != nil {
		log.Fatalf("failed to start proxy: %v", err)
	}

	log.Printf("[FlashFlow Proxy] Listening on %s routing to [%s]", pxy.AddrPort(), strings.Join(targets, ", "))

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
