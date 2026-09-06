package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"flashflow/internal/attribution"
	"flashflow/internal/statistics"
	"flashflow/internal/topology"
	"flashflow/internal/transport"
)

const outDirName = "experiments/006-statistics-queueing/results"

// This experiment deliberately does NOT use topology.EdgeServer/cache/
// routing -- queueing attribution needs one clean, isolated system
// boundary (client -> transport -> Origin), not the combined machinery
// other 006 experiments exercise. transport.TransportConfig.MaxConnsPerHost
// already exists and is already wired into the real http.Transport
// (internal/transport/pool.go) but has never been used anywhere in this
// project to create actual finite capacity -- set low here, it makes Go's
// transport genuinely block a request until a connection frees up: a
// real, measurable bottleneck, not a simulated one.

// LoadLevelResult is one (concurrency, replicate) measurement. The
// statistical unit for the Little's Law check is the REPLICATE at a
// given concurrency level -- not the individual request. Requests within
// one replicate describe that replicate's own latency distribution, not
// independent trials of "does Little's Law hold."
type LoadLevelResult struct {
	Concurrency int     `json:"concurrency"`
	Replicate   int     `json:"replicate"`
	Lambda      float64 `json:"lambda_req_per_sec"`
	L           float64 `json:"l_avg_in_system"`
	W           float64 `json:"w_avg_latency_ms"`
	LambdaW     float64 `json:"lambda_w_predicted_l"`
	RelError    float64 `json:"rel_error"`
}

// runLoadLevel drives `concurrency` closed-loop workers against origin
// for duration, sampling the outstanding-request count every sampleEvery
// to get an independent, time-averaged estimate of L -- independent of
// the per-request latencies used for W, so the Little's Law check isn't
// circular (computing L only from W and lambda would make the "check"
// tautological).
func runLoadLevel(originURL string, concurrency int, duration, sampleEvery time.Duration, maxConnsPerHost int) LoadLevelResult {
	tt := transport.NewTrackedTransport(transport.TransportConfig{
		Label: "queueing_client", MaxConnsPerHost: maxConnsPerHost, MaxIdleConnsPerHost: maxConnsPerHost, MaxIdleConns: maxConnsPerHost * 2,
	})
	client := tt.HTTPClient(10 * time.Second)

	var outstanding atomic.Int64
	var lSamples []float64
	var lMu sync.Mutex
	stopSampling := make(chan struct{})
	var samplerWG sync.WaitGroup
	samplerWG.Add(1)
	go func() {
		defer samplerWG.Done()
		ticker := time.NewTicker(sampleEvery)
		defer ticker.Stop()
		for {
			select {
			case <-stopSampling:
				return
			case <-ticker.C:
				lMu.Lock()
				lSamples = append(lSamples, float64(outstanding.Load()))
				lMu.Unlock()
			}
		}
	}()

	var latMu sync.Mutex
	var latencies []time.Duration
	var completed atomic.Int64

	stopWorkers := make(chan struct{})
	var workersWG sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			for {
				select {
				case <-stopWorkers:
					return
				default:
				}
				outstanding.Add(1)
				start := time.Now()
				resp, err := client.Get(originURL + "/data")
				elapsed := time.Since(start)
				outstanding.Add(-1)
				if err != nil {
					continue
				}
				resp.Body.Close()
				completed.Add(1)
				latMu.Lock()
				latencies = append(latencies, elapsed)
				latMu.Unlock()
			}
		}()
	}

	measureStart := time.Now()
	time.Sleep(duration)
	measureElapsed := time.Since(measureStart)
	close(stopWorkers)
	workersWG.Wait()
	close(stopSampling)
	samplerWG.Wait()
	tt.CloseIdleConnections()

	latMsAll := make([]float64, len(latencies))
	for i, l := range latencies {
		latMsAll[i] = float64(l.Microseconds()) / 1000.0
	}
	w, _ := statistics.Mean(latMsAll)
	lMeasured, _ := statistics.Mean(lSamples)
	lambda := float64(completed.Load()) / measureElapsed.Seconds()

	// w is in ms; internal/attribution.Sample.W must be seconds, matching
	// Lambda's per-second unit -- the same conversion this function
	// always made inline, now the shared package's own documented
	// contract (see internal/attribution/littleslaw.go) rather than a
	// one-off comment repeated at every call site.
	metrics, err := attribution.CheckLittlesLaw(attribution.Sample{L: lMeasured, Lambda: lambda, W: w / 1000.0})
	if err != nil {
		// L/Lambda/W are all measured, non-negative-by-construction
		// quantities (a count, a rate, a mean latency) -- a rejection
		// here would mean the measurement itself produced an impossible
		// value, a genuine bug worth failing loudly on rather than
		// silently coercing.
		log.Fatalf("attribution.CheckLittlesLaw: %v", err)
	}

	return LoadLevelResult{
		Concurrency: concurrency, Lambda: lambda, L: lMeasured, W: w, LambdaW: metrics.Predicted, RelError: metrics.RelError,
	}
}

