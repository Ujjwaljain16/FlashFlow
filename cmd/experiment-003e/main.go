package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
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

type edgeCount struct {
	Instance           string  `json:"instance"`
	ProxyRecordedCount uint64  `json:"proxy_recorded_count"`
	EdgeForwardedCount uint64  `json:"edge_forwarded_count"`
	CountsAgree        bool    `json:"counts_agree"`
	SharePercent       float64 `json:"share_percent"`
}

type phaseResult struct {
	Phase                       string                   `json:"phase"`
	SuccessfulRequests          int                      `json:"successful_requests"`
	FailedRequests              int                      `json:"failed_requests"`
	ThroughputRPS               float64                  `json:"throughput_rps"`
	ClientLatencies             httpx.LatencyPercentiles `json:"client_latencies"`
	PerEdgeCounts               []edgeCount              `json:"per_edge_counts"`
	ClientServerOutcomeMismatch int                      `json:"client_server_outcome_mismatch"`
}

type Experiment003EResult struct {
	Experiment  string        `json:"experiment"`
	Timestamp   string        `json:"timestamp"`
	Scenario    string        `json:"scenario"`
	Policy      string        `json:"policy"`
	Concurrency int           `json:"concurrency"`
	Requests    int           `json:"requests"`
	Phases      []phaseResult `json:"phases"`
	Findings    string        `json:"findings"`
}

const outDirName = "experiments/003-routing-policies/results"

var edgeNames = []string{"edge-a", "edge-b", "edge-c"}

func measurePhase(pxy *proxy.ReverseProxy, edges []*topology.EdgeServer, concurrency, requests int, payload []byte) (httpx.BenchmarkResult, []edgeCount, int) {
	baselineReg := pxy.Registry().Snapshot()
	baselineEdge := make(map[string]uint64, len(edges))
	for _, e := range edges {
		baselineEdge[e.URL()] = e.TransportStats().RequestsCompleted
	}

	res, err := httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{
		TargetURL: pxy.URL(), Path: "/data", Requests: requests, Concurrency: concurrency, Payload: payload,
	})
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
			Instance: edgeNames[i], ProxyRecordedCount: proxyCount, EdgeForwardedCount: edgeFwd, CountsAgree: proxyCount == edgeFwd,
		})
		total += proxyCount
	}
	for i := range counts {
		if total > 0 {
			counts[i].SharePercent = 100 * float64(counts[i].ProxyRecordedCount) / float64(total)
		}
	}
	if int(total) != res.SuccessfulRequests+res.FailedRequests {
		log.Fatalf("measurement contamination: total=%d vs successful+failed=%d+%d", total, res.SuccessfulRequests, res.FailedRequests)
	}
	mismatch := int(total) - res.SuccessfulRequests

	return res, counts, mismatch
}

