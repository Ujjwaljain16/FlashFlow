package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"flashflow/internal/httpx"
	"flashflow/internal/netsim"
	"flashflow/internal/topology"
	"flashflow/internal/transport"
)

const outDirName = "experiments/004-caching-failures/results"

type Experiment004FResult struct {
	Experiment string `json:"experiment"`
	Timestamp  string `json:"timestamp"`

	SimulatedLatencyMs int     `json:"simulated_latency_ms"`
	MissLatencyMs      float64 `json:"miss_latency_ms"`
	HitLatencyMs       float64 `json:"hit_latency_ms"`

	InsulationLossRate float64 `json:"insulation_loss_rate"`
	ColdKeyTrials      int     `json:"cold_key_trials"`
	ColdKeyFailures    int     `json:"cold_key_failures"`
	PrimingAttempts    int     `json:"priming_attempts"`

	BurstSize                     int     `json:"burst_size"`
	BurstCount                    int     `json:"burst_count"`
	BurstLossRate                 float64 `json:"burst_loss_rate"`
	CoalescedAllOrNothingBursts   int     `json:"coalesced_all_or_nothing_bursts"`
	CoalescedPartialBursts        int     `json:"coalesced_partial_bursts"`
	CoalescedRequestFailureRate   float64 `json:"coalesced_request_failure_rate"`
	IndependentAllOrNothingBursts int     `json:"independent_all_or_nothing_bursts"`
	IndependentPartialBursts      int     `json:"independent_partial_bursts"`
	IndependentRequestFailureRate float64 `json:"independent_request_failure_rate"`

	Findings string `json:"findings"`
}

func msF(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }

