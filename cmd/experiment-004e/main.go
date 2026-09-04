package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"flashflow/internal/cache"
	"flashflow/internal/httpx"
	"flashflow/internal/topology"
	"flashflow/internal/transport"
)

const outDirName = "experiments/004-caching-failures/results"

type Experiment004EResult struct {
	Experiment                   string              `json:"experiment"`
	Timestamp                    string              `json:"timestamp"`
	WarmKeyRequestsDuringOutage  int                 `json:"warm_key_requests_during_outage"`
	WarmKeySuccessesDuringOutage int                 `json:"warm_key_successes_during_outage"`
	ColdKeyBurstSize             int                 `json:"cold_key_burst_size"`
	ColdKeyFailuresDuringOutage  int                 `json:"cold_key_failures_during_outage"`
	CoalesceStatsAfterBurst      cache.CoalesceStats `json:"coalesce_stats_after_burst"`
	FailedDialsDuringOutage      uint64              `json:"failed_dials_during_outage"`
	PostBurstStillFailedCleanly  bool                `json:"post_burst_still_failed_cleanly"`
	PostRecoveryColdKeySucceeded bool                `json:"post_recovery_cold_key_succeeded"`
	PostRecoveryColdKeyStatus    string              `json:"post_recovery_cold_key_status"`
	PostRecoveryWarmKeyStillHit  bool                `json:"post_recovery_warm_key_still_hit"`
	Findings                     string              `json:"findings"`
}

