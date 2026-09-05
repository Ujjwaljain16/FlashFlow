// Command experiment-008h is a small, deliberately modest HTTP
// measurement client: fixed request count, light concurrency, no
// saturation sweep. It exists to support one narrow comparison --
// FlashFlow's real proxy path against NGINX, both fronting the
// identical origin, at light load -- explicitly a reference point, not
// a load test and not a claim about which system handles more traffic.
// See scripts/nginx-reference-benchmark.sh, which orchestrates both
// runs and prints the comparison with that framing stated plainly.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"flashflow/internal/statistics"
)

type Result struct {
	Label               string  `json:"label"`
	URL                 string  `json:"url"`
	Requests            int     `json:"requests"`
	Concurrency         int     `json:"concurrency"`
	Warmup              int     `json:"warmup"`
	Completed           int     `json:"completed"`
	Errors              int     `json:"errors"`
	ThroughputReqPerSec float64 `json:"throughput_req_per_sec"`
	MeanMs              float64 `json:"mean_ms"`
	P50Ms               float64 `json:"p50_ms"`
	P95Ms               float64 `json:"p95_ms"`
	P99Ms               float64 `json:"p99_ms"`
}

// Caveat is the mandatory framing every combined comparison this
// program produces carries -- stated once here so it can never be
// accidentally omitted from one output path but not another.
const Caveat = "Reference point only, at light load (a fixed request count, modest concurrency) through a single " +
	"backend. Not a claim that FlashFlow replaces NGINX or matches its production maturity -- FlashFlow's " +
	"research capabilities (deterministic virtual execution, counterfactual replay, adaptive routing, " +
	"statistical analysis, automatic tuning) are outside the scope of this proxy-performance comparison."

type Comparison struct {
	Experiment string `json:"experiment"`
	A          Result `json:"a"`
	B          Result `json:"b"`
	Caveat     string `json:"caveat"`
}

func main() {
	url := flag.String("url", "", "URL to benchmark")
	label := flag.String("label", "", "label for this run (e.g. \"nginx\" or \"flashflow-proxy\")")
	requests := flag.Int("requests", 200, "total requests to send")
	concurrency := flag.Int("concurrency", 10, "concurrent in-flight requests -- deliberately modest, light load only")
	warmup := flag.Int("warmup", 20, "requests to send and discard before the timed run, to reach steady-state connection reuse")
	out := flag.String("out", "", "write JSON result to this file (in addition to stdout)")
	compareA := flag.String("compare-a", "", "combine two previously-written -out result files instead of benchmarking: path to the first")
	compareB := flag.String("compare-b", "", "combine two previously-written -out result files instead of benchmarking: path to the second")
	compareOut := flag.String("compare-out", "", "write the combined comparison JSON to this file")
	flag.Parse()

	if *compareA != "" || *compareB != "" {
		runCompare(*compareA, *compareB, *compareOut)
		return
	}

	if *url == "" || *label == "" {
		log.Fatal("both -url and -label are required (or use -compare-a/-compare-b to combine existing results)")
	}

	// A custom Transport with MaxIdleConnsPerHost raised to at least
	// *concurrency: Go's http.DefaultTransport caps this at 2, which
	// under concurrency=10 forces most requests onto a freshly-dialed
	// connection rather than a reused keep-alive one -- identically for
	// both sides of a paired comparison (this is the same binary
	// benchmarking both), so it never biased FlashFlow vs. NGINX against
	// each other, but it did mean neither side's number reflected
	// steady-state, connection-reuse behavior, which is what a
	// reference point at "light load" should actually measure.
	transport := &http.Transport{MaxIdleConnsPerHost: *concurrency}
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}

	get := func() (int, time.Duration, error) {
		reqStart := time.Now()
		resp, err := client.Get(*url)
		elapsed := time.Since(reqStart)
		if err != nil {
			return 0, elapsed, err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode, elapsed, nil
	}

	if *warmup > 0 {
		fmt.Printf("Warming up (%d requests, discarded)...\n", *warmup)
		for i := 0; i < *warmup; i++ {
			get()
		}
	}

	sem := make(chan struct{}, *concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var latenciesMs []float64
	completed, errCount := 0, 0

	start := time.Now()
	for i := 0; i < *requests; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			status, elapsed, err := get()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errCount++
				return
			}
			// A fast error response (e.g. a misconfigured proxy
			// returning 502 immediately) must never be counted as a
			// fast success. This alone would NOT have caught the
			// actual failure the paired NGINX comparison first hit --
			// a Windows/Git-Bash path-mangling bug caused NGINX to
			// silently serve its own default welcome page (a genuine
			// HTTP 200) instead of proxying to Origin at all, producing
			// a suspiciously fast "successful" result. That class of
			// bug (wrong backend reached, still a 2xx) can only be
			// caught by checking response CONTENT, not status alone --
			// see scripts/nginx-reference-benchmark.sh's own readiness
			// check, which verifies both endpoints actually return
			// Origin's real response shape before benchmarking either.
			if status < 200 || status >= 300 {
				errCount++
				return
			}
			completed++
			latenciesMs = append(latenciesMs, float64(elapsed.Microseconds())/1000.0)
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	result := Result{
		Label: *label, URL: *url, Requests: *requests, Concurrency: *concurrency, Warmup: *warmup,
		Completed: completed, Errors: errCount,
		ThroughputReqPerSec: float64(completed) / elapsed.Seconds(),
	}
	if len(latenciesMs) > 0 {
		result.MeanMs, _ = statistics.Mean(latenciesMs)
		result.P50Ms, _ = statistics.Percentile(latenciesMs, 50)
		result.P95Ms, _ = statistics.Percentile(latenciesMs, 95)
		result.P99Ms, _ = statistics.Percentile(latenciesMs, 99)
	}

	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(b))
	if *out != "" {
		if err := os.WriteFile(*out, b, 0644); err != nil {
			log.Fatalf("writing -out file: %v", err)
		}
	}
}