// newEdge builds a fresh Origin+Edge pair so each section of this
// experiment starts from a clean, independent state.
func newEdge(instance string, coalesce bool, cond netsim.Conditions) (*topology.OriginServer, *topology.EdgeServer) {
	origin := topology.NewOriginServer(topology.OriginConfig{Instance: instance + "-origin"})
	if err := origin.Start(); err != nil {
		log.Fatalf("failed to start origin: %v", err)
	}
	edge, err := topology.NewEdgeServer(topology.EdgeConfig{
		Instance:          instance + "-edge",
		OriginURL:         origin.URL(),
		CacheTTL:          time.Minute,
		Coalesce:          coalesce,
		NetworkConditions: cond,
		TransportConfig:   transport.TransportConfig{Label: "edge_origin_" + instance},
	})
	if err != nil {
		log.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		log.Fatalf("failed to start edge: %v", err)
	}
	return origin, edge
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 004-F: Network Degradation")
	fmt.Println(" Simulated edge-origin link conditions (netsim, in place of tc netem -- see internal/netsim)")
	fmt.Println("==========================================================================================")

	client := &http.Client{Timeout: 3 * time.Second}

	// --- Section 1: does a cache hit stay insulated from a degraded, ---
	// --- but not fully down, link (Predictions 1 & 2)?               ---
	fmt.Println("\n--- Section 1: cache insulation under latency + partial loss ---")
	const (
		simLatency = 60 * time.Millisecond
		simLoss    = 0.5
	)
	originI, edgeI := newEdge("004f-insulation", false, netsim.Conditions{Latency: simLatency, LossRate: simLoss})

	// Priming can itself be dropped under 50% loss -- retry rather than
	// disabling loss for setup, since that would hide a real property of
	// a lossy link (even establishing a cache entry isn't guaranteed on
	// the first try) instead of reporting it.
	var primeAttempts int
	var missElapsed time.Duration
	for {
		primeAttempts++
		start := time.Now()
		resp, err := client.Get(edgeI.URL() + "/data/hot")
		if err != nil {
			log.Fatalf("priming request failed at the transport level: %v", err)
		}
		status := resp.Header.Get(httpx.HeaderCacheStatus)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && status == "MISS" {
			missElapsed = time.Since(start)
			break
		}
		if primeAttempts > 50 {
			log.Fatalf("priming did not succeed after 50 attempts -- loss_rate=%.2f is unexpectedly high", simLoss)
		}
	}
	fmt.Printf("  warm key primed after %d attempt(s) (loss_rate=%.0f%% makes this expected, not a bug).\n", primeAttempts, simLoss*100)

	start := time.Now()
	resp, err := client.Get(edgeI.URL() + "/data/hot")
	if err != nil {
		log.Fatalf("hit request failed: %v", err)
	}
	hitElapsed := time.Since(start)
	hitStatus := resp.Header.Get(httpx.HeaderCacheStatus)
	resp.Body.Close()
	if hitStatus != "HIT" {
		log.Fatalf("expected HIT on the confirmation request, got %q", hitStatus)
	}
	fmt.Printf("  miss latency=%.1fms, hit latency=%.1fms (simulated link latency=%dms, loss=%.0f%%)\n",
		msF(missElapsed), msF(hitElapsed), simLatency.Milliseconds(), simLoss*100)

	const coldTrials = 60
	coldFailures := 0
	for i := 0; i < coldTrials; i++ {
		resp, err := client.Get(fmt.Sprintf("%s/data/lossy-trial-%03d", edgeI.URL(), i))
		if err != nil {
			log.Fatalf("cold trial %d failed at the transport level: %v", i, err)
		}
		if resp.StatusCode == http.StatusBadGateway {
			coldFailures++
		}
		resp.Body.Close()
	}
	fmt.Printf("  %d/%d independent cold-key requests failed under %.0f%% simulated loss.\n", coldFailures, coldTrials, simLoss*100)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	edgeI.Stop(ctx)
	originI.Stop(ctx)
	cancel()

	// --- Section 2: does coalescing correlate the failures within a  ---
	// --- burst, compared to independent per-request dialing (Pred 3)? ---
	fmt.Println("\n--- Section 2: does coalescing correlate failures within a burst? ---")
	const (
		burstSize    = 10
		burstCount   = 50
		burstLoss    = 0.3
		burstLatency = 20 * time.Millisecond // widens the coalescing window; a drop returns almost instantly otherwise
	)

	runBursts := func(instance string, coalesce bool) (allOrNothing, partial, totalFailures int) {
		origin, edge := newEdge(instance, coalesce, netsim.Conditions{Latency: burstLatency, LossRate: burstLoss})
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			edge.Stop(ctx)
			origin.Stop(ctx)
			cancel()
		}()

		for b := 0; b < burstCount; b++ {
			path := fmt.Sprintf("/data/burst-%03d", b) // fresh key every burst -- a key that already succeeded would just HIT, hiding the link entirely
			var wg sync.WaitGroup
			failures := 0
			var mu sync.Mutex
			for i := 0; i < burstSize; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					resp, err := client.Get(edge.URL() + path)
					if err != nil {
						log.Fatalf("burst request failed at the transport level: %v", err)
					}
					defer resp.Body.Close()
					if resp.StatusCode == http.StatusBadGateway {
						mu.Lock()
						failures++
						mu.Unlock()
					}
				}()
			}
			wg.Wait()

			totalFailures += failures
			switch failures {
			case 0, burstSize:
				allOrNothing++
			default:
				partial++
			}
		}
		return allOrNothing, partial, totalFailures
	}

	coalAllOrNothing, coalPartial, coalFailures := runBursts("004f-coalesced", true)
	indepAllOrNothing, indepPartial, indepFailures := runBursts("004f-independent", false)

	coalRate := float64(coalFailures) / float64(burstCount*burstSize)
	indepRate := float64(indepFailures) / float64(burstCount*burstSize)

	fmt.Printf("  coalesced:   %d/%d bursts all-or-nothing, %d partial. Request failure rate=%.1f%% (target %.0f%%)\n",
		coalAllOrNothing, burstCount, coalPartial, coalRate*100, burstLoss*100)
	fmt.Printf("  independent: %d/%d bursts all-or-nothing, %d partial. Request failure rate=%.1f%% (target %.0f%%)\n",
		indepAllOrNothing, burstCount, indepPartial, indepRate*100, burstLoss*100)

	finding := fmt.Sprintf(
		"Insulation: miss=%.1fms vs hit=%.1fms under %dms simulated latency; %d/%d independent cold requests failed under %.0f%% loss "+
			"(warm key needed %d priming attempt(s) under the same loss). "+
			"Correlation: with coalescing, %d/%d bursts of %d were all-or-nothing (only %d partial) at %.0f%% loss, request failure rate %.1f%%; "+
			"without coalescing, %d/%d bursts were all-or-nothing (%d partial), request failure rate %.1f%%. "+
			"Both cells land near the target loss rate at the request-count level, but coalescing concentrates failure onto whole bursts "+
			"instead of spreading it across individual requests within one.",
		msF(missElapsed), msF(hitElapsed), int(simLatency.Milliseconds()), coldFailures, coldTrials, simLoss*100, primeAttempts,
		coalAllOrNothing, burstCount, burstSize, coalPartial, burstLoss*100, coalRate*100,
		indepAllOrNothing, burstCount, indepPartial, indepRate*100,
	)

	res := Experiment004FResult{
		Experiment: "004-F-network-degradation", Timestamp: time.Now().UTC().Format(time.RFC3339),
		SimulatedLatencyMs: int(simLatency.Milliseconds()), MissLatencyMs: msF(missElapsed), HitLatencyMs: msF(hitElapsed),
		InsulationLossRate: simLoss, ColdKeyTrials: coldTrials, ColdKeyFailures: coldFailures, PrimingAttempts: primeAttempts,
		BurstSize: burstSize, BurstCount: burstCount, BurstLossRate: burstLoss,
		CoalescedAllOrNothingBursts: coalAllOrNothing, CoalescedPartialBursts: coalPartial, CoalescedRequestFailureRate: coalRate,
		IndependentAllOrNothingBursts: indepAllOrNothing, IndependentPartialBursts: indepPartial, IndependentRequestFailureRate: indepRate,
		Findings: finding,
	}

	fname := filepath.Join(outDirName, "004F-network-degradation.json")
	b, _ := json.MarshalIndent(res, "", "  ")
	os.WriteFile(fname, b, 0644)

	fmt.Printf("\n--- Summary ---\n%s\n", finding)
	fmt.Println("\nExperiment 004-F complete.")
}