func startTopology(delays map[string]time.Duration) (*topology.OriginServer, []*topology.EdgeServer, map[string]string) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-003e"})
	if err := origin.Start(); err != nil {
		log.Fatalf("failed to start origin: %v", err)
	}
	edges := make([]*topology.EdgeServer, 0, len(edgeNames))
	targetsByName := make(map[string]string, len(edgeNames))
	for _, name := range edgeNames {
		e, err := topology.NewEdgeServer(topology.EdgeConfig{
			Instance: name, OriginURL: origin.URL(), DefaultDelay: delays[name],
			TransportConfig: transport.TransportConfig{Label: fmt.Sprintf("edge_origin_%s", name)},
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
	return origin, edges, targetsByName
}

func stopAll(origin *topology.OriginServer, edges []*topology.EdgeServer, pxy *proxy.ReverseProxy) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	pxy.Stop(ctx)
	for _, e := range edges {
		e.Stop(ctx)
	}
	origin.Stop(ctx)
	cancel()
	time.Sleep(150 * time.Millisecond)
}

func writeResult(scenario, policy string, r Experiment003EResult) {
	fname := filepath.Join(outDirName, fmt.Sprintf("003E-%s-%s.json", scenario, policy))
	b, _ := json.MarshalIndent(r, "", "  ")
	os.WriteFile(fname, b, 0644)
}

func printPhase(label string, res httpx.BenchmarkResult, counts []edgeCount, mismatch int) {
	fmt.Printf("  [%s] RPS=%.1f p50=%v p95=%v p99=%v\n", label, res.ThroughputRPS,
		res.ClientLatencies.P50, res.ClientLatencies.P95, res.ClientLatencies.P99)
	for _, c := range counts {
		fmt.Printf("    %-8s proxy=%-4d edge_forwarded=%-4d share=%.2f%% agree=%t\n",
			c.Instance, c.ProxyRecordedCount, c.EdgeForwardedCount, c.SharePercent, c.CountsAgree)
	}
	if mismatch != 0 {
		fmt.Printf("    NOTE: client/server outcome mismatch=%d\n", mismatch)
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 003-E: Power of Two Choices")
	fmt.Println("==========================================================================================")

	payload := bytes.Repeat([]byte("P"), 64)
	const seed = 20260904 // fixed, recorded seed for reproducibility

	// ---------------------------------------------------------------
	// Part 1 (H5a): pure-homogeneous lock-in check — direct replication
	// of Experiment 003-D's H4a-follow-up, but with P2C-over-latency
	// instead of EWMA's full-scan argmin. Does randomized pairing avoid
	// the 94/4/2-style collapse EWMA showed among genuinely equal edges?
	// ---------------------------------------------------------------
	fmt.Println("\n--- Scenario: pure-homogeneous-lock-in-check (H5a) ---")
	{
		const concurrency = 30
		const requests = 600
		delays := map[string]time.Duration{"edge-a": 1 * time.Millisecond, "edge-b": 1 * time.Millisecond, "edge-c": 1 * time.Millisecond}

		for run := 1; run <= 3; run++ {
			fmt.Printf("\n Run %d/3\n", run)
			origin, edges, _ := startTopology(delays)
			var targets []string
			for _, name := range edgeNames {
				targets = append(targets, edges[indexOf(edgeNames, name)].URL())
			}
			clk := clock.NewWallClock()
			pxy := proxy.NewReverseProxy(proxy.Config{
				Targets:         targets,
				TransportConfig: transport.TransportConfig{Label: "proxy_upstream", MaxIdleConnsPerHost: 100, MaxIdleConns: 300},
				HealthConfig:    health.DefaultConfig(),
				ProberConfig:    health.DefaultCheckerConfig(),
			}, clk, nil)
			pxy.SetSelector(proxy.NewP2CSelector(proxy.ScorerFromLatency(pxy.LatencyTracker()), rand.New(rand.NewSource(seed+int64(run)))))
			if err := pxy.Start(); err != nil {
				log.Fatalf("failed to start proxy: %v", err)
			}

			res, counts, mismatch := measurePhase(pxy, edges, concurrency, requests, payload)
			printPhase("measured", res, counts, mismatch)

			finding := fmt.Sprintf("run %d, 3 IDENTICAL 1ms edges, P2C-over-latency (seed=%d): shares = %.1f%%/%.1f%%/%.1f%% (a/b/c). "+
				"EWMA on the identical setup (003-D) gave e.g. 94.0%%/3.8%%/2.2%%.",
				run, seed+int64(run), counts[0].SharePercent, counts[1].SharePercent, counts[2].SharePercent)

			writeResult("pure-homogeneous-lock-in-check", fmt.Sprintf("run%d", run), Experiment003EResult{
				Experiment: "003-E-power-of-two-choices", Timestamp: time.Now().UTC().Format(time.RFC3339),
				Scenario: "pure-homogeneous-lock-in-check", Policy: "p2c-latency", Concurrency: concurrency, Requests: requests,
				Phases: []phaseResult{{Phase: "measured", SuccessfulRequests: res.SuccessfulRequests, FailedRequests: res.FailedRequests,
					ThroughputRPS: res.ThroughputRPS, ClientLatencies: res.ClientLatencies, PerEdgeCounts: counts, ClientServerOutcomeMismatch: mismatch}},
				Findings: finding,
			})
			stopAll(origin, edges, pxy)
		}
	}

	// ---------------------------------------------------------------
	// Part 2: static-heterogeneous comparison matrix — RR / LC / EWMA /
	// P2C-over-load / P2C-over-latency, same topology as every prior
	// Stage 3 static-heterogeneous run (1ms/1ms/100ms, c=30, r=600).
	// ---------------------------------------------------------------
	fmt.Println("\n--- Scenario: static-heterogeneous (comparison matrix) ---")
	{
		const concurrency = 30
		const requests = 600
		delays := map[string]time.Duration{"edge-a": 1 * time.Millisecond, "edge-b": 1 * time.Millisecond, "edge-c": 100 * time.Millisecond}

		policies := []struct {
			name  string
			build func(pxy *proxy.ReverseProxy) proxy.TargetSelector
		}{
			{"round-robin", func(pxy *proxy.ReverseProxy) proxy.TargetSelector { return proxy.NewRoundRobinSelector() }},
			{"least-connections", func(pxy *proxy.ReverseProxy) proxy.TargetSelector {
				return proxy.NewLeastConnectionsSelector(pxy.LoadTracker())
			}},
			{"ewma", func(pxy *proxy.ReverseProxy) proxy.TargetSelector { return proxy.NewEWMASelector(pxy.LatencyTracker()) }},
			{"p2c-load", func(pxy *proxy.ReverseProxy) proxy.TargetSelector {
				return proxy.NewP2CSelector(proxy.ScorerFromLoad(pxy.LoadTracker()), rand.New(rand.NewSource(seed)))
			}},
			{"p2c-latency", func(pxy *proxy.ReverseProxy) proxy.TargetSelector {
				return proxy.NewP2CSelector(proxy.ScorerFromLatency(pxy.LatencyTracker()), rand.New(rand.NewSource(seed)))
			}},
		}

		for _, p := range policies {
			fmt.Printf("\n Policy: %s\n", p.name)
			origin, edges, _ := startTopology(delays)
			var targets []string
			for _, name := range edgeNames {
				targets = append(targets, edges[indexOf(edgeNames, name)].URL())
			}
			clk := clock.NewWallClock()
			pxy := proxy.NewReverseProxy(proxy.Config{
				Targets:         targets,
				TransportConfig: transport.TransportConfig{Label: "proxy_upstream", MaxIdleConnsPerHost: 100, MaxIdleConns: 300},
				HealthConfig:    health.DefaultConfig(),
				ProberConfig:    health.DefaultCheckerConfig(),
			}, clk, nil)
			pxy.SetSelector(p.build(pxy))
			if err := pxy.Start(); err != nil {
				log.Fatalf("failed to start proxy: %v", err)
			}

			_, _ = httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{TargetURL: pxy.URL(), Path: "/data", Requests: 30, Concurrency: concurrency, Payload: payload})
			time.Sleep(100 * time.Millisecond)

			res, counts, mismatch := measurePhase(pxy, edges, concurrency, requests, payload)
			printPhase("measured", res, counts, mismatch)

			finding := fmt.Sprintf("%s under static 1ms/1ms/100ms: RPS=%.1f p95=%v p99=%v shares a=%.1f%% b=%.1f%% c=%.1f%%.",
				p.name, res.ThroughputRPS, res.ClientLatencies.P95, res.ClientLatencies.P99,
				counts[0].SharePercent, counts[1].SharePercent, counts[2].SharePercent)

			writeResult("static-heterogeneous", p.name, Experiment003EResult{
				Experiment: "003-E-power-of-two-choices", Timestamp: time.Now().UTC().Format(time.RFC3339),
				Scenario: "static-heterogeneous", Policy: p.name, Concurrency: concurrency, Requests: requests,
				Phases: []phaseResult{{Phase: "measured", SuccessfulRequests: res.SuccessfulRequests, FailedRequests: res.FailedRequests,
					ThroughputRPS: res.ThroughputRPS, ClientLatencies: res.ClientLatencies, PerEdgeCounts: counts, ClientServerOutcomeMismatch: mismatch}},
				Findings: finding,
			})
			stopAll(origin, edges, pxy)
		}
	}

	// ---------------------------------------------------------------
	// Part 3 (H5b vs H5c): degradation THEN RECOVERY — no prior Stage 3
	// experiment tested an edge getting better again after getting
	// worse. Phase1: homogeneous. Phase2: edge-c degraded to 100ms.
	// Phase3: edge-c recovers to 1ms. Compare EWMA, P2C-load, and
	// P2C-latency: does each policy's edge-c share recover in phase 3?
	// ---------------------------------------------------------------
	fmt.Println("\n--- Scenario: degradation-then-recovery (H5b vs H5c) ---")
	{
		const concurrency = 30
		const requestsPerPhase = 600
		homogeneousDelays := map[string]time.Duration{"edge-a": 1 * time.Millisecond, "edge-b": 1 * time.Millisecond, "edge-c": 1 * time.Millisecond}

		policies := []struct {
			name  string
			build func(pxy *proxy.ReverseProxy) proxy.TargetSelector
		}{
			{"ewma", func(pxy *proxy.ReverseProxy) proxy.TargetSelector { return proxy.NewEWMASelector(pxy.LatencyTracker()) }},
			{"p2c-load", func(pxy *proxy.ReverseProxy) proxy.TargetSelector {
				return proxy.NewP2CSelector(proxy.ScorerFromLoad(pxy.LoadTracker()), rand.New(rand.NewSource(seed)))
			}},
			{"p2c-latency", func(pxy *proxy.ReverseProxy) proxy.TargetSelector {
				return proxy.NewP2CSelector(proxy.ScorerFromLatency(pxy.LatencyTracker()), rand.New(rand.NewSource(seed)))
			}},
		}

		for _, p := range policies {
			fmt.Printf("\n Policy: %s\n", p.name)
			origin, edges, targetsByName := startTopology(homogeneousDelays)
			var targets []string
			for _, name := range edgeNames {
				targets = append(targets, targetsByName[name])
			}
			clk := clock.NewWallClock()
			pxy := proxy.NewReverseProxy(proxy.Config{
				Targets:         targets,
				TransportConfig: transport.TransportConfig{Label: "proxy_upstream", MaxIdleConnsPerHost: 100, MaxIdleConns: 300},
				HealthConfig:    health.DefaultConfig(),
				ProberConfig:    health.DefaultCheckerConfig(),
			}, clk, nil)
			pxy.SetSelector(p.build(pxy))
			if err := pxy.Start(); err != nil {
				log.Fatalf("failed to start proxy: %v", err)
			}

			_, _ = httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{TargetURL: pxy.URL(), Path: "/data", Requests: 30, Concurrency: concurrency, Payload: payload})
			time.Sleep(100 * time.Millisecond)

			edgeC := edges[indexOf(edgeNames, "edge-c")]

			phase1Res, phase1Counts, phase1Mismatch := measurePhase(pxy, edges, concurrency, requestsPerPhase, payload)
			printPhase("phase1-homogeneous", phase1Res, phase1Counts, phase1Mismatch)

			edgeC.SetArtificialDelay(100 * time.Millisecond)
			time.Sleep(50 * time.Millisecond)
			phase2Res, phase2Counts, phase2Mismatch := measurePhase(pxy, edges, concurrency, requestsPerPhase, payload)
			printPhase("phase2-degraded", phase2Res, phase2Counts, phase2Mismatch)

			edgeC.SetArtificialDelay(1 * time.Millisecond)
			time.Sleep(50 * time.Millisecond)
			phase3Res, phase3Counts, phase3Mismatch := measurePhase(pxy, edges, concurrency, requestsPerPhase, payload)
			printPhase("phase3-recovered", phase3Res, phase3Counts, phase3Mismatch)

			finding := fmt.Sprintf(
				"%s: edge-c share phase1(homogeneous)=%.1f%% phase2(degraded)=%.1f%% phase3(recovered-to-1ms)=%.1f%%. "+
					"RPS phase1=%.1f phase2=%.1f phase3=%.1f.",
				p.name, phase1Counts[2].SharePercent, phase2Counts[2].SharePercent, phase3Counts[2].SharePercent,
				phase1Res.ThroughputRPS, phase2Res.ThroughputRPS, phase3Res.ThroughputRPS)

			writeResult("degradation-then-recovery", p.name, Experiment003EResult{
				Experiment: "003-E-power-of-two-choices", Timestamp: time.Now().UTC().Format(time.RFC3339),
				Scenario: "degradation-then-recovery", Policy: p.name, Concurrency: concurrency, Requests: requestsPerPhase,
				Phases: []phaseResult{
					{Phase: "phase1-homogeneous", SuccessfulRequests: phase1Res.SuccessfulRequests, FailedRequests: phase1Res.FailedRequests,
						ThroughputRPS: phase1Res.ThroughputRPS, ClientLatencies: phase1Res.ClientLatencies, PerEdgeCounts: phase1Counts, ClientServerOutcomeMismatch: phase1Mismatch},
					{Phase: "phase2-degraded", SuccessfulRequests: phase2Res.SuccessfulRequests, FailedRequests: phase2Res.FailedRequests,
						ThroughputRPS: phase2Res.ThroughputRPS, ClientLatencies: phase2Res.ClientLatencies, PerEdgeCounts: phase2Counts, ClientServerOutcomeMismatch: phase2Mismatch},
					{Phase: "phase3-recovered", SuccessfulRequests: phase3Res.SuccessfulRequests, FailedRequests: phase3Res.FailedRequests,
						ThroughputRPS: phase3Res.ThroughputRPS, ClientLatencies: phase3Res.ClientLatencies, PerEdgeCounts: phase3Counts, ClientServerOutcomeMismatch: phase3Mismatch},
				},
				Findings: finding,
			})
			stopAll(origin, edges, pxy)
		}
	}

	fmt.Println("\nExperiment 003-E complete.")
}
