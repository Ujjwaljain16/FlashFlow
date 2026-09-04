package tcp

import (
	"net"
	"sync"
	"testing"
	"time"
)

func TestServer_LifecycleAndCounters(t *testing.T) {
	server := NewServer("127.0.0.1:0")
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	addr := server.AddrPort()

	// Connect 5 clients
	var wg sync.WaitGroup
	var conns []net.Conn
	for i := 0; i < 5; i++ {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("failed to connect: %v", err)
		}
		conns = append(conns, conn)

		// Send a message
		msg := []byte("ping")
		WriteMessage(conn, msg)

		// Wait for response in a goroutine
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			resp, _ := ReadMessage(c)
			if string(resp) != "ping" {
				t.Errorf("expected ping, got %s", string(resp))
			}
		}(conn)
	}

	wg.Wait()

	// Wait a moment for server stats to settle
	time.Sleep(50 * time.Millisecond)

	stats := server.Tracker.Snapshot()
	if stats.ActiveConns != 5 {
		t.Errorf("expected 5 active connections, got %d", stats.ActiveConns)
	}
	if stats.TotalAccepted != 5 {
		t.Errorf("expected 5 total accepted, got %d", stats.TotalAccepted)
	}
	if stats.MessagesRecv != 5 {
		t.Errorf("expected 5 messages received, got %d", stats.MessagesRecv)
	}

	// Close all connections
	for _, c := range conns {
		c.Close()
	}

	// Wait for server to process closures
	time.Sleep(50 * time.Millisecond)

	stats = server.Tracker.Snapshot()
	if stats.ActiveConns != 0 {
		t.Errorf("expected 0 active connections after close, got %d", stats.ActiveConns)
	}
	if stats.TotalClosed != 5 {
		t.Errorf("expected 5 total closed, got %d", stats.TotalClosed)
	}

	// Verify tracker invariants after clean shutdown.
	// ActiveConns == TotalAccepted - TotalClosed
	stats = server.Tracker.Snapshot()
	if stats.ActiveConns != stats.TotalAccepted-stats.TotalClosed {
		t.Errorf("invariant violated: ActiveConns(%d) != Accepted(%d) - Closed(%d)",
			stats.ActiveConns, stats.TotalAccepted, stats.TotalClosed)
	}
	if stats.MessagesSent > stats.MessagesRecv {
		t.Errorf("invariant violated: MessagesSent(%d) > MessagesRecv(%d)",
			stats.MessagesSent, stats.MessagesRecv)
	}
}
