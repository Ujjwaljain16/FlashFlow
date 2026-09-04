package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

const (
	// HeaderRequestID is the standard FlashFlow header for request correlation.
	HeaderRequestID = "X-Request-ID"

	// HeaderSelectedEdge is an optional debug header indicating the chosen upstream edge.
	HeaderSelectedEdge = "X-Selected-Edge"

	// HeaderEdgeID is the header injected by edge nodes to identify themselves.
	HeaderEdgeID = "X-Edge-ID"

	// HeaderProxyLatency is an optional debug header showing proxy processing time.
	HeaderProxyLatency = "X-Proxy-Latency-Ms"

	// HeaderUpstreamLatency is an optional debug header showing upstream response time.
	HeaderUpstreamLatency = "X-Upstream-Latency-Ms"
)

// GenerateRequestID generates an opaque 16-byte cryptographically secure random ID
// formatted as a 32-character lowercase hexadecimal string.
func GenerateRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback should rand.Read fail unexpectedly
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// ExtractOrGenerateRequestID inspects incoming HTTP headers for X-Request-ID.
// If present and non-empty, it returns the existing ID; otherwise, it generates a fresh opaque ID.
func ExtractOrGenerateRequestID(r *http.Request) string {
	if r == nil {
		return GenerateRequestID()
	}
	reqID := r.Header.Get(HeaderRequestID)
	if reqID != "" {
		return reqID
	}
	return GenerateRequestID()
}
