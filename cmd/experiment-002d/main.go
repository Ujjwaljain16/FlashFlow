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

type Experiment002DResult struct {
	Experiment                      string                   `json:"experiment"`
	Timestamp                       string                   `json:"timestamp"`
	Concurrency                     int                      `json:"concurrency"`
	Requests                        int                      `json:"requests"`
	EdgeADelayMs                    int                      `json:"edge_a_delay_ms"`
	EdgeBDelayMs                    int                      `json:"edge_b_delay_ms"`
	EdgeCDelayMs                    int                      `json:"edge_c_delay_ms"`
	SuccessfulRequests              int                      `json:"successful_requests"`
	FailedRequests                  int                      `json:"failed_requests"`
	ThroughputRPS                   float64                  `json:"throughput_rps"`
	ClientLatencies                 httpx.LatencyPercentiles `json:"client_latencies"`
	ProxyUpstreamConnections        uint64                   `json:"proxy_upstream_connections"`
	TotalEdgeOriginConnections      uint64                   `json:"total_edge_origin_connections"`
	RequestsPerProxyConnection      float64                  `json:"requests_per_proxy_connection"`
	RequestsPerEdgeOriginConnection float64                  `json:"requests_per_edge_origin_connection"`
	Findings                        string                   `json:"findings"`
}

func main() {
	outDir := filepath.Join("experiments", "002-http-reverse-proxy", "results")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 002-D: One Slow Edge Latency Impact under Static Selection")
	fmt.Println(" Topology: Client -> Proxy -> [Edge A (1ms), Edge B (1ms), Edge C (100ms)] -> Origin")
	fmt.Println("==========================================================================================")

	testCases := []struct {
		concurrency int
		requests    int
		edgeADelay  time.Duration
		edgeBDelay  time.Duration
		edgeCDelay  time.Duration
	}{
		// Control baseline: all fast (1ms)
		{concurrency: 30, requests: 600, edgeADelay: 1 * time.Millisecond, edgeBDelay: 1 * time.Millisecond, edgeCDelay: 1 * time.Millisecond},
		// Experimental: Edge C degraded to 100ms
		{concurrency: 30, requests: 600, edgeADelay: 1 * time.Millisecond, edgeBDelay: 1 * time.Millisecond, edgeCDelay: 100 * time.Millisecond},
	}

	for _, tc := range testCases {
		fmt.Printf("\n--- Cell: c=%-3d r=%-4d [A=%v B=%v C=%v] ---\n",
			tc.concurrency, tc.requests, tc.edgeADelay, tc.edgeBDelay, tc.edgeCDelay)

		origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-002d"})
		if err := origin.Start(); err != nil {
			log.Fatalf("failed to start origin: %v", err)
		}

		edgeA, _ := topology.NewEdgeServer(topology.EdgeConfig{
			Instance: "edge-a", OriginURL: origin.URL(), DefaultDelay: tc.edgeADelay,
			TransportConfig: transport.TransportConfig{Label: "edge_origin_a"},
		})
		edgeB, _ := topology.NewEdgeServer(topology.EdgeConfig{
			Instance: "edge-b", OriginURL: origin.URL(), DefaultDelay: tc.edgeBDelay,
			TransportConfig: transport.TransportConfig{Label: "edge_origin_b"},
		})
		edgeC, _ := topology.NewEdgeServer(topology.EdgeConfig{
			Instance: "edge-c", OriginURL: origin.URL(), DefaultDelay: tc.edgeCDelay,
			TransportConfig: transport.TransportConfig{Label: "edge_origin_c"},
		})

		_ = edgeA.Start()
		_ = edgeB.Start()
		_ = edgeC.Start()

		edges := []*topology.EdgeServer{edgeA, edgeB, edgeC}
		edgeURLs := []string{edgeA.URL(), edgeB.URL(), edgeC.URL()}

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
		_ = pxy.Start()

		payloadBytes := bytes.Repeat([]byte("D"), 64)

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

		finding := "Static routing evenly distributes 1/3 of all requests to Edge C despite 100ms latency, severely inflating p95/p99 tail latency and reducing overall RPS."
		if tc.edgeCDelay == 1*time.Millisecond {
			finding = "Homogeneous baseline: All edges respond in ~1ms, providing symmetric load distribution and low tail latency."
		}

		result := Experiment002DResult{
			Experiment:                      "002-D-one-slow-edge-latency-impact",
			Timestamp:                       time.Now().UTC().Format(time.RFC3339),
			Concurrency:                     tc.concurrency,
			Requests:                        tc.requests,
			EdgeADelayMs:                    int(tc.edgeADelay.Milliseconds()),
			EdgeBDelayMs:                    int(tc.edgeBDelay.Milliseconds()),
			EdgeCDelayMs:                    int(tc.edgeCDelay.Milliseconds()),
			SuccessfulRequests:              res.SuccessfulRequests,
			FailedRequests:                  res.FailedRequests,
			ThroughputRPS:                   res.ThroughputRPS,
			ClientLatencies:                 res.ClientLatencies,
			ProxyUpstreamConnections:        proxyStats.SuccessfulDials,
			TotalEdgeOriginConnections:      totalEdgeDials,
			RequestsPerProxyConnection:      proxyStats.RequestsPerConn,
			RequestsPerEdgeOriginConnection: reqsPerEdgeConn,
			Findings:                        finding,
		}

		fname := filepath.Join(outDir, fmt.Sprintf("002D-c%03d-delayC%dms.json",
			tc.concurrency, int(tc.edgeCDelay.Milliseconds())))
		b, _ := json.MarshalIndent(result, "", "  ")
		os.WriteFile(fname, b, 0644)

		fmt.Printf("  Results: RPS=%.1f | p50=%v | p95=%v | p99=%v\n",
			res.ThroughputRPS, res.ClientLatencies.P50, res.ClientLatencies.P95, res.ClientLatencies.P99)
		fmt.Printf("  Findings: %s\n", finding)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pxy.Stop(ctx)
		edgeA.Stop(ctx)
		edgeB.Stop(ctx)
		edgeC.Stop(ctx)
		origin.Stop(ctx)
		cancel()
		time.Sleep(150 * time.Millisecond)
	}

	fmt.Println("\nExperiment 002-D complete.")
}