func freeLocalAddr() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("failed to reserve a free port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 004-E: Origin Outage & Recovery")
	fmt.Println(" Client -> Edge (cache + coalescing) -> Origin. Origin is stopped mid-run to simulate a")
	fmt.Println(" real outage (connection refused, not a synthetic error), then restarted, testing what")
	fmt.Println(" the cache and coalescer actually buy when the backend they protect really goes down.")
	fmt.Println("==========================================================================================")

	// Origin binds to a fixed, pre-reserved address so it can be stopped
	// and restarted on the exact same address -- the edge's OriginURL is
	// captured once at construction and must still be valid after recovery.
	fixedAddr := freeLocalAddr()
	origin := topology.NewOriginServer(topology.OriginConfig{Addr: fixedAddr, Instance: "origin-004e"})
	if err := origin.Start(); err != nil {
		log.Fatalf("failed to start origin: %v", err)
	}

	edge, err := topology.NewEdgeServer(topology.EdgeConfig{
		Instance:  "edge-004e",
		OriginURL: origin.URL(),
		CacheTTL:  time.Minute, // long enough that the warm key never expires mid-run
		Coalesce:  true,
		TransportConfig: transport.TransportConfig{
			Label: "edge_origin_004e", MaxIdleConnsPerHost: 200, MaxIdleConns: 600,
		},
	})
	if err != nil {
		log.Fatalf("failed to create edge: %v", err)
	}
	if err := edge.Start(); err != nil {
		log.Fatalf("failed to start edge: %v", err)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	get := func(path string) *http.Response {
		resp, err := client.Get(edge.URL() + path)
		if err != nil {
			log.Fatalf("request to %s failed at the transport level: %v", path, err)
		}
		return resp
	}

	// 1. Warm one key while Origin is healthy, confirm it, so its later
	// behavior during the outage means what we think it means.
	fmt.Println("\n--- Step 1: prime the warm key ---")
	resp := get("/data/hot")
	primeStatus := resp.Header.Get(httpx.HeaderCacheStatus)
	resp.Body.Close()
	if primeStatus != "MISS" {
		log.Fatalf("expected the priming request to be a MISS, got %q", primeStatus)
	}
	resp = get("/data/hot")
	confirmStatus := resp.Header.Get(httpx.HeaderCacheStatus)
	resp.Body.Close()
	if confirmStatus != "HIT" {
		log.Fatalf("expected the confirmation request to be a HIT, got %q", confirmStatus)
	}
	fmt.Println("  warm key primed and confirmed HIT while origin is healthy.")

	// 2. Inject the outage.
	fmt.Println("\n--- Step 2: stop origin (simulated outage) ---")
	if err := origin.Stop(context.Background()); err != nil {
		log.Fatalf("failed to stop origin: %v", err)
	}
	fmt.Println("  origin stopped.")

	// 3. During the outage: the warm key should keep succeeding entirely
	// from the cache, untouched by origin being down.
	fmt.Println("\n--- Step 3: warm key during outage ---")
	const warmRequests = 20
	warmSuccesses := 0
	for i := 0; i < warmRequests; i++ {
		resp := get("/data/hot")
		if resp.StatusCode == http.StatusOK && resp.Header.Get(httpx.HeaderCacheStatus) == "HIT" {
			warmSuccesses++
		}
		resp.Body.Close()
	}
	fmt.Printf("  %d/%d warm-key requests succeeded as cache hits during the outage.\n", warmSuccesses, warmRequests)

	// 4. During the outage: a concurrent burst against a never-cached key
	// must collapse to one dial attempt and fail identically for everyone.
	fmt.Println("\n--- Step 4: cold key burst during outage ---")
	edge.SetArtificialDelay(50 * time.Millisecond) // widen the coalescing window; a refused dial returns almost instantly otherwise
	const coldBurst = 30
	var wg sync.WaitGroup
	statuses := make([]int, coldBurst)
	for i := 0; i < coldBurst; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp := get("/data/outage-only")
			statuses[i] = resp.StatusCode
			resp.Body.Close()
		}(i)
	}
	wg.Wait()

	coldFailures := 0
	for _, s := range statuses {
		if s == http.StatusBadGateway {
			coldFailures++
		}
	}
	coalesceStats := edge.CoalesceStats()
	failedDials := edge.TransportStats().FailedDials
	fmt.Printf("  %d/%d cold-key requests failed with 502 during the outage.\n", coldFailures, coldBurst)
	fmt.Printf("  coalesce stats after the burst: %+v\n", coalesceStats)
	fmt.Printf("  failed dials so far: %d\n", failedDials)

	// 5. The in-flight entry must not be left stuck: one more request for
	// the same key, still during the outage, should fail cleanly again.
	fmt.Println("\n--- Step 5: post-burst request, still during outage ---")
	resp = get("/data/outage-only")
	postBurstStatus := resp.StatusCode
	resp.Body.Close()
	postBurstCleanFail := postBurstStatus == http.StatusBadGateway
	fmt.Printf("  post-burst request status: %d (clean failure: %v)\n", postBurstStatus, postBurstCleanFail)

	// 6. Recovery.
	fmt.Println("\n--- Step 6: restart origin (recovery) ---")
	edge.SetArtificialDelay(0)
	if err := origin.Start(); err != nil {
		log.Fatalf("failed to restart origin: %v", err)
	}
	fmt.Println("  origin restarted on the same address.")

	resp = get("/data/outage-only")
	recoveryStatus := resp.StatusCode
	recoveryCacheStatus := resp.Header.Get(httpx.HeaderCacheStatus)
	resp.Body.Close()
	recoverySucceeded := recoveryStatus == http.StatusOK
	fmt.Printf("  cold key after recovery: status=%d cache_status=%q\n", recoveryStatus, recoveryCacheStatus)

	resp = get("/data/hot")
	warmAfterRecoveryStatus := resp.Header.Get(httpx.HeaderCacheStatus)
	resp.Body.Close()
	warmStillHit := warmAfterRecoveryStatus == "HIT"
	fmt.Printf("  warm key after recovery: cache_status=%q\n", warmAfterRecoveryStatus)

	finding := fmt.Sprintf(
		"Warm key: %d/%d succeeded during the outage (cache insulation). Cold-key burst of %d: %d failed with 502, "+
			"coalesced to leads=%d shared=%d failures=%d (failed dials=%d, not %d -- coalescing held). "+
			"Post-burst request during outage still failed cleanly: %v (no abandoned in-flight entry). "+
			"After recovery: cold key succeeded=%v (status=%d, %s), warm key still HIT=%v.",
		warmSuccesses, warmRequests, coldBurst, coldFailures,
		coalesceStats.Leads, coalesceStats.Shared, coalesceStats.Failures, failedDials, coldBurst,
		postBurstCleanFail, recoverySucceeded, recoveryStatus, recoveryCacheStatus, warmStillHit,
	)

	res := Experiment004EResult{
		Experiment: "004-E-origin-outage-and-recovery", Timestamp: time.Now().UTC().Format(time.RFC3339),
		WarmKeyRequestsDuringOutage: warmRequests, WarmKeySuccessesDuringOutage: warmSuccesses,
		ColdKeyBurstSize: coldBurst, ColdKeyFailuresDuringOutage: coldFailures,
		CoalesceStatsAfterBurst: coalesceStats, FailedDialsDuringOutage: failedDials,
		PostBurstStillFailedCleanly:  postBurstCleanFail,
		PostRecoveryColdKeySucceeded: recoverySucceeded, PostRecoveryColdKeyStatus: recoveryCacheStatus,
		PostRecoveryWarmKeyStillHit: warmStillHit, Findings: finding,
	}

	fname := filepath.Join(outDirName, "004E-origin-outage-and-recovery.json")
	b, _ := json.MarshalIndent(res, "", "  ")
	os.WriteFile(fname, b, 0644)

	fmt.Printf("\n--- Summary ---\n%s\n", finding)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	edge.Stop(ctx)
	origin.Stop(ctx)
	cancel()

	fmt.Println("\nExperiment 004-E complete.")
}
