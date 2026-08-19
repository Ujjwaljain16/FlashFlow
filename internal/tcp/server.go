package tcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// Server represents a raw TCP server for measuring connection lifecycles.
type Server struct {
	Addr    string
	Tracker *Tracker

	listener    net.Listener
	wg          sync.WaitGroup
	quit        chan struct{}
	conns       map[net.Conn]struct{} // active connection set for graceful shutdown
	connsMu     sync.Mutex
}

func NewServer(addr string) *Server {
	return &Server{
		Addr:    addr,
		Tracker: &Tracker{},
		quit:    make(chan struct{}),
		conns:   make(map[net.Conn]struct{}),
	}
}

// Start begins listening and accepting connections.
func (s *Server) Start() error {
	l, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	s.listener = l

	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// AddrPort returns the actual bound address (useful when listening on :0).
func (s *Server) AddrPort() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.Addr
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.quit:
				return // expected: listener was closed by Stop()
			default:
			}

			// Distinguish temporary from permanent listener errors.
			// A permanent error (e.g., file descriptor exhaustion) would
			// create a tight spin loop if we continue blindly.
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				// Temporary: give the OS a moment and retry.
				time.Sleep(5 * time.Millisecond)
				continue
			}

			// Non-temporary, non-shutdown: log and stop accepting.
			log.Printf("acceptLoop: permanent error, stopping: %v", err)
			return
		}

		s.connsMu.Lock()
		s.conns[conn] = struct{}{}
		s.connsMu.Unlock()

		s.Tracker.IncAccepted()
		s.wg.Add(1)
		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.connsMu.Lock()
		delete(s.conns, conn)
		s.connsMu.Unlock()
		conn.Close()
		s.Tracker.IncClosed()
	}()

	// Connection lifecycle:
	//   ACCEPT → READ → RESPOND → READ → ... → EOF → CLOSE

	for {
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Minute)); err != nil {
			s.Tracker.IncError()
			return
		}

		payload, err := ReadMessage(conn)
		if err != nil {
			if err != io.EOF {
				s.Tracker.IncError()
			}
			return
		}
		s.Tracker.AddBytesRecv(int64(4 + len(payload)))
		s.Tracker.IncMsgRecv()

		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Minute)); err != nil {
			s.Tracker.IncError()
			return
		}
		if err := WriteMessage(conn, payload); err != nil {
			s.Tracker.IncError()
			return
		}
		s.Tracker.AddBytesSent(int64(4 + len(payload)))
		s.Tracker.IncMsgSent()
	}
}

// Stop gracefully shuts down the server.
//
// Shutdown sequence:
//  1. Close the listener so no new connections are accepted.
//  2. Close all currently active connections so their goroutines see an error
//     and exit — rather than waiting up to 5 minutes for read deadlines.
//  3. Wait for all goroutines to finish, or until ctx is cancelled.
//
// This is a foundational lesson for Stage 2: a reverse proxy must close
// upstream and downstream connections during shutdown, not just stop accepting.
func (s *Server) Stop(ctx context.Context) error {
	close(s.quit)

	var listenerErr error
	if s.listener != nil {
		listenerErr = s.listener.Close()
	}

	// Close all active connections so their goroutines unblock immediately.
	s.connsMu.Lock()
	for conn := range s.conns {
		conn.Close()
	}
	s.connsMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return listenerErr
	case <-ctx.Done():
		return fmt.Errorf("server shutdown timed out: %w", ctx.Err())
	}
}
