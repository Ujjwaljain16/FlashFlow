package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"flashflow/internal/tcp"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:9000", "Address to listen on")
	flag.Parse()

	server := tcp.NewServer(*addr)
	if err := server.Start(); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}

	log.Printf("TCP Server Listening on %s", server.AddrPort())

	// Ticker to periodically log metrics
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			stats := server.Tracker.Snapshot()
			log.Printf("\n--- TCP Server Stats ---\naccepted_connections: %d\nactive_connections: %d\nclosed_connections: %d\nmessages_received: %d\nmessages_sent: %d\nerrors: %d\nbytes_received: %d\nbytes_sent: %d\n",
				stats.TotalAccepted, stats.ActiveConns, stats.TotalClosed,
				stats.MessagesRecv, stats.MessagesSent, stats.Errors,
				stats.BytesRecv, stats.BytesSent)
		case <-sigCh:
			log.Println("Shutting down server...")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Stop(ctx); err != nil {
				log.Printf("error during shutdown: %v", err)
			}
			return
		}
	}
}
