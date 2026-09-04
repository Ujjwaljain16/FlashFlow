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

// edgeCount mirrors the two-independent-instrumentation-point pattern from
// Experiment 003-A: the proxy's health registry vs. each edge's own
// edge->origin transport. See that experiment's comment for why this
// cross-check matters.
type edgeCount struct {
	Instance           string  `json:"instance"`
	ProxyRecordedCount uint64  `json:"proxy_recorded_count"`
	EdgeForwardedCount uint64  `json:"edge_forwarded_count"`
	CountsAgree        bool    `json:"counts_agree"`
	SharePercent       float64 `json:"share_percent"`
}

// Experiment003BResult is the on-disk record for one measured policy cell.
type Experiment003BResult struct {
	Experiment                  string                   `json:"experiment"`
	Timestamp                   string                   `json:"timestamp"`
	Policy                      string                   `json:"policy"`
	Concurrency                 int                      `json:"concurrency"`
	Requests                    int                      `json:"requests"`
	EdgeADelayMs                int                      `json:"edge_a_delay_ms"`
	EdgeBDelayMs                int                      `json:"edge_b_delay_ms"`
	EdgeCDelayMs                int                      `json:"edge_c_delay_ms"`
	SuccessfulRequests          int                      `json:"successful_requests"`
	FailedRequests              int                      `json:"failed_requests"`
	ThroughputRPS               float64                  `json:"throughput_rps"`
	ClientLatencies             httpx.LatencyPercentiles `json:"client_latencies"`
	PerEdgeCounts               []edgeCount              `json:"per_edge_counts"`
	ClientServerOutcomeMismatch int                      `json:"client_server_outcome_mismatch"`
	Findings                    string                   `json:"findings"`
}

