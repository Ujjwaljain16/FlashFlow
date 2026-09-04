package httpx

import (
	"encoding/json"
	"net/http"
)

// OriginResponse represents the standard JSON payload returned by Origin nodes.
type OriginResponse struct {
	Service     string `json:"service"`
	Instance    string `json:"instance"`
	RequestID   string `json:"request_id"`
	Timestamp   string `json:"timestamp"`
	PayloadSize int    `json:"payload_size"`
}

// EdgeResponse represents the standard JSON payload forwarded through Edge nodes.
type EdgeResponse struct {
	Edge      string          `json:"edge"`
	Origin    *OriginResponse `json:"origin,omitempty"`
	RawOrigin json.RawMessage `json:"raw_origin,omitempty"`
	RequestID string          `json:"request_id"`
	Timestamp string          `json:"timestamp"`
}

// ErrorResponse represents a structured error returned by proxy or edge.
type ErrorResponse struct {
	Error     string `json:"error"`
	Code      int    `json:"code"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteJSON writes a JSON response with status code and appropriate Content-Type header.
func WriteJSON(w http.ResponseWriter, status int, data any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(data)
}
