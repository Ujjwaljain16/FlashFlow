package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"flashflow/internal/topology"
	"flashflow/internal/transport"
)

func main() {
	addr := flag.String("addr", ":8001", "Address to listen on")
	instance := flag.String("instance", "edge-a", "Edge instance identity (e.g. edge-a, edge-b, edge-c)")
	originURL := flag.String("origin", "http://127.0.0.1:8000", "Target origin URL")
	delayMs := flag.Int("delay-ms", 0, "Artificial application delay on this edge in milliseconds")
	flag.Parse()

	tCfg := transport.DefaultTransportConfig("edge_origin_" + *instance)
	cfg := topology.EdgeConfig{
		Addr:            *addr,
		Instance:        *instance,
		OriginURL:       *originURL,
		DefaultDelay:    time.Duration(*delayMs) * time.Millisecond,
		TransportConfig: tCfg,
	}

	server, err := topology.NewEdgeServer(cfg)
	if err != nil {
		log.Fatalf("failed to initialize edge server: %v", err)
	}

	if err := server.Start(); err != nil {
		log.Fatalf("failed to start edge server: %v", err)
	}

	log.Printf("[FlashFlow Edge] Running %s on %s -> Origin %s (delay: %v)", *instance, server.AddrPort(), *originURL, cfg.DefaultDelay)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			st := server.TransportStats()
			log.Printf("[FlashFlow Edge %s] Origin Transport Stats: dials=%d active=%d reqs=%d reqs/conn=%.2f",
				*instance, st.SuccessfulDials, st.ActiveConns, st.RequestsCompleted, st.RequestsPerConn)
		case <-sigCh:
			log.Printf("[FlashFlow Edge] Shutting down %s...", *instance)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Stop(ctx); err != nil {
				log.Printf("error during edge shutdown: %v", err)
			}
			log.Printf("[FlashFlow Edge] %s stopped cleanly.", *instance)
			return
		}
	}
}
