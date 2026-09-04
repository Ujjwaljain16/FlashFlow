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
	Phase              string                   `json:"phase"`
	SuccessfulRequests int                      `json:"successful_requests"`
	FailedRequests     int                      `json:"failed_requests"`
	ErrorRatePercent   float64                  `json:"error_rate_percent"`
	ThroughputRPS      float64                  `json:"throughput_rps"`
	ClientLatencies    httpx.LatencyPercentiles `json:"client_latencies"`
	PerEdgeCounts      []edgeCount              `json:"per_edge_counts"`
}

type Experiment003FResult struct {
	Experiment string        `json:"experiment"`
	Timestamp  string        `json:"timestamp"`
	Scenario   string        `json:"scenario"`
	Policy     string        `json:"policy"`
	Phases     []phaseResult `json:"phases"`
	Findings   string        `json:"findings"`
}

const outDirName = "experiments/003-routing-policies/results"

var edgeNames = []string{"edge-a", "edge-b", "edge-c"}

// measurePhase runs one measured benchmark phase. Unlike prior 003-*
// experiments, it does NOT treat client/server outcome mismatch or
// nonzero failures as contamination to fatal out on — the Failure
// scenario in this experiment deliberately produces real request
// failures (a killed edge), so this helper reports them as data instead.
func measurePhase(pxy *proxy.ReverseProxy, edges []*topology.EdgeServer, concurrency, requests int, payload []byte) (httpx.BenchmarkResult, []edgeCount) {
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

	return res, counts
}

func startTopology(delays map[string]time.Duration) (*topology.OriginServer, []*topology.EdgeServer, map[string]string) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-003f"})
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

func stopRemaining(origin *topology.OriginServer, edges []*topology.EdgeServer, pxy *proxy.ReverseProxy) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	pxy.Stop(ctx)
	for _, e := range edges {
		e.Stop(ctx) // no-op if already stopped
	}
	origin.Stop(ctx)
	cancel()
	time.Sleep(150 * time.Millisecond)
}

func writeResult(scenario, policy string, r Experiment003FResult) {
	fname := filepath.Join(outDirName, fmt.Sprintf("003F-%s-%s.json", scenario, policy))
	b, _ := json.MarshalIndent(r, "", "  ")
	os.WriteFile(fname, b, 0644)
}

