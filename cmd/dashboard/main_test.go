package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestWithSecurityHeaders_SetsCSPAndFriends regression-tests an
// independent audit finding: the dashboard sent no security headers at
// all. Checks the specific properties that matter, not exact string
// equality, so the CSP's own exact policy can evolve without this test
// becoming brittle.
func TestWithSecurityHeaders_SetsCSPAndFriends(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	withSecurityHeaders(inner).ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("expected a Content-Security-Policy header, got none")
	}
	if !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("expected script-src 'self' in the CSP, got %q", csp)
	}
	// script-src must stay strict (no inline, no eval) even though
	// style-src doesn't -- script injection is exactly what a CSP on
	// this dashboard exists to block (see withSecurityHeaders' own doc
	// comment for why style-src is looser).
	scriptSrcDirective := csp[strings.Index(csp, "script-src"):]
	if end := strings.Index(scriptSrcDirective, ";"); end != -1 {
		scriptSrcDirective = scriptSrcDirective[:end]
	}
	if strings.Contains(scriptSrcDirective, "unsafe-inline") || strings.Contains(scriptSrcDirective, "unsafe-eval") {
		t.Errorf("script-src directive must stay strict, got %q", scriptSrcDirective)
	}

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if rec.Header().Get("Referrer-Policy") == "" {
		t.Error("expected a Referrer-Policy header, got none")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("wrapped handler's own response was not passed through: got status %d, want 200", rec.Code)
	}
}
