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

	"flashflow/internal/tcp"
)

// benchConfig is a single cell in the experiment matrix.
type benchConfig struct {
	Concurrency int
	Requests    int
	PayloadSize int
	Mode        string
}

// fullResult is the on-disk record for one measured iteration.
type fullResult struct {
	Experiment   string `json:"experiment"`
	Mode         string `json:"connection_mode"`
	Concurrency  int    `json:"concurrency"`
	PayloadSize  int    `json:"payload_bytes"`
	Iteration    int    `json:"iteration"`
	tcp.ClientResult
}

func main() {
	outDir := filepath.Join("experiments", "001-tcp-connection-lifecycle", "results")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		log.Fatalf("failed to create results dir: %v", err)
	}

	concurrencies := []int{1, 10, 100}
	requests := []int{1000, 10000}
	payloads := []int{32, 1024}
	modes := []string{"persistent", "per-request"}
	iterations := 5

	// Build the full matrix of cells.
	var cells []benchConfig
	for _, c := range concurrencies {
		for _, r := range requests {
			for _, p := range payloads {
				for _, m := range modes {
					cells = append(cells, benchConfig{c, r, p, m})
				}
			}
		}
	}

	// Randomise order to reduce order-dependent bias from earlier cells.
	// TIME_WAIT state from a heavy per-request run could contaminate the
	// immediately-following persistent run if the order were fixed.
	rand.Shuffle(len(cells), func(i, j int) { cells[i], cells[j] = cells[j], cells[i] })

	totalMeasured := 0
	totalWarmups := 0

	for cellIdx, cell := range cells {
		payloadBytes := bytes.Repeat([]byte("A"), cell.PayloadSize)

		// --- Fresh server per configuration cell ---
		//
		// Using a single long-lived server across all cells would allow
		// TIME_WAIT socket state from earlier (especially per-request) cells
		// to contaminate the latency measurements of later cells.
		// A fresh server on :0 guarantees a clean TCP context per cell.
		server := tcp.NewServer("127.0.0.1:0")
		if err := server.Start(); err != nil {
			log.Fatalf("failed to start server for cell %d: %v", cellIdx, err)
		}
		addr := server.AddrPort()

		fmt.Printf("[%02d/%02d] mode=%-11s c=%-3d r=%-5d p=%-4d  addr=%s\n",
			cellIdx+1, len(cells), cell.Mode, cell.Concurrency, cell.Requests, cell.PayloadSize, addr)

		// Warmup — discard, but count for methodology transparency.
		warmupReqs := cell.Requests / 10
		if warmupReqs < 50 {
			warmupReqs = 50
		}
		warmupCfg := tcp.ClientConfig{
			Addr:           addr,
			Requests:       warmupReqs,
			Concurrency:    cell.Concurrency,
			Payload:        payloadBytes,
			ConnectionMode: cell.Mode,
		}
		_, _ = tcp.RunBenchmark(warmupCfg)
		totalWarmups++

		// Measured iterations.
		for i := 1; i <= iterations; i++ {
			cfg := tcp.ClientConfig{
				Addr:           addr,
				Requests:       cell.Requests,
				Concurrency:    cell.Concurrency,
				Payload:        payloadBytes,
				ConnectionMode: cell.Mode,
			}
			res, err := tcp.RunBenchmark(cfg)
			if err != nil {
				log.Printf("  iter %d failed: %v", i, err)
				continue
			}

			out := fullResult{
				Experiment:   "001-tcp-connection-lifecycle",
				Mode:         cell.Mode,
				Concurrency:  cell.Concurrency,
				PayloadSize:  cell.PayloadSize,
				Iteration:    i,
				ClientResult: res,
			}
			filename := filepath.Join(outDir,
				fmt.Sprintf("c%03d-r%05d-p%04d-%s-iter%d.json",
					cell.Concurrency, cell.Requests, cell.PayloadSize, cell.Mode, i))
			b, _ := json.MarshalIndent(out, "", "  ")
			if err := os.WriteFile(filename, b, 0644); err != nil {
				log.Printf("  failed to write result: %v", err)
			}
			totalMeasured++
		}

		// Stop the server and let the OS reclaim its port before the next cell.
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := server.Stop(stopCtx); err != nil {
			log.Printf("  server stop error: %v", err)
		}
		cancel()

		// Cooldown: allow any TIME_WAIT sockets to begin draining.
		// This does not eliminate TIME_WAIT (it lasts ~2×MSL = 60–120s on most
		// systems), but it prevents each cell from *immediately* inheriting the
		// congestion of the previous cell.
		//
		// Finding: this cooldown is itself a demonstration that connection
		// lifecycle state persists beyond the visible benchmark duration.
		time.Sleep(200 * time.Millisecond)
	}

	fmt.Printf("\nDone. Measured runs: %d, Discarded warmups: %d, Total invocations: %d\n",
		totalMeasured, totalWarmups, totalMeasured+totalWarmups)
}
