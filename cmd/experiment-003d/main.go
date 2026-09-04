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
	EdgeCEWMAEstimateMs         float64                  `json:"edge_c_ewma_estimate_ms,omitempty"`
	ClientServerOutcomeMismatch int                      `json:"client_server_outcome_mismatch"`
}

type Experiment003DResult struct {
	Experiment  string        `json:"experiment"`
	Timestamp   string        `json:"timestamp"`
	Scenario    string        `json:"scenario"`
	Policy      string        `json:"policy"`
	Alpha       float64       `json:"alpha,omitempty"`
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
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-003d"})
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

func writeResult(scenario, policy string, r Experiment003DResult) {
	fname := filepath.Join(outDirName, fmt.Sprintf("003D-%s-%s.json", scenario, policy))
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
	fmt.Println(" Experiment 003-D: Latency-Aware EWMA Under Latency Variation")
	fmt.Println("==========================================================================================")

	payload := bytes.Repeat([]byte("E"), 64)

	// ---------------------------------------------------------------
	// Part 1 (H4a): static-heterogeneous — RR vs LC vs EWMA, same
	// topology as 003-B/003-C for direct comparability.
	// ---------------------------------------------------------------
	fmt.Println("\n--- Scenario: static-heterogeneous (H4a) ---")
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

			finding := fmt.Sprintf("%s under static 1ms/1ms/100ms: RPS=%.1f p95=%v p99=%v edge-c share=%.2f%%.",
				p.name, res.ThroughputRPS, res.ClientLatencies.P95, res.ClientLatencies.P99, counts[2].SharePercent)

			writeResult("static-heterogeneous", p.name, Experiment003DResult{
				Experiment: "003-D-ewma-latency-variation", Timestamp: time.Now().UTC().Format(time.RFC3339),
				Scenario: "static-heterogeneous", Policy: p.name, Concurrency: concurrency, Requests: requests,
				Phases: []phaseResult{{Phase: "measured", SuccessfulRequests: res.SuccessfulRequests, FailedRequests: res.FailedRequests,
					ThroughputRPS: res.ThroughputRPS, ClientLatencies: res.ClientLatencies, PerEdgeCounts: counts, ClientServerOutcomeMismatch: mismatch}},
				Findings: finding,
			})
			stopAll(origin, edges, pxy)
		}
	}

	// ---------------------------------------------------------------
	// Part 2 (H4b): dynamic-degradation — RR vs LC vs EWMA, same
	// two-phase scenario as 003-C.
	// ---------------------------------------------------------------
	fmt.Println("\n--- Scenario: dynamic-degradation (H4b) ---")
	{
		const concurrency = 30
		const requestsPerPhase = 600
		homogeneousDelays := map[string]time.Duration{"edge-a": 1 * time.Millisecond, "edge-b": 1 * time.Millisecond, "edge-c": 1 * time.Millisecond}

		policies := []struct {
			name  string
			build func(pxy *proxy.ReverseProxy) proxy.TargetSelector
		}{
			{"round-robin", func(pxy *proxy.ReverseProxy) proxy.TargetSelector { return proxy.NewRoundRobinSelector() }},
			{"least-connections", func(pxy *proxy.ReverseProxy) proxy.TargetSelector {
				return proxy.NewLeastConnectionsSelector(pxy.LoadTracker())
			}},
			{"ewma", func(pxy *proxy.ReverseProxy) proxy.TargetSelector { return proxy.NewEWMASelector(pxy.LatencyTracker()) }},
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

			phase1Res, phase1Counts, phase1Mismatch := measurePhase(pxy, edges, concurrency, requestsPerPhase, payload)
			printPhase("phase1-homogeneous", phase1Res, phase1Counts, phase1Mismatch)

			edges[indexOf(edgeNames, "edge-c")].SetArtificialDelay(100 * time.Millisecond)
			time.Sleep(50 * time.Millisecond)

			phase2Res, phase2Counts, phase2Mismatch := measurePhase(pxy, edges, concurrency, requestsPerPhase, payload)
			printPhase("phase2-degraded", phase2Res, phase2Counts, phase2Mismatch)

			finding := fmt.Sprintf(
				"%s: phase1 edge-c share=%.2f%% RPS=%.1f. phase2 (degraded, no reconfiguration) edge-c share=%.2f%% RPS=%.1f p95=%v p99=%v.",
				p.name, phase1Counts[2].SharePercent, phase1Res.ThroughputRPS,
				phase2Counts[2].SharePercent, phase2Res.ThroughputRPS, phase2Res.ClientLatencies.P95, phase2Res.ClientLatencies.P99)

			writeResult("dynamic-degradation", p.name, Experiment003DResult{
				Experiment: "003-D-ewma-latency-variation", Timestamp: time.Now().UTC().Format(time.RFC3339),
				Scenario: "dynamic-degradation", Policy: p.name, Concurrency: concurrency, Requests: requestsPerPhase,
				Phases: []phaseResult{
					{Phase: "phase1-homogeneous", SuccessfulRequests: phase1Res.SuccessfulRequests, FailedRequests: phase1Res.FailedRequests,
						ThroughputRPS: phase1Res.ThroughputRPS, ClientLatencies: phase1Res.ClientLatencies, PerEdgeCounts: phase1Counts, ClientServerOutcomeMismatch: phase1Mismatch},
					{Phase: "phase2-degraded", SuccessfulRequests: phase2Res.SuccessfulRequests, FailedRequests: phase2Res.FailedRequests,
						ThroughputRPS: phase2Res.ThroughputRPS, ClientLatencies: phase2Res.ClientLatencies, PerEdgeCounts: phase2Counts, ClientServerOutcomeMismatch: phase2Mismatch},
				},
				Findings: finding,
			})
			stopAll(origin, edges, pxy)
		}
	}

	// ---------------------------------------------------------------
	// Part 3 (H4c): alpha-oscillation — EWMA only, three alpha values,
	// edge-c's delay alternates fast/slow across 6 phases. Tests whether
	// low alpha lags (stable but slow) and high alpha tracks quickly
	// (reactive but potentially volatile) — an open question, not a
	// hypothesis with a predicted winner.
	// ---------------------------------------------------------------
	fmt.Println("\n--- Scenario: alpha-oscillation (H4c) ---")
	{
		const concurrency = 10
		const requestsPerPhase = 200
		fastDelay := 1 * time.Millisecond
		slowDelay := 150 * time.Millisecond
		// Phase i is "slow" (edge-c degraded) when i is odd.
		phaseIsSlow := []bool{false, true, false, true, false, true}

		alphas := []float64{0.05, 0.2, 0.6}

		for _, alpha := range alphas {
			fmt.Printf("\n Alpha: %.2f\n", alpha)
			origin, edges, targetsByName := startTopology(map[string]time.Duration{
				"edge-a": fastDelay, "edge-b": fastDelay, "edge-c": fastDelay,
			})
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
				EWMAAlpha:       alpha,
			}, clk, nil)
			pxy.SetSelector(proxy.NewEWMASelector(pxy.LatencyTracker()))
			if err := pxy.Start(); err != nil {
				log.Fatalf("failed to start proxy: %v", err)
			}

			_, _ = httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{TargetURL: pxy.URL(), Path: "/data", Requests: 10, Concurrency: concurrency, Payload: payload})
			time.Sleep(50 * time.Millisecond)

			var phases []phaseResult
			edgeC := edges[indexOf(edgeNames, "edge-c")]
			for i, slow := range phaseIsSlow {
				if slow {
					edgeC.SetArtificialDelay(slowDelay)
				} else {
					edgeC.SetArtificialDelay(fastDelay)
				}
				time.Sleep(30 * time.Millisecond)

				res, counts, mismatch := measurePhase(pxy, edges, concurrency, requestsPerPhase, payload)
				estMs := 0.0
				if est, ok := pxy.LatencyTracker().Estimate(edgeC.URL()); ok {
					estMs = float64(est.Microseconds()) / 1000.0
				}
				label := fmt.Sprintf("phase%d-%s", i+1, map[bool]string{true: "slow", false: "fast"}[slow])
				printPhase(label, res, counts, mismatch)
				fmt.Printf("    edge-c EWMA estimate: %.2fms\n", estMs)

				phases = append(phases, phaseResult{
					Phase: label, SuccessfulRequests: res.SuccessfulRequests, FailedRequests: res.FailedRequests,
					ThroughputRPS: res.ThroughputRPS, ClientLatencies: res.ClientLatencies, PerEdgeCounts: counts,
					EdgeCEWMAEstimateMs: estMs, ClientServerOutcomeMismatch: mismatch,
				})
			}

			finding := fmt.Sprintf("alpha=%.2f: edge-c share per phase (fast/slow/fast/slow/fast/slow) = "+
				"%.1f%%/%.1f%%/%.1f%%/%.1f%%/%.1f%%/%.1f%%; EWMA estimate per phase (ms) = %.1f/%.1f/%.1f/%.1f/%.1f/%.1f",
				alpha,
				phases[0].PerEdgeCounts[2].SharePercent, phases[1].PerEdgeCounts[2].SharePercent,
				phases[2].PerEdgeCounts[2].SharePercent, phases[3].PerEdgeCounts[2].SharePercent,
				phases[4].PerEdgeCounts[2].SharePercent, phases[5].PerEdgeCounts[2].SharePercent,
				phases[0].EdgeCEWMAEstimateMs, phases[1].EdgeCEWMAEstimateMs, phases[2].EdgeCEWMAEstimateMs,
				phases[3].EdgeCEWMAEstimateMs, phases[4].EdgeCEWMAEstimateMs, phases[5].EdgeCEWMAEstimateMs)

			writeResult("alpha-oscillation", fmt.Sprintf("alpha-%.2f", alpha), Experiment003DResult{
				Experiment: "003-D-ewma-latency-variation", Timestamp: time.Now().UTC().Format(time.RFC3339),
				Scenario: "alpha-oscillation", Policy: "ewma", Alpha: alpha, Concurrency: concurrency, Requests: requestsPerPhase,
				Phases: phases, Findings: finding,
			})
			stopAll(origin, edges, pxy)
		}
	}

	// ---------------------------------------------------------------
	// Part 4 (unplanned — added after Part 1/2 showed an unexpected,
	// reproducible skew): purely homogeneous edges, EWMA only, nothing
	// else confounding it. Parts 1 and 2 both showed EWMA giving edge-a
	// 0% and edge-b ~95% of traffic despite both being configured at an
	// IDENTICAL 1ms delay — i.e. EWMA converged to a massively uneven
	// split among genuinely equal targets. This isolates that finding
	// from the heterogeneous-capacity scenario entirely: 3 edges, all at
	// 1ms, nothing ever changes, does EWMA's traffic split stay anywhere
	// near even (33/33/33, like Round Robin would give it) or does it
	// still collapse onto one target?
	// ---------------------------------------------------------------
	fmt.Println("\n--- Scenario: pure-homogeneous-lock-in-check (unplanned) ---")
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
			pxy.SetSelector(proxy.NewEWMASelector(pxy.LatencyTracker()))
			if err := pxy.Start(); err != nil {
				log.Fatalf("failed to start proxy: %v", err)
			}

			res, counts, mismatch := measurePhase(pxy, edges, concurrency, requests, payload)
			printPhase("measured", res, counts, mismatch)

			finding := fmt.Sprintf("run %d, 3 IDENTICAL 1ms edges, EWMA only: shares = %.1f%%/%.1f%%/%.1f%% (a/b/c). "+
				"Round Robin would give ~33.3%%/33.3%%/33.3%%.",
				run, counts[0].SharePercent, counts[1].SharePercent, counts[2].SharePercent)

			writeResult("pure-homogeneous-lock-in-check", fmt.Sprintf("run%d", run), Experiment003DResult{
				Experiment: "003-D-ewma-latency-variation", Timestamp: time.Now().UTC().Format(time.RFC3339),
				Scenario: "pure-homogeneous-lock-in-check", Policy: "ewma", Concurrency: concurrency, Requests: requests,
				Phases: []phaseResult{{Phase: "measured", SuccessfulRequests: res.SuccessfulRequests, FailedRequests: res.FailedRequests,
					ThroughputRPS: res.ThroughputRPS, ClientLatencies: res.ClientLatencies, PerEdgeCounts: counts, ClientServerOutcomeMismatch: mismatch}},
				Findings: finding,
			})
			stopAll(origin, edges, pxy)
		}
	}

	fmt.Println("\nExperiment 003-D complete.")
}
