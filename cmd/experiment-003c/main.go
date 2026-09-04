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
	ClientServerOutcomeMismatch int                      `json:"client_server_outcome_mismatch"`
}

type Experiment003CResult struct {
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

// measurePhase runs one measured benchmark phase against an already-running
// proxy+edges and returns the client-side benchmark result plus per-edge
// counts attributable ONLY to this phase (baselined against the counters'
// state at phase start, following the contamination-avoidance pattern
// established in Experiment 003-A).
func measurePhase(pxy *proxy.ReverseProxy, edges []*topology.EdgeServer, concurrency, requests int, payload []byte) (httpx.BenchmarkResult, []edgeCount, int) {
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
		Payload:     payload,
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
			"benchmark successful+failed requests (%d+%d)", total, res.SuccessfulRequests, res.FailedRequests)
	}
	mismatch := int(total) - res.SuccessfulRequests

	return res, counts, mismatch
}

func startTopology(delays map[string]time.Duration) (*topology.OriginServer, []*topology.EdgeServer, map[string]string) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-003c"})
	if err := origin.Start(); err != nil {
		log.Fatalf("failed to start origin: %v", err)
	}

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

func writeResult(scenario, policy string, r Experiment003CResult) {
	fname := filepath.Join(outDirName, fmt.Sprintf("003C-%s-%s.json", scenario, policy))
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

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 003-C: Least Connections Under Unequal Request Duration")
	fmt.Println("==========================================================================================")

	payload := bytes.Repeat([]byte("L"), 64)

	// ---------------------------------------------------------------
	// Scenario 1: static-heterogeneous — same topology as 002-D/003-B
	// (edge-a=1ms, edge-b=1ms, edge-c=100ms). Tests H3a: can Least
	// Connections match 003-B's hand-configured WRR result without ever
	// being told the 100:1 capacity ratio?
	// ---------------------------------------------------------------
	fmt.Println("\n--- Scenario: static-heterogeneous (H3a) ---")
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

			finding := fmt.Sprintf(
				"%s under static 1ms/1ms/100ms: RPS=%.1f p95=%v p99=%v edge-c share=%.2f%%.",
				p.name, res.ThroughputRPS, res.ClientLatencies.P95, res.ClientLatencies.P99, counts[2].SharePercent)

			writeResult("static-heterogeneous", p.name, Experiment003CResult{
				Experiment: "003-C-least-connections-unequal-duration", Timestamp: time.Now().UTC().Format(time.RFC3339),
				Scenario: "static-heterogeneous", Policy: p.name, Concurrency: concurrency, Requests: requests,
				Phases: []phaseResult{{
					Phase: "measured", SuccessfulRequests: res.SuccessfulRequests, FailedRequests: res.FailedRequests,
					ThroughputRPS: res.ThroughputRPS, ClientLatencies: res.ClientLatencies, PerEdgeCounts: counts,
					ClientServerOutcomeMismatch: mismatch,
				}},
				Findings: finding,
			})

			stopAll(origin, edges, pxy)
		}
	}

	// ---------------------------------------------------------------
	// Scenario 2: dynamic-degradation — all edges start at 1ms (phase 1),
	// then edge-c is degraded to 100ms via SetArtificialDelay mid-run
	// (phase 2), using the SAME proxy/selector/edge instances throughout
	// so any accumulated selector state (WRR's smooth-WRR counters,
	// Least Connections' load tracker) carries over exactly as it would
	// in a real, continuously-running proxy. Tests H3b: do RR and a WRR
	// frozen at its original 1:1:1 configuration keep routing to the
	// now-slow edge, while Least Connections shifts away without being
	// reconfigured?
	// ---------------------------------------------------------------
	fmt.Println("\n--- Scenario: dynamic-degradation (H3b) ---")
	{
		const concurrency = 30
		const requestsPerPhase = 600
		homogeneousDelays := map[string]time.Duration{"edge-a": 1 * time.Millisecond, "edge-b": 1 * time.Millisecond, "edge-c": 1 * time.Millisecond}

		policies := []struct {
			name  string
			build func(pxy *proxy.ReverseProxy, targetsByName map[string]string) proxy.TargetSelector
		}{
			{"round-robin", func(pxy *proxy.ReverseProxy, targetsByName map[string]string) proxy.TargetSelector {
				return proxy.NewRoundRobinSelector()
			}},
			{"wrr-frozen-equal-weights", func(pxy *proxy.ReverseProxy, targetsByName map[string]string) proxy.TargetSelector {
				return proxy.NewWeightedRoundRobinSelector(proxy.TargetWeights{
					targetsByName["edge-a"]: 1, targetsByName["edge-b"]: 1, targetsByName["edge-c"]: 1,
				})
			}},
			{"least-connections", func(pxy *proxy.ReverseProxy, targetsByName map[string]string) proxy.TargetSelector {
				return proxy.NewLeastConnectionsSelector(pxy.LoadTracker())
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
			pxy.SetSelector(p.build(pxy, targetsByName))
			if err := pxy.Start(); err != nil {
				log.Fatalf("failed to start proxy: %v", err)
			}

			_, _ = httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{TargetURL: pxy.URL(), Path: "/data", Requests: 30, Concurrency: concurrency, Payload: payload})
			time.Sleep(100 * time.Millisecond)

			phase1Res, phase1Counts, phase1Mismatch := measurePhase(pxy, edges, concurrency, requestsPerPhase, payload)
			printPhase("phase1-homogeneous", phase1Res, phase1Counts, phase1Mismatch)

			// Degrade edge-c mid-run. No selector is reconfigured.
			edges[indexOf(edgeNames, "edge-c")].SetArtificialDelay(100 * time.Millisecond)
			time.Sleep(50 * time.Millisecond)

			phase2Res, phase2Counts, phase2Mismatch := measurePhase(pxy, edges, concurrency, requestsPerPhase, payload)
			printPhase("phase2-degraded", phase2Res, phase2Counts, phase2Mismatch)

			finding := fmt.Sprintf(
				"%s: phase1 (homogeneous) edge-c share=%.2f%%, RPS=%.1f. phase2 (edge-c degraded to 100ms, no "+
					"reconfiguration) edge-c share=%.2f%%, RPS=%.1f, p95=%v, p99=%v.",
				p.name, phase1Counts[2].SharePercent, phase1Res.ThroughputRPS,
				phase2Counts[2].SharePercent, phase2Res.ThroughputRPS, phase2Res.ClientLatencies.P95, phase2Res.ClientLatencies.P99)

			writeResult("dynamic-degradation", p.name, Experiment003CResult{
				Experiment: "003-C-least-connections-unequal-duration", Timestamp: time.Now().UTC().Format(time.RFC3339),
				Scenario: "dynamic-degradation", Policy: p.name, Concurrency: concurrency, Requests: requestsPerPhase,
				Phases: []phaseResult{
					{Phase: "phase1-homogeneous", SuccessfulRequests: phase1Res.SuccessfulRequests, FailedRequests: phase1Res.FailedRequests,
						ThroughputRPS: phase1Res.ThroughputRPS, ClientLatencies: phase1Res.ClientLatencies, PerEdgeCounts: phase1Counts,
						ClientServerOutcomeMismatch: phase1Mismatch},
					{Phase: "phase2-degraded", SuccessfulRequests: phase2Res.SuccessfulRequests, FailedRequests: phase2Res.FailedRequests,
						ThroughputRPS: phase2Res.ThroughputRPS, ClientLatencies: phase2Res.ClientLatencies, PerEdgeCounts: phase2Counts,
						ClientServerOutcomeMismatch: phase2Mismatch},
				},
				Findings: finding,
			})

			stopAll(origin, edges, pxy)
		}
	}

	// ---------------------------------------------------------------
	// Scenario 3: low-concurrency-signal-starvation — same static
	// heterogeneous topology as Scenario 1, but at c=1 (no concurrent
	// overlap ever possible). Tests H3c: Least Connections should show NO
	// measurable advantage here, because its signal depends on requests
	// actually overlapping in time.
	// ---------------------------------------------------------------
	fmt.Println("\n--- Scenario: low-concurrency-signal-starvation (H3c) ---")
	{
		const concurrency = 1
		const requests = 300
		delays := map[string]time.Duration{"edge-a": 1 * time.Millisecond, "edge-b": 1 * time.Millisecond, "edge-c": 100 * time.Millisecond}

		policies := []struct {
			name       string
			targetsOrd []string // controls tie-break winner order
			build      func(pxy *proxy.ReverseProxy) proxy.TargetSelector
		}{
			{"round-robin", edgeNames, func(pxy *proxy.ReverseProxy) proxy.TargetSelector { return proxy.NewRoundRobinSelector() }},
			{"least-connections", edgeNames, func(pxy *proxy.ReverseProxy) proxy.TargetSelector {
				return proxy.NewLeastConnectionsSelector(pxy.LoadTracker())
			}},
			// Verification cell for the H3c finding below: at c=1, every
			// selection is a tie (sequential execution means the previous
			// request has always fully completed and decremented before
			// the next SelectTarget call), so LeastConnectionsSelector's
			// deterministic first-in-`available`-order tie-break always
			// wins — regardless of that target's actual speed. The first
			// two cells above put the fast edge-a first, making LC look
			// artificially excellent. This cell reorders targets so the
			// SLOW edge-c is first, to confirm the mechanism: if this is
			// really a tie-break-order artifact and not genuine load
			// sensing, LC here should perform as badly as (or worse than)
			// Round Robin, not well.
			{"least-connections-slow-first-order", []string{"edge-c", "edge-a", "edge-b"}, func(pxy *proxy.ReverseProxy) proxy.TargetSelector {
				return proxy.NewLeastConnectionsSelector(pxy.LoadTracker())
			}},
		}

		for _, p := range policies {
			fmt.Printf("\n Policy: %s\n", p.name)
			origin, edges, _ := startTopology(delays)
			var targets []string
			for _, name := range p.targetsOrd {
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

			_, _ = httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{TargetURL: pxy.URL(), Path: "/data", Requests: 15, Concurrency: concurrency, Payload: payload})
			time.Sleep(100 * time.Millisecond)

			res, counts, mismatch := measurePhase(pxy, edges, concurrency, requests, payload)
			printPhase("measured", res, counts, mismatch)

			finding := fmt.Sprintf(
				"%s at c=1 under static 1ms/1ms/100ms: RPS=%.1f p95=%v p99=%v edge-c share=%.2f%%.",
				p.name, res.ThroughputRPS, res.ClientLatencies.P95, res.ClientLatencies.P99, counts[2].SharePercent)

			writeResult("low-concurrency-signal-starvation", p.name, Experiment003CResult{
				Experiment: "003-C-least-connections-unequal-duration", Timestamp: time.Now().UTC().Format(time.RFC3339),
				Scenario: "low-concurrency-signal-starvation", Policy: p.name, Concurrency: concurrency, Requests: requests,
				Phases: []phaseResult{{
					Phase: "measured", SuccessfulRequests: res.SuccessfulRequests, FailedRequests: res.FailedRequests,
					ThroughputRPS: res.ThroughputRPS, ClientLatencies: res.ClientLatencies, PerEdgeCounts: counts,
					ClientServerOutcomeMismatch: mismatch,
				}},
				Findings: finding,
			})

			stopAll(origin, edges, pxy)
		}
	}

	fmt.Println("\nExperiment 003-C complete.")
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
