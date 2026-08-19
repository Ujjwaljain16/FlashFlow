package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"

	"flashflow/internal/tcp"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9000", "Server address")
	requests := flag.Int("requests", 1000, "Number of requests to make")
	concurrency := flag.Int("concurrency", 10, "Number of concurrent workers")
	payloadSize := flag.Int("payload", 32, "Payload size in bytes")
	connMode := flag.String("connection-mode", "persistent", "Connection mode: 'persistent' or 'per-request'")
	outFormat := flag.String("output", "text", "Output format: 'text' or 'json'")
	
	flag.Parse()

	if *connMode != "persistent" && *connMode != "per-request" {
		log.Fatalf("invalid connection mode: %s", *connMode)
	}

	payload := bytes.Repeat([]byte("A"), *payloadSize)

	cfg := tcp.ClientConfig{
		Addr:           *addr,
		Requests:       *requests,
		Concurrency:    *concurrency,
		Payload:        payload,
		ConnectionMode: *connMode,
	}

	res, err := tcp.RunBenchmark(cfg)
	if err != nil {
		log.Fatalf("benchmark failed: %v", err)
	}

	if *outFormat == "json" {
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Printf("\n--- TCP Client Benchmark Result ---\n")
		fmt.Printf("Connection Mode: %s\n", *connMode)
		fmt.Printf("Concurrency:     %d\n", *concurrency)
		fmt.Printf("Requests:        %d\n", res.TotalRequests)
		fmt.Printf("Payload:         %d bytes\n\n", *payloadSize)
		
		fmt.Printf("Connections:     %d\n", res.ConnectionsMade)
		var sr float64
		if res.TotalRequests > 0 {
			sr = float64(res.SuccessfulRequests)/float64(res.TotalRequests)*100
		}
		fmt.Printf("Success rate:    %d/%d (%.2f%%)\n", res.SuccessfulRequests, res.TotalRequests, sr)
		fmt.Printf("Total Duration:  %v\n", res.TotalDuration)
		fmt.Printf("Throughput:      %.2f RPS\n\n", res.ThroughputRPS)
		
		fmt.Printf("p50 req:         %v\n", res.P50ReqLatency)
		fmt.Printf("p95 req:         %v\n", res.P95ReqLatency)
		fmt.Printf("p99 req:         %v\n", res.P99ReqLatency)
		fmt.Printf("Min req:         %v\n", res.MinReqLatency)
		fmt.Printf("Max req:         %v\n", res.MaxReqLatency)
		if res.P50ConnLatency > 0 {
			fmt.Printf("\np50 dial:        %v\n", res.P50ConnLatency)
			fmt.Printf("p95 dial:        %v\n", res.P95ConnLatency)
			fmt.Printf("p99 dial:        %v\n", res.P99ConnLatency)
		}
	}
}
