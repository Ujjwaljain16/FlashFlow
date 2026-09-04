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

type Experiment002CResult struct {
	Experiment                      string                   `json:"experiment"`
	Timestamp                       string                   `json:"timestamp"`
	Concurrency                     int                      `json:"concurrency"`
	Requests                        int                      `json:"requests"`
	PayloadBytes                    int                      `json:"payload_bytes"`
	SuccessfulRequests              int                      `json:"successful_requests"`
	FailedRequests                  int                      `json:"failed_requests"`
	ThroughputRPS                   float64                  `json:"throughput_rps"`
	ClientLatencies                 httpx.LatencyPercentiles `json:"client_latencies"`
	ProxyUpstreamConnections        uint64                   `json:"proxy_upstream_connections"`
	TotalEdgeOriginConnections      uint64                   `json:"total_edge_origin_connections"`
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
	}{
		{concurrency: 10, requests: 1500, payload: 64},
		{concurrency: 50, requests: 2000, payload: 64},
		{concurrency: 100, requests: 3000, payload: 64},
		{concurrency: 50, requests: 2000, payload: 1024},
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 002-C: Three-Edge Baseline Throughput Matrix")
	fmt.Println(" Topology: Client -> Proxy -> [Edge A, Edge B, Edge C] -> Origin")
	fmt.Println("==========================================================================================")

	for _, tc := range testCases {
		fmt.Printf("\n--- Cell: c=%-3d r=%-4d p=%-4dB ---\n",
			tc.concurrency, tc.requests, tc.payload)

		origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-002c"})
		if err := origin.Start(); err != nil {
			log.Fatalf("failed to start origin: %v", err)
		}

		edgeNames := []string{"edge-a", "edge-b", "edge-c"}
		edges := make([]*topology.EdgeServer, 0, len(edgeNames))
		edgeURLs := make([]string, 0, len(edgeNames))

		for _, name := range edgeNames {
			eCfg := topology.EdgeConfig{
				Instance:  name,
				OriginURL: origin.URL(),
				TransportConfig: transport.TransportConfig{
					Label:               "edge_origin_" + name,
					MaxIdleConnsPerHost: 100,
				},
			}
			edge, err := topology.NewEdgeServer(eCfg)
			if err != nil {
				log.Fatalf("failed to create edge %s: %v", name, err)
			}
			if err := edge.Start(); err != nil {
				log.Fatalf("failed to start edge %s: %v", name, err)
			}
			edges = append(edges, edge)
			edgeURLs = append(edgeURLs, edge.URL())
		}

		clk := clock.NewWallClock()
		proxyCfg := proxy.Config{
			Targets: edgeURLs,
			TransportConfig: transport.TransportConfig{
				Label:               "proxy_upstream",
				MaxIdleConnsPerHost: 100,
				MaxIdleConns:        300,
			},
			HealthConfig: health.DefaultConfig(),
			ProberConfig: health.DefaultCheckerConfig(),
		}
		pxy := proxy.NewReverseProxy(proxyCfg, clk, proxy.NewRoundRobinSelector())
		if err := pxy.Start(); err != nil {
			log.Fatalf("failed to start proxy: %v", err)
		}

		payloadBytes := bytes.Repeat([]byte("C"), tc.payload)

		// Warmup
		_, _ = httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{
			TargetURL:   pxy.URL(),
			Path:        "/data",
			Requests:    60,
			Concurrency: tc.concurrency,
			Payload:     payloadBytes,
		})
		time.Sleep(100 * time.Millisecond)

		// Measured Benchmark
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
		var totalEdgeDials uint64
		var totalEdgeReqs uint64
		for _, e := range edges {
			st := e.TransportStats()
			totalEdgeDials += st.SuccessfulDials
			totalEdgeReqs += st.RequestsCompleted
		}

		var reqsPerEdgeConn float64
		if totalEdgeDials > 0 {
			reqsPerEdgeConn = float64(totalEdgeReqs) / float64(totalEdgeDials)
		}

		result := Experiment002CResult{
			Experiment:                      "002-C-three-edge-baseline-throughput",
			Timestamp:                       time.Now().UTC().Format(time.RFC3339),
			Concurrency:                     tc.concurrency,
			Requests:                        tc.requests,
			PayloadBytes:                    tc.payload,
			SuccessfulRequests:              res.SuccessfulRequests,
			FailedRequests:                  res.FailedRequests,
			ThroughputRPS:                   res.ThroughputRPS,
			ClientLatencies:                 res.ClientLatencies,
			ProxyUpstreamConnections:        proxyStats.SuccessfulDials,
			TotalEdgeOriginConnections:      totalEdgeDials,
			RequestsPerProxyConnection:      proxyStats.RequestsPerConn,
			RequestsPerEdgeOriginConnection: reqsPerEdgeConn,
		}

		fname := filepath.Join(outDir, fmt.Sprintf("002C-c%03d-r%04d-p%04d.json",
			tc.concurrency, tc.requests, tc.payload))
		b, _ := json.MarshalIndent(result, "", "  ")
		os.WriteFile(fname, b, 0644)

		fmt.Printf("  Results: RPS=%.1f | Success=%d/%d | p50=%v | p95=%v | p99=%v\n",
			res.ThroughputRPS, res.SuccessfulRequests, res.TotalRequests,
			res.ClientLatencies.P50, res.ClientLatencies.P95, res.ClientLatencies.P99)
		fmt.Printf("  Proxy->Edges (3 hosts): Dials=%d  Reqs/Conn=%.2f\n",
			proxyStats.SuccessfulDials, proxyStats.RequestsPerConn)
		fmt.Printf("  Edges->Origin (combined): Dials=%d Reqs/Conn=%.2f\n",
			totalEdgeDials, reqsPerEdgeConn)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pxy.Stop(ctx)
		for _, e := range edges {
			e.Stop(ctx)
		}
		origin.Stop(ctx)
		cancel()
		time.Sleep(150 * time.Millisecond)
	}

	fmt.Println("\nExperiment 002-C complete.")
}