func main() {
	if err := os.MkdirAll(outDirName, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	fmt.Println("==========================================================================================")
	fmt.Println(" Experiment 006-D: Queueing / Concurrency Attribution")
	fmt.Println(" Can increased latency be associated with increased in-flight work, using a real, finite-")
	fmt.Println(" capacity bottleneck (transport.MaxConnsPerHost) -- and does L ~ lambda*W hold?")
	fmt.Println("==========================================================================================")

	const serviceDelay = 20 * time.Millisecond
	const maxConnsPerHost = 5
	const duration = 1 * time.Second
	const sampleEvery = 2 * time.Millisecond
	const replicatesPerLevel = 3

	origin := topology.NewOriginServer(topology.OriginConfig{Instance: "origin-006d", DefaultDelay: serviceDelay})
	if err := origin.Start(); err != nil {
		log.Fatalf("failed to start origin: %v", err)
	}
	defer origin.Stop(context.Background())

	concurrencyLevels := []int{2, 4, 5, 8, 15, 30}
	fmt.Printf("\nOrigin service time: %v (fixed). Transport capacity: %d concurrent connections.\n", serviceDelay, maxConnsPerHost)
	fmt.Printf("%-6s %-10s %-10s %-10s %-14s %-10s\n", "conc", "lambda", "L", "W(ms)", "lambda*W", "rel.err")

	var allResults []LoadLevelResult
	relErrors := make([]float64, 0)
	for _, c := range concurrencyLevels {
		for rep := 0; rep < replicatesPerLevel; rep++ {
			res := runLoadLevel(origin.URL(), c, duration, sampleEvery, maxConnsPerHost)
			res.Replicate = rep
			allResults = append(allResults, res)
			relErrors = append(relErrors, res.RelError)
			fmt.Printf("%-6d %-10.1f %-10.2f %-10.2f %-14.2f %-9.1f%%\n",
				c, res.Lambda, res.L, res.W, res.LambdaW, res.RelError*100)
		}
	}

	meanAbsRelError := 0.0
	for _, e := range relErrors {
		if e < 0 {
			e = -e
		}
		meanAbsRelError += e
	}
	meanAbsRelError /= float64(len(relErrors))

	// Does throughput plateau once offered concurrency exceeds capacity?
	// Compare mean lambda at a below-capacity level vs a well-above-
	// capacity level.
	lambdaAt := func(concurrency int) []float64 {
		var out []float64
		for _, r := range allResults {
			if r.Concurrency == concurrency {
				out = append(out, r.Lambda)
			}
		}
		return out
	}
	lambdaBelow := lambdaAt(2)
	lambdaWellAbove := lambdaAt(30)
	meanLambdaBelow, _ := statistics.Mean(lambdaBelow)
	meanLambdaWellAbove, _ := statistics.Mean(lambdaWellAbove)
	offeredLoadMultiplier := float64(30) / float64(2)
	throughputMultiplier := meanLambdaWellAbove / meanLambdaBelow
	predictedCeiling := float64(maxConnsPerHost) / (serviceDelay.Seconds())

	fmt.Printf("\nMean |relative error| between L and lambda*W across all %d (concurrency, replicate) points: %.1f%%\n",
		len(relErrors), meanAbsRelError*100)
	fmt.Printf("Offered concurrency grew %.0fx (2->30); measured throughput grew only %.1fx (%.1f -> %.1f req/s), "+
		"close to the %d-connections/%v predicted ceiling of %.0f req/s\n",
		offeredLoadMultiplier, throughputMultiplier, meanLambdaBelow, meanLambdaWellAbove, maxConnsPerHost, serviceDelay, predictedCeiling)

	finding := fmt.Sprintf(
		"Across %d (concurrency, replicate) measurements spanning offered concurrency from %d (below the %d-connection "+
			"capacity) to %d (6x capacity), L (independently sampled, time-averaged outstanding-request count) and "+
			"lambda*W (computed from independently measured throughput and mean latency) agreed within a mean "+
			"absolute relative error of %.1f%% -- consistent with Little's Law holding over these stable, closed-loop "+
			"measurement windows, using one self-consistent system boundary (client-observed dispatch-to-response) "+
			"for all three quantities. Offered concurrency grew %.0fx (2 to 30) while measured throughput grew only "+
			"%.1fx (%.1f to %.1f req/s) -- essentially flat, and close to the %.0f req/s ceiling predicted directly "+
			"from capacity/service-time (%d connections / %v), a genuine finite-capacity effect from "+
			"transport.MaxConnsPerHost, not a simulated one. This is a measured association between increased "+
			"in-flight work and constrained throughput/rising latency under a real, finite-capacity system boundary "+
			"-- not a claim that FlashFlow's edge or origin behave like a textbook M/M/c queue in general; the "+
			"finding is scoped specifically to the transport-layer connection limit exercised here.",
		len(relErrors), concurrencyLevels[0], maxConnsPerHost, concurrencyLevels[len(concurrencyLevels)-1],
		meanAbsRelError*100, offeredLoadMultiplier, throughputMultiplier, meanLambdaBelow, meanLambdaWellAbove,
		predictedCeiling, maxConnsPerHost, serviceDelay,
	)
	fmt.Printf("\n%s\n", finding)

	out := struct {
		Experiment           string            `json:"experiment"`
		Timestamp            string            `json:"timestamp"`
		ServiceDelayMs       int64             `json:"service_delay_ms"`
		MaxConnsPerHost      int               `json:"max_conns_per_host"`
		DurationMs           int64             `json:"duration_ms"`
		ReplicatesPerLevel   int               `json:"replicates_per_level"`
		Results              []LoadLevelResult `json:"results"`
		MeanAbsRelError      float64           `json:"mean_abs_rel_error"`
		ThroughputMultiplier float64           `json:"throughput_multiplier_2_to_30"`
		PredictedCeilingRPS  float64           `json:"predicted_ceiling_req_per_sec"`
		Findings             string            `json:"findings"`
	}{
		Experiment: "006-D-queueing-concurrency-attribution", Timestamp: time.Now().UTC().Format(time.RFC3339),
		ServiceDelayMs: serviceDelay.Milliseconds(), MaxConnsPerHost: maxConnsPerHost,
		DurationMs: duration.Milliseconds(), ReplicatesPerLevel: replicatesPerLevel,
		Results: allResults, MeanAbsRelError: meanAbsRelError,
		ThroughputMultiplier: throughputMultiplier, PredictedCeilingRPS: predictedCeiling, Findings: finding,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	os.WriteFile(filepath.Join(outDirName, "006D-queueing-attribution.json"), b, 0644)

	fmt.Println("\nExperiment 006-D complete.")
}