func printPhase(label string, res httpx.BenchmarkResult, counts []edgeCount) {
	errRate := 0.0
	if res.TotalRequests > 0 {
		errRate = 100 * float64(res.FailedRequests) / float64(res.TotalRequests)
	}
	fmt.Printf("  [%s] RPS=%.1f p50=%v p95=%v p99=%v errors=%d/%d (%.2f%%)\n", label, res.ThroughputRPS,
		res.ClientLatencies.P50, res.ClientLatencies.P95, res.ClientLatencies.P99, res.FailedRequests, res.TotalRequests, errRate)
	for _, c := range counts {
		fmt.Printf("    %-8s proxy=%-4d edge_forwarded=%-4d share=%.2f%% agree=%t\n",
			c.Instance, c.ProxyRecordedCount, c.EdgeForwardedCount, c.SharePercent, c.CountsAgree)
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

// buildSelector constructs the named policy's selector against pxy and
// targetsByName. wrrWeights must match the ACTUAL topology in force for
// the calling scenario — reusing the heterogeneous-tuned 100:100:1
// weights from Experiment 003-B against a homogeneous topology would
// silently confound "WRR configured wrong for this topology" with
// "WRR's failure-handling behavior", which are different questions.
func buildSelector(name string, pxy *proxy.ReverseProxy, targetsByName map[string]string, seed int64, wrrWeights proxy.TargetWeights) proxy.TargetSelector {
	switch name {
	case "round-robin":
		return proxy.NewRoundRobinSelector()
	case "wrr":
		return proxy.NewWeightedRoundRobinSelector(wrrWeights)
	case "least-connections":
		return proxy.NewLeastConnectionsSelector(pxy.LoadTracker())
	case "ewma":
		return proxy.NewEWMASelector(pxy.LatencyTracker())
	case "p2c-latency":
		return proxy.NewP2CSelector(proxy.ScorerFromLatency(pxy.LatencyTracker()), rand.New(rand.NewSource(seed)))
	}
	log.Fatalf("unknown policy %q", name)
	return nil
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 003-F: Comparison Matrix — Burst and Failure")
	fmt.Println("==========================================================================================")

	payload := bytes.Repeat([]byte("F"), 64)
	const seed = 20260905

	// ---------------------------------------------------------------
	// Scenario: burst (H6a) — heterogeneous topology (1ms/1ms/100ms),
	// baseline -> sudden concurrency spike (c=150, far above the c=30
	// used throughout 003-B-E) -> cooldown. Does routing quality hold
	// up under much heavier concurrent contention on the documented
	// read-then-write races?
	// ---------------------------------------------------------------
	fmt.Println("\n--- Scenario: burst (H6a) ---")
	{
		delays := map[string]time.Duration{"edge-a": 1 * time.Millisecond, "edge-b": 1 * time.Millisecond, "edge-c": 100 * time.Millisecond}
		policies := []string{"round-robin", "wrr", "least-connections", "ewma", "p2c-latency"}

		for _, policyName := range policies {
			fmt.Printf("\n Policy: %s\n", policyName)
			origin, edges, targetsByName := startTopology(delays)
			var targets []string
			for _, name := range edgeNames {
				targets = append(targets, targetsByName[name])
			}
			clk := clock.NewWallClock()
			pxy := proxy.NewReverseProxy(proxy.Config{
				Targets:         targets,
				TransportConfig: transport.TransportConfig{Label: "proxy_upstream", MaxIdleConnsPerHost: 200, MaxIdleConns: 600},
				HealthConfig:    health.DefaultConfig(),
				ProberConfig:    health.DefaultCheckerConfig(),
			}, clk, nil)
			pxy.SetSelector(buildSelector(policyName, pxy, targetsByName, seed, proxy.TargetWeights{
				targetsByName["edge-a"]: 100, targetsByName["edge-b"]: 100, targetsByName["edge-c"]: 1,
			}))
			if err := pxy.Start(); err != nil {
				log.Fatalf("failed to start proxy: %v", err)
			}

			_, _ = httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{TargetURL: pxy.URL(), Path: "/data", Requests: 20, Concurrency: 10, Payload: payload})
			time.Sleep(100 * time.Millisecond)

			baseRes, baseCounts := measurePhase(pxy, edges, 10, 150, payload)
			printPhase("baseline-c10", baseRes, baseCounts)

			burstRes, burstCounts := measurePhase(pxy, edges, 150, 1500, payload)
			printPhase("burst-c150", burstRes, burstCounts)

			cooldownRes, cooldownCounts := measurePhase(pxy, edges, 10, 150, payload)
			printPhase("cooldown-c10", cooldownRes, cooldownCounts)

			finding := fmt.Sprintf(
				"%s: edge-c share baseline=%.1f%% burst=%.1f%% cooldown=%.1f%%. RPS baseline=%.1f burst=%.1f cooldown=%.1f. "+
					"Burst p99=%v. Errors: baseline=%d burst=%d cooldown=%d.",
				policyName, baseCounts[2].SharePercent, burstCounts[2].SharePercent, cooldownCounts[2].SharePercent,
				baseRes.ThroughputRPS, burstRes.ThroughputRPS, cooldownRes.ThroughputRPS, burstRes.ClientLatencies.P99,
				baseRes.FailedRequests, burstRes.FailedRequests, cooldownRes.FailedRequests)

			writeResult("burst", policyName, Experiment003FResult{
				Experiment: "003-F-comparison-matrix", Timestamp: time.Now().UTC().Format(time.RFC3339),
				Scenario: "burst", Policy: policyName,
				Phases: []phaseResult{
					phaseFrom("baseline-c10", baseRes, baseCounts),
					phaseFrom("burst-c150", burstRes, burstCounts),
					phaseFrom("cooldown-c10", cooldownRes, cooldownCounts),
				},
				Findings: finding,
			})
			stopRemaining(origin, edges, pxy)
		}
	}

	// ---------------------------------------------------------------
	// Scenario: failure (H6b) — homogeneous 3-edge topology (isolating
	// failure-handling from the heterogeneity question), edge-b is
	// hard-killed (listener closed, not just slowed) mid-run. A fast
	// health-checker interval is used so detection completes within the
	// measured phase rather than spilling into a much longer wait.
	// ---------------------------------------------------------------
	fmt.Println("\n--- Scenario: failure (H6b) ---")
	{
		delays := map[string]time.Duration{"edge-a": 1 * time.Millisecond, "edge-b": 1 * time.Millisecond, "edge-c": 1 * time.Millisecond}
		policies := []string{"round-robin", "wrr", "least-connections", "ewma", "p2c-latency"}
		fastProber := health.CheckerConfig{Interval: 50 * time.Millisecond, Timeout: 30 * time.Millisecond, Path: "/health"}

		for _, policyName := range policies {
			fmt.Printf("\n Policy: %s\n", policyName)
			origin, edges, targetsByName := startTopology(delays)
			var targets []string
			for _, name := range edgeNames {
				targets = append(targets, targetsByName[name])
			}
			clk := clock.NewWallClock()
			pxy := proxy.NewReverseProxy(proxy.Config{
				Targets:         targets,
				TransportConfig: transport.TransportConfig{Label: "proxy_upstream", MaxIdleConnsPerHost: 100, MaxIdleConns: 300},
				HealthConfig:    health.DefaultConfig(),
				ProberConfig:    fastProber,
			}, clk, nil)
			// Homogeneous topology in this scenario -> equal weights, NOT
			// the heterogeneous-tuned 100:100:1 used in the burst
			// scenario above (see buildSelector's doc comment).
			pxy.SetSelector(buildSelector(policyName, pxy, targetsByName, seed, proxy.TargetWeights{
				targetsByName["edge-a"]: 1, targetsByName["edge-b"]: 1, targetsByName["edge-c"]: 1,
			}))
			if err := pxy.Start(); err != nil {
				log.Fatalf("failed to start proxy: %v", err)
			}

			_, _ = httpx.RunHTTPBenchmark(httpx.BenchmarkConfig{TargetURL: pxy.URL(), Path: "/data", Requests: 20, Concurrency: 20, Payload: payload})
			time.Sleep(100 * time.Millisecond)

			beforeRes, beforeCounts := measurePhase(pxy, edges, 20, 400, payload)
			printPhase("before-failure", beforeRes, beforeCounts)

			// Hard-kill edge-b: listener closed, connections refused.
			killCtx, killCancel := context.WithTimeout(context.Background(), 1*time.Second)
			edges[indexOf(edgeNames, "edge-b")].Stop(killCtx)
			killCancel()

			// Measured immediately, with no settle delay, so the transient
			// error spike during detection is captured as part of this
			// phase rather than hidden by waiting it out first.
			duringRes, duringCounts := measurePhase(pxy, edges, 20, 400, payload)
			printPhase("during-and-after-kill", duringRes, duringCounts)

			finding := fmt.Sprintf(
				"%s: before-failure edge-b share=%.1f%%. After hard-killing edge-b: %d/%d requests failed (%.2f%%) "+
					"during the detection+redistribution window; surviving share a=%.1f%% b=%.1f%% c=%.1f%%.",
				policyName, beforeCounts[1].SharePercent, duringRes.FailedRequests, duringRes.TotalRequests,
				100*float64(duringRes.FailedRequests)/float64(duringRes.TotalRequests),
				duringCounts[0].SharePercent, duringCounts[1].SharePercent, duringCounts[2].SharePercent)

			writeResult("failure", policyName, Experiment003FResult{
				Experiment: "003-F-comparison-matrix", Timestamp: time.Now().UTC().Format(time.RFC3339),
				Scenario: "failure", Policy: policyName,
				Phases: []phaseResult{
					phaseFrom("before-failure", beforeRes, beforeCounts),
					phaseFrom("during-and-after-kill", duringRes, duringCounts),
				},
				Findings: finding,
			})
			stopRemaining(origin, edges, pxy)
		}
	}

	fmt.Println("\nExperiment 003-F complete.")
}

func phaseFrom(label string, res httpx.BenchmarkResult, counts []edgeCount) phaseResult {
	errRate := 0.0
	if res.TotalRequests > 0 {
		errRate = 100 * float64(res.FailedRequests) / float64(res.TotalRequests)
	}
	return phaseResult{
		Phase: label, SuccessfulRequests: res.SuccessfulRequests, FailedRequests: res.FailedRequests,
		ErrorRatePercent: errRate, ThroughputRPS: res.ThroughputRPS, ClientLatencies: res.ClientLatencies, PerEdgeCounts: counts,
	}
}
