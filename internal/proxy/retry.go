package proxy

import (
	"net/http"
	"time"
)

// RetryTransport is an http.RoundTripper that retries GET and HEAD requests
// on transport errors using exponential backoff. It does NOT retry on HTTP
// 5xx responses (those are valid upstream responses, handled by the circuit
// breaker). Non-idempotent methods (POST, PUT, PATCH, DELETE) are never
// retried to avoid double-writes.
type RetryTransport struct {
	Base       http.RoundTripper // defaults to http.DefaultTransport when nil
	MaxRetries int               // additional attempts beyond the first
}

func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	// Only retry idempotent methods on transport errors.
	idempotent := req.Method == http.MethodGet || req.Method == http.MethodHead

	var (
		resp *http.Response
		err  error
	)
	for attempt := 0; attempt <= t.MaxRetries; attempt++ {
		if attempt > 0 {
			if !idempotent {
				break
			}
			// Exponential backoff: 50 ms, 100 ms, 200 ms, ...
			time.Sleep(time.Duration(1<<uint(attempt-1)) * 50 * time.Millisecond)
		}
		resp, err = base.RoundTrip(req)
		if err == nil {
			return resp, nil
		}
	}
	return resp, err
}
