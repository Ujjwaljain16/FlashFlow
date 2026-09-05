package main

import (
	"net/http"
	"testing"
	"time"
)

// TestNewServer_HasReadHeaderTimeout regression-tests F-12: the dashboard's
// http.Server was constructed with no timeouts at all, letting a slow or
// silent client hold a per-connection goroutine open indefinitely.
func TestNewServer_HasReadHeaderTimeout(t *testing.T) {
	srv := newServer("127.0.0.1:0", http.NewServeMux())
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatalf("expected a positive ReadHeaderTimeout, got %v", srv.ReadHeaderTimeout)
	}
	if srv.ReadHeaderTimeout > time.Minute {
		t.Fatalf("ReadHeaderTimeout %v is unexpectedly large for a request-header bound", srv.ReadHeaderTimeout)
	}
}

// TestAddrFlag_DefaultsToLoopback regression-tests F-21: the dashboard
// used to default to a bare ":7070" (binding every interface), reachable
// from anything on the same network despite being a no-auth, single-user
// local development tool.
func TestDefaultAddr_BindsLoopbackOnly(t *testing.T) {
	if defaultAddr != "127.0.0.1:7070" {
		t.Fatalf("expected the default bind address to be loopback-only, got %q", defaultAddr)
	}
}
