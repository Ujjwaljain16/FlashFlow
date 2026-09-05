// Command dashboard is FlashFlow's operator interface (master context
// rules 29-35): a Go HTTP server exposing the Playground (run/compare
// a routing policy against a canonical scenario, live), an experiment
// artifact browser, and a tuning view -- reading the same artifacts
// and driving the same internal/replay engine every experiment already
// uses, never a second source of truth. The dashboard is an interface
// over the laboratory, not the laboratory itself: every CLI command
// under cmd/experiment-* remains fully independent of this binary.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed static
var staticFiles embed.FS

// defaultAddr binds localhost only -- this is a no-auth, single-user local
// development tool (see docs/StageArtifacts/Stage8.md); a bare ":7070"
// binds every interface, making its experiment browser and Playground
// reachable from anywhere on the same network, not just this machine.
// Pass an explicit -addr to opt into a wider bind.
const defaultAddr = "127.0.0.1:7070"

func main() {
	addr := flag.String("addr", defaultAddr, "dashboard listen address")
	flag.Parse()

	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("embedding static assets: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("/api/experiments", handleListGroups)
	mux.HandleFunc("/api/experiments/", handleExperimentPath)
	mux.HandleFunc("/api/playground/policies", handlePolicies)
	mux.HandleFunc("/api/playground/run", handleRun)
	mux.HandleFunc("/api/playground/compare", handleCompare)
	mux.HandleFunc("/api/tuning", handleTuning)

	server := newServer(*addr, mux)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("FlashFlow dashboard shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("dashboard shutdown error: %v", err)
		}
	}()

	log.Printf("FlashFlow dashboard listening on %s", *addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("dashboard server error: %v", err)
	}
}

// newServer builds the dashboard's *http.Server, factored out of main so
// its configuration (notably ReadHeaderTimeout, F-12/F-21's fix) has a
// direct test independent of flag parsing/signal handling.
func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writing JSON response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