// runCompare loads two previously-written Result files and prints a
// side-by-side table plus, if requested, writes the combined
// comparison JSON -- kept in this same Go binary rather than reaching
// for python3/jq as a new dependency this project otherwise has no use
// for; Go is the one toolchain every step of this project already
// requires.
func runCompare(pathA, pathB, outPath string) {
	if pathA == "" || pathB == "" {
		log.Fatal("both -compare-a and -compare-b are required together")
	}
	a := loadResult(pathA)
	b := loadResult(pathB)

	fmt.Printf("%-22s %18s %18s\n", "", a.Label, b.Label)
	rows := []struct {
		name   string
		va, vb float64
	}{
		{"throughput (req/s)", a.ThroughputReqPerSec, b.ThroughputReqPerSec},
		{"mean (ms)", a.MeanMs, b.MeanMs},
		{"p50 (ms)", a.P50Ms, b.P50Ms},
		{"p95 (ms)", a.P95Ms, b.P95Ms},
		{"p99 (ms)", a.P99Ms, b.P99Ms},
		{"errors", float64(a.Errors), float64(b.Errors)},
	}
	for _, r := range rows {
		fmt.Printf("%-22s %18.2f %18.2f\n", r.name, r.va, r.vb)
	}
	fmt.Printf("\n%s\n", Caveat)

	if outPath != "" {
		combined := Comparison{Experiment: "008-H-nginx-reference-benchmark", A: a, B: b, Caveat: Caveat}
		cb, _ := json.MarshalIndent(combined, "", "  ")
		if err := os.WriteFile(outPath, cb, 0644); err != nil {
			log.Fatalf("writing -compare-out file: %v", err)
		}
	}
}

func loadResult(path string) Result {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("reading %s: %v", path, err)
	}
	var r Result
	if err := json.Unmarshal(data, &r); err != nil {
		log.Fatalf("parsing %s: %v", path, err)
	}
	return r
}
