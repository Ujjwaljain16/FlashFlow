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
)

func main() {
	addr := flag.String("addr", ":8000", "Address to listen on")
	instance := flag.String("instance", "origin-1", "Origin instance identity")
	delayMs := flag.Int("delay-ms", 0, "Artificial application processing delay in milliseconds")
	flag.Parse()

	cfg := topology.OriginConfig{
		Addr:         *addr,
		Instance:     *instance,
		DefaultDelay: time.Duration(*delayMs) * time.Millisecond,
	}

	server := topology.NewOriginServer(cfg)
	if err := server.Start(); err != nil {
		log.Fatalf("failed to start origin server: %v", err)
	}

	log.Printf("[FlashFlow Origin] Running %s listening on %s (default delay: %v)", *instance, server.AddrPort(), cfg.DefaultDelay)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	log.Printf("[FlashFlow Origin] Shutting down %s...", *instance)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Stop(ctx); err != nil {
		log.Printf("error during shutdown: %v", err)
	}
	log.Printf("[FlashFlow Origin] %s stopped cleanly.", *instance)
}
