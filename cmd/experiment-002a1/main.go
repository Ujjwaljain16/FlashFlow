package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/clock"
	"flashflow/internal/health"
	"flashflow/internal/httpx"
	"flashflow/internal/proxy"
	"flashflow/internal/topology"
	"flashflow/internal/transport"
)

type Experiment002A1Result struct {
	Experiment                      string                   `json:"experiment"`
	Timestamp                       string                   `json:"timestamp"`
	Concurrency                     int                      `json:"concurrency"`
	Requests                        int                      `json:"requests"`
	PayloadBytes                    int                      `json:"payload_bytes"`
	KeepAliveEnabled                bool                     `json:"keep_alive_enabled"`
	SuccessfulRequests              int                      `json:"successful_requests"`
	FailedRequests                  int                      `json:"failed_requests"`
	ThroughputRPS                   float64                  `json:"throughput_rps"`
	ClientLatencies                 httpx.LatencyPercentiles `json:"client_latencies"`
	ProxyUpstreamConnections        uint64                   `json:"proxy_upstream_connections"`
	EdgeOriginConnections           uint64                   `json:"edge_origin_connections"`
	RequestsPerProxyConnection      float64                  `json:"requests_per_proxy_connection"`
	RequestsPerEdgeOriginConnection float64                  `json:"requests_per_edge_origin_connection"`
}

func main() {
	outDir := filepath.Join("experiments", "002-http-reverse-proxy", "results")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	testCases := []struct {
		concurrency int
		requests    int
		payload     int
		keepAlive   bool
	}{
		// Keep-Alive ENABLED
		{concurrency: 1, requests: 500, payload: 64, keepAlive: true},
		{concurrency: 10, requests: 1000, payload: 64, keepAlive: true},
		{concurrency: 50, requests: 1000, payload: 64, keepAlive: true},
		{concurrency: 100, requests: 1000, payload: 64, keepAlive: true},
		{concurrency: 10, requests: 1000, payload: 1024, keepAlive: true},

		// Keep-Alive DISABLED (Per-Request Connection)
		{concurrency: 1, requests: 500, payload: 64, keepAlive: false},
		{concurrency: 10, requests: 1000, payload: 64, keepAlive: false},
		{concurrency: 50, requests: 1000, payload: 64, keepAlive: false},
		{concurrency: 100, requests: 1000, payload: 64, keepAlive: false},
		{concurrency: 10, requests: 1000, payload: 1024, keepAlive: false},
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 002-A1: Single Upstream Connection Reuse Isolation")
	fmt.Println(" Topology: Client -> Proxy -> Edge-1 -> Origin")
	fmt.Println("==========================================================================================")

	for _, tc := range testCases {
		modeStr := "Keep-Alive"
		if !tc.keepAlive {
			modeStr = "Disabled (Per-Req)"
		}
		fmt.Printf("\n--- Cell: c=%-3d r=%-4d p=%-4dB [%s] ---\n",
			tc.concurrency, tc.requests, tc.payload, modeStr)

		// 1. Start fresh Origin
		origin := topology.NewOriginServer(topology.OriginConfig{
			Instance: "origin-002a1",
		})
		if err := origin.Start(); err != nil {
			log.Fatalf("failed to start origin: %v", err)
		}

		// 2. Start fresh Edge
		edgeCfg := topology.EdgeConfig{
			Instance:  "edge-002a1",
			OriginURL: origin.URL(),
			TransportConfig: transport.TransportConfig{
				Label:               "edge_origin",
				DisableKeepAlives:   !tc.keepAlive,
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
			},
		}
		edge, err := topology.NewEdgeServer(edgeCfg)
		if err != nil {
			log.Fatalf("failed to create edge: %v", err)
		}
		if err := edge.Start(); err != nil {
			log.Fatalf("failed to start edge: %v", err)
		}

		// 3. Start fresh Proxy
		clk := clock.NewWallClock()
		proxyCfg := proxy.Config{
			Targets: []string{edge.URL()},
			TransportConfig: transport.TransportConfig{
				Label:               "proxy_upstream",
				DisableKeepAlives:   !tc.keepAlive,
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
			},
			HealthConfig: health.DefaultConfig(),
			ProberConfig: health.DefaultCheckerConfig(),
		}
		pxy := proxy.NewReverseProxy(proxyCfg, clk, proxy.NewStaticSelector(edge.URL()))
		if err := pxy.Start(); err != nil {
			log.Fatalf("failed to start proxy: %v", err)
		}

		payloadBytes := bytes.Repeat([]byte("K"), tc.payload)

		// 4. Warmup run (discarded)
		wCfg := httpx.BenchmarkConfig{
			TargetURL:   pxy.URL(),
			Path:        "/data",
			Requests:    50,
			Concurrency: tc.concurrency,
			Payload:     payloadBytes,
		}
		_, _ = httpx.RunHTTPBenchmark(wCfg)
		time.Sleep(100 * time.Millisecond)

		// 5. Measured benchmark run
		mCfg := httpx.BenchmarkConfig{
			TargetURL:   pxy.URL(),
			Path:        "/data",
			Requests:    tc.requests,
			Concurrency: tc.concurrency,
			Payload:     payloadBytes,
		}
		res, err := httpx.RunHTTPBenchmark(mCfg)
		if err != nil {
			log.Fatalf("benchmark failed: %v", err)
		}

		proxyStats := pxy.TransportStats()
		edgeStats := edge.TransportStats()

		result := Experiment002A1Result{
			Experiment:                      "002-A1-single-upstream-connection-reuse",
			Timestamp:                       time.Now().UTC().Format(time.RFC3339),
			Concurrency:                     tc.concurrency,
			Requests:                        tc.requests,
			PayloadBytes:                    tc.payload,
			KeepAliveEnabled:                tc.keepAlive,
			SuccessfulRequests:              res.SuccessfulRequests,
			FailedRequests:                  res.FailedRequests,
			ThroughputRPS:                   res.ThroughputRPS,
			ClientLatencies:                 res.ClientLatencies,
			ProxyUpstreamConnections:        proxyStats.SuccessfulDials,
			EdgeOriginConnections:           edgeStats.SuccessfulDials,
			RequestsPerProxyConnection:      proxyStats.RequestsPerConn,
			RequestsPerEdgeOriginConnection: edgeStats.RequestsPerConn,
		}

		// Write result JSON
		fname := filepath.Join(outDir, fmt.Sprintf("002A1-c%03d-r%04d-p%04d-ka%t.json",
			tc.concurrency, tc.requests, tc.payload, tc.keepAlive))
		b, _ := json.MarshalIndent(result, "", "  ")
		os.WriteFile(fname, b, 0644)

		fmt.Printf("  Results: RPS=%.1f | Success=%d/%d | p50=%v | p99=%v\n",
			res.ThroughputRPS, res.SuccessfulRequests, res.TotalRequests,
			res.ClientLatencies.P50, res.ClientLatencies.P99)
		fmt.Printf("  Proxy->Edge: Dials=%d  Reqs/Conn=%.2f\n",
			proxyStats.SuccessfulDials, proxyStats.RequestsPerConn)
		fmt.Printf("  Edge->Origin: Dials=%d Reqs/Conn=%.2f\n",
			edgeStats.SuccessfulDials, edgeStats.RequestsPerConn)

		// Teardown cell
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pxy.Stop(ctx)
		edge.Stop(ctx)
		origin.Stop(ctx)
		cancel()
		time.Sleep(150 * time.Millisecond)
	}

	fmt.Println("\nExperiment 002-A1 complete.")
}