func main() {
	outDir := filepath.Join("experiments", "003-routing-policies", "results")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 003-B: Weighted Round Robin Under Known, Static Heterogeneous Capacity")
	fmt.Println(" Topology: Client -> Proxy -> [Edge A=1ms, Edge B=1ms, Edge C=100ms] -> Origin")
	fmt.Println(" Replicates Experiment 002-D's exact topology/concurrency for direct comparability.")
	fmt.Println("==========================================================================================")

	const (
		concurrency = 30
		requests    = 600
		delayA      = 1 * time.Millisecond
		delayB      = 1 * time.Millisecond
		delayC      = 100 * time.Millisecond
	)

	edgeNames := []string{"edge-a", "edge-b", "edge-c"}

	testCases := []struct {
		policy      string
		description string
		newSelector func(targets map[string]string) proxy.TargetSelector
	}{
		{
			policy:      "round-robin",
			description: "Plain Round Robin (replicates 002-D's 'One Degraded Edge' cell)",
			newSelector: func(targets map[string]string) proxy.TargetSelector {
				return proxy.NewRoundRobinSelector()
			},
		},
		{
			policy:      "wrr-equal-weights",
			description: "WRR with equal weights 1:1:1 (sanity check: should behave like Round Robin)",
			newSelector: func(targets map[string]string) proxy.TargetSelector {
				return proxy.NewWeightedRoundRobinSelector(proxy.TargetWeights{
					targets["edge-a"]: 1, targets["edge-b"]: 1, targets["edge-c"]: 1,
				})
			},
		},
		{
			policy:      "wrr-capacity-correct",
			description: "WRR weighted 100:100:1, inversely proportional to known service time (1ms:1ms:100ms)",
			newSelector: func(targets map[string]string) proxy.TargetSelector {
				return proxy.NewWeightedRoundRobinSelector(proxy.TargetWeights{
					targets["edge-a"]: 100, targets["edge-b"]: 100, targets["edge-c"]: 1,
				})
			},
		},
	}

	for _, tc := range testCases {
		fmt.Printf("\n--- Policy: %-22s (%s) ---\n", tc.policy, tc.description)

		origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-003b"})
		if err := origin.Start(); err != nil {
			log.Fatalf("failed to start origin: %v", err)
		}

		delays := map[string]time.Duration{"edge-a": delayA, "edge-b": delayB, "edge-c": delayC}
		edges := make([]*topology.EdgeServer, 0, len(edgeNames))
		targetsByName := make(map[string]string, len(edgeNames))
		for _, name := range edgeNames {
			e, err := topology.NewEdgeServer(topology.EdgeConfig{
				Instance:     name,
				OriginURL:    origin.URL(),
				DefaultDelay: delays[name],
				TransportConfig: transport.TransportConfig{
					Label: fmt.Sprintf("edge_origin_%s", name),
				},
			})
			if err != nil {
				log.Fatalf("failed to create %s: %v", name, err)
			}
			if err := e.Start(); err != nil {
				log.Fatalf("failed to start %s: %v", name, err)
			}
			edges = append(edges, e)
			targetsByName[name] = e.URL()
		}

		var targetURLs []string
		for _, name := range edgeNames {
			targetURLs = append(targetURLs, targetsByName[name])
		}

		clk := clock.NewWallClock()
		proxyCfg := proxy.Config{
			Targets: targetURLs,
			TransportConfig: transport.TransportConfig{
				Label:               "proxy_upstream",
				MaxIdleConnsPerHost: 100,
				MaxIdleConns:        300,
			},
			HealthConfig: health.DefaultConfig(),
			ProberConfig: health.DefaultCheckerConfig(),
		}
		pxy := proxy.NewReverseProxy(proxyCfg, clk, tc.newSelector(targetsByName))
		if err := pxy.Start(); err != nil {
			log.Fatalf("failed to start proxy: %v", err)
		}

		payloadBytes := bytes.Repeat([]byte("W"), 64)

		wCfg := httpx.BenchmarkConfig{
			TargetURL:   pxy.URL(),
			Path:        "/data",
			Requests:    30,
			Concurrency: concurrency,
			Payload:     payloadBytes,
		}
		_, _ = httpx.RunHTTPBenchmark(wCfg)
		time.Sleep(100 * time.Millisecond)

		baselineReg := pxy.Registry().Snapshot()
		baselineEdge := make(map[string]uint64, len(edges))
		for _, e := range edges {
			baselineEdge[e.URL()] = e.TransportStats().RequestsCompleted
		}

		mCfg := httpx.BenchmarkConfig{
			TargetURL:   pxy.URL(),
			Path:        "/data",
			Requests:    requests,
			Concurrency: concurrency,
			Payload:     payloadBytes,
		}
		res, err := httpx.RunHTTPBenchmark(mCfg)
		if err != nil {
			log.Fatalf("benchmark failed: %v", err)
		}

		regSnapshot := pxy.Registry().Snapshot()

		counts := make([]edgeCount, 0, len(edges))
		var total uint64
		for i, e := range edges {
			proxyCount := regSnapshot[e.URL()].TotalAppRequests - baselineReg[e.URL()].TotalAppRequests
			edgeFwd := e.TransportStats().RequestsCompleted - baselineEdge[e.URL()]
			counts = append(counts, edgeCount{
				Instance:           edgeNames[i],
				ProxyRecordedCount: proxyCount,
				EdgeForwardedCount: edgeFwd,
				CountsAgree:        proxyCount == edgeFwd,
			})
			total += proxyCount
		}
		for i := range counts {
			if total > 0 {
				counts[i].SharePercent = 100 * float64(counts[i].ProxyRecordedCount) / float64(total)
			}
		}

		if int(total) != res.SuccessfulRequests+res.FailedRequests {
			log.Fatalf("measurement contamination detected: per-edge total (%d) does not match "+
				"benchmark successful+failed requests (%d+%d) for policy %s — data would be invalid",
				total, res.SuccessfulRequests, res.FailedRequests, tc.policy)
		}
		clientServerMismatch := int(total) - res.SuccessfulRequests

		allAgree := true
		for _, c := range counts {
			if !c.CountsAgree {
				allAgree = false
			}
		}

		finding := fmt.Sprintf(
			"%s: RPS=%.1f p95=%v p99=%v. Edge-C (100ms) received %.2f%% of traffic (%d/%d requests). "+
				"Proxy/edge counts agreed for all edges: %t.",
			tc.policy, res.ThroughputRPS, res.ClientLatencies.P95, res.ClientLatencies.P99,
			counts[2].SharePercent, counts[2].ProxyRecordedCount, total, allAgree,
		)

		result := Experiment003BResult{
			Experiment:                  "003-B-weighted-round-robin-heterogeneous-capacity",
			Timestamp:                   time.Now().UTC().Format(time.RFC3339),
			Policy:                      tc.policy,
			Concurrency:                 concurrency,
			Requests:                    requests,
			EdgeADelayMs:                int(delayA.Milliseconds()),
			EdgeBDelayMs:                int(delayB.Milliseconds()),
			EdgeCDelayMs:                int(delayC.Milliseconds()),
			SuccessfulRequests:          res.SuccessfulRequests,
			FailedRequests:              res.FailedRequests,
			ThroughputRPS:               res.ThroughputRPS,
			ClientLatencies:             res.ClientLatencies,
			PerEdgeCounts:               counts,
			ClientServerOutcomeMismatch: clientServerMismatch,
			Findings:                    finding,
		}

		fname := filepath.Join(outDir, fmt.Sprintf("003B-%s.json", tc.policy))
		b, _ := json.MarshalIndent(result, "", "  ")
		os.WriteFile(fname, b, 0644)

		fmt.Printf("  Results: RPS=%.1f | p50=%v | p95=%v | p99=%v\n",
			res.ThroughputRPS, res.ClientLatencies.P50, res.ClientLatencies.P95, res.ClientLatencies.P99)
		for _, c := range counts {
			fmt.Printf("  %-8s proxy=%-4d edge_forwarded=%-4d share=%.2f%% agree=%t\n",
				c.Instance, c.ProxyRecordedCount, c.EdgeForwardedCount, c.SharePercent, c.CountsAgree)
		}
		if clientServerMismatch != 0 {
			fmt.Printf("  NOTE: client/server outcome mismatch=%d\n", clientServerMismatch)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pxy.Stop(ctx)
		for _, e := range edges {
			e.Stop(ctx)
		}
		origin.Stop(ctx)
		cancel()
		time.Sleep(150 * time.Millisecond)
	}

	fmt.Println("\nExperiment 003-B complete.")
}
