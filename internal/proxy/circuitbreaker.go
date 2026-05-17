package proxy

import (
	"net/http"
	"sync"
	"time"
)

// cbState is the circuit breaker state machine state.
type cbState int

const (
	cbClosed   cbState = iota // normal operation — requests pass through
	cbOpen                    // upstream is failing — requests are rejected immediately
	cbHalfOpen                // testing recovery — one probe request is allowed
)

// CircuitBreaker is a per-upstream state machine that opens after maxFailures
// consecutive failures and transitions to half-open after openTimeout elapses.
// Callers use Allow/RecordSuccess/RecordFailure to drive state transitions.
type CircuitBreaker struct {
	mu           sync.Mutex
	state        cbState
	failures     int
	lastOpened   time.Time
	maxFailures  int
	openTimeout  time.Duration
}

// NewCircuitBreaker returns a CircuitBreaker that opens after maxFailures
// consecutive upstream errors and allows a probe request after openTimeout.
func NewCircuitBreaker(maxFailures int, openTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures: maxFailures,
		openTimeout: openTimeout,
	}
}

// Allow returns true if the request should be forwarded to the upstream.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case cbClosed:
		return true
	case cbOpen:
		if time.Since(cb.lastOpened) >= cb.openTimeout {
			cb.state = cbHalfOpen
			return true
		}
		return false
	case cbHalfOpen:
		return true
	}
	return false
}

// RecordSuccess resets the failure counter and closes the circuit.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.state = cbClosed
}

// RecordFailure increments the failure counter and opens the circuit when
// the threshold is reached or a half-open probe fails.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	if cb.failures >= cb.maxFailures || cb.state == cbHalfOpen {
		cb.state = cbOpen
		cb.lastOpened = time.Now()
	}
}

// cbStatusRecorder captures the HTTP status written by the wrapped handler
// so the circuit breaker can record success or failure.
type cbStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *cbStatusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// WithCircuitBreaker wraps an upstream handler with the provided CircuitBreaker.
// When the circuit is open, it responds with 503 Service Unavailable and a
// Retry-After: 10 header instead of forwarding to the upstream. 5xx responses
// from the upstream are counted as failures; all other responses reset the
// failure counter.
func WithCircuitBreaker(cb *CircuitBreaker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cb.Allow() {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "10")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"code":"CIRCUIT_OPEN","message":"upstream temporarily unavailable"}`))
				return
			}
			rec := &cbStatusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			if rec.status >= 500 {
				cb.RecordFailure()
			} else {
				cb.RecordSuccess()
			}
		})
	}
}
