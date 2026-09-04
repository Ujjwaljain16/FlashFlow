package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"flashflow/internal/cache"
	"flashflow/internal/clock"
	"flashflow/internal/vtime"
)

const outDirName = "experiments/005-virtual-time/results"

// step records one cache operation's outcome against what the scenario
// expected, so a mismatch is caught immediately rather than discovered
// later by eyeballing a trace.
type step struct {
	VirtualTimeMs float64 `json:"virtual_time_ms"`
	Action        string  `json:"action"`
	Key           string  `json:"key"`
	Expected      string  `json:"expected"`
	Got           string  `json:"got"`
	Match         bool    `json:"match"`
}

type Experiment005CResult struct {
	Experiment         string  `json:"experiment"`
	Timestamp          string  `json:"timestamp"`
	TTLMs              int64   `json:"ttl_ms"`
	Steps              []step  `json:"steps"`
	RealElapsedMs      float64 `json:"real_elapsed_ms"`
	AllExpectationsMet bool    `json:"all_expectations_met"`
	Findings           string  `json:"findings"`
}

func msF(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 005-C: Virtual Cache Expiration")
	fmt.Println(" Can Stage 4's cache.Cache -- unmodified -- run correctly under virtual time?")
	fmt.Println("==========================================================================================")

	const ttl = 100 * time.Millisecond
	const key = "GET /data/hot"

	e := vtime.NewEngine(0)
	// cache.Cache is reused with zero modification: it already takes an
	// injected clock.Clock, and Engine.Clock() satisfies that directly.
	c := cache.New(e.Clock(), ttl)

	var steps []step
	record := func(action, expected, got string) {
		match := expected == got
		steps = append(steps, step{
			VirtualTimeMs: msF(time.Duration(e.Now())), Action: action, Key: key, Expected: expected, Got: got, Match: match,
		})
		e.Record(action, key, map[string]any{"expected": expected, "got": got, "match": match})
		if !match {
			log.Fatalf("MISMATCH at t=%.0fms: %s expected %s, got %s", msF(time.Duration(e.Now())), action, expected, got)
		}
	}

	// t=0ms: entry created.
	e.Schedule(0, func() {
		c.Set(key, &cache.Entry{StatusCode: 200, Body: []byte("v1"), StoredAt: e.Now()})
		record("set", "FILLED", "FILLED")
	})

	// t=99ms: still within TTL -- must HIT.
	e.Schedule(clock.VirtualTime(99*time.Millisecond), func() {
		_, ok := c.Get(key)
		got := "MISS"
		if ok {
			got = "HIT"
		}
		record("get", "HIT", got)
	})

	// t=101ms: past the 100ms TTL -- must MISS, then refill.
	e.Schedule(clock.VirtualTime(101*time.Millisecond), func() {
		_, ok := c.Get(key)
		got := "MISS"
		if ok {
			got = "HIT"
		}
		record("get", "MISS", got)

		c.Set(key, &cache.Entry{StatusCode: 200, Body: []byte("v2"), StoredAt: e.Now()})
		record("set", "FILLED", "FILLED")
	})

	// t=150ms: freshly refilled at t=101ms with a 100ms TTL -- must HIT
	// again, proving the refill itself started a fresh TTL window rather
	// than inheriting the expired entry's clock.
	e.Schedule(clock.VirtualTime(150*time.Millisecond), func() {
		_, ok := c.Get(key)
		got := "MISS"
		if ok {
			got = "HIT"
		}
		record("get", "HIT", got)
	})

	realStart := time.Now()
	if err := e.RunUntilEmpty(); err != nil {
		log.Fatalf("scenario failed: %v", err)
	}
	realElapsed := time.Since(realStart)

	fmt.Println("\nSteps:")
	allMet := true
	for _, s := range steps {
		status := "OK"
		if !s.Match {
			status = "MISMATCH"
			allMet = false
		}
		fmt.Printf("  t=%-6.1fms %-4s key=%-16s expected=%-6s got=%-6s [%s]\n",
			s.VirtualTimeMs, s.Action, s.Key, s.Expected, s.Got, status)
	}

	finalStats := c.Snapshot()
	finding := fmt.Sprintf(
		"All %d cache operations under virtual time matched their expected outcome with zero real sleeping "+
			"(scenario completed in %.3fms of real time). Final cache stats: %+v. cache.Cache required no "+
			"modification -- it already took an injected clock.Clock before this stage existed.",
		len(steps), msF(realElapsed), finalStats,
	)
	fmt.Printf("\n%s\n", finding)

	res := Experiment005CResult{
		Experiment: "005-C-virtual-cache-expiration", Timestamp: time.Now().UTC().Format(time.RFC3339),
		TTLMs: ttl.Milliseconds(), Steps: steps, RealElapsedMs: msF(realElapsed),
		AllExpectationsMet: allMet, Findings: finding,
	}
	fname := filepath.Join(outDirName, "005C-virtual-cache-expiration.json")
	b, _ := json.MarshalIndent(res, "", "  ")
	os.WriteFile(fname, b, 0644)

	traceFname := filepath.Join(outDirName, "005C-trace.jsonl")
	if err := e.Trace().WriteJSONLFile(traceFname); err != nil {
		log.Fatalf("failed to write trace: %v", err)
	}

	fmt.Println("\nExperiment 005-C complete.")
}
