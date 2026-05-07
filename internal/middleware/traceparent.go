package middleware

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
)

const traceparentHeader = "traceparent"

// newTraceID generates a W3C trace ID (32 hex chars).
func newTraceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%032x", b)
}

// newSpanID generates a W3C span ID (16 hex chars).
func newSpanID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%016x", b)
}

// Traceparent reads the W3C traceparent header from the incoming request
// (format: traceparent: 00-<trace-id>-<span-id>-<trace-flags>).
// If not present, generates a new trace ID and span ID.
// Passes the header unchanged to downstream services for end-to-end tracing.
func Traceparent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tp := r.Header.Get(traceparentHeader)
		if tp == "" || !isValidTraceparent(tp) {
			// Generate new trace if not provided or invalid
			tp = fmt.Sprintf("00-%s-%s-01", newTraceID(), newSpanID())
		}
		// Ensure it's propagated to downstream requests (via proxy.go)
		r.Header.Set(traceparentHeader, tp)
		w.Header().Set(traceparentHeader, tp)
		next.ServeHTTP(w, r)
	})
}

func isValidTraceparent(tp string) bool {
	// Basic validation: should have 4 parts separated by hyphens
	parts := strings.Split(tp, "-")
	return len(parts) == 4 && parts[0] == "00"
}
