package httpx

import (
	"net/http"
	"strings"
)

// hopByHopHeaders lists standard HTTP/1.1 hop-by-hop headers that must not be
// blindly forwarded across a reverse proxy boundary (RFC 7230 Section 6.1 / RFC 2616 Section 13.5.1).
var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"trailers":            {},
	"transfer-encoding":   {},
	"upgrade":             {},
	"proxy-connection":    {},
}

// IsHopByHopHeader returns true if the header is defined as a hop-by-hop header.
func IsHopByHopHeader(headerName string) bool {
	_, ok := hopByHopHeaders[strings.ToLower(strings.TrimSpace(headerName))]
	return ok
}

// CopyEndToEndHeaders copies headers from src to dst while stripping hop-by-hop
// headers and any additional headers specified in the "Connection" header value.
func CopyEndToEndHeaders(dst, src http.Header) {
	if src == nil || dst == nil {
		return
	}

	// 1. Identify any dynamic hop-by-hop headers declared in the Connection header
	dynamicHops := make(map[string]struct{})
	if connVal := src.Get("Connection"); connVal != "" {
		for _, token := range strings.Split(connVal, ",") {
			token = strings.ToLower(strings.TrimSpace(token))
			if token != "" {
				dynamicHops[token] = struct{}{}
			}
		}
	}

	// 2. Copy only true end-to-end headers
	for key, values := range src {
		lowerKey := strings.ToLower(key)
		if IsHopByHopHeader(lowerKey) {
			continue
		}
		if _, ok := dynamicHops[lowerKey]; ok {
			continue
		}
		for _, val := range values {
			dst.Add(key, val)
		}
	}
}
