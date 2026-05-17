package middleware

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter returns a per-client-IP, per-route token-bucket middleware.
//
// routeLimits maps a route prefix (e.g. "/api/auth") to its allowed requests
// per second. Routes not matched by any prefix use defaultRPS.
//
// Standard X-RateLimit-Limit / X-RateLimit-Remaining / X-RateLimit-Reset
// headers are set on every response. A Retry-After header is added on 429s.
//
// A background goroutine evicts entries idle for more than 5 minutes to
// prevent unbounded memory growth in long-running deployments.
func RateLimiter(defaultRPS float64, routeLimits map[string]float64) func(http.Handler) http.Handler {
	var mu sync.Mutex
	limiters := make(map[string]*limiterEntry)

	getLimiter := func(key string, rps float64) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()
		if e, ok := limiters[key]; ok {
			e.lastSeen = time.Now()
			return e.limiter
		}
		burst := int(rps) * 2
		if burst < 1 {
			burst = 1
		}
		l := rate.NewLimiter(rate.Limit(rps), burst)
		limiters[key] = &limiterEntry{limiter: l, lastSeen: time.Now()}
		return l
	}

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cutoff := time.Now().Add(-5 * time.Minute)
			mu.Lock()
			for k, e := range limiters {
				if e.lastSeen.Before(cutoff) {
					delete(limiters, k)
				}
			}
			mu.Unlock()
		}
	}()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rps := routeRPS(r.URL.Path, defaultRPS, routeLimits)
			key := extractIP(r) + "|" + routeKey(r.URL.Path)
			l := getLimiter(key, rps)

			limitVal := int(rps)
			remaining := int(l.Tokens())
			if remaining < 0 {
				remaining = 0
			}
			reset := time.Now().Unix() + 1

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limitVal))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))

			if !l.Allow() {
				retryAfter := 1
				if rps > 0 {
					retryAfter = int(1.0/rps) + 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"code":    "RATE_LIMITED",
					"message": fmt.Sprintf("rate limit exceeded (%d req/s) — retry after %ds", limitVal, retryAfter),
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// routeRPS returns the configured RPS for the given path, falling back to defaultRPS.
func routeRPS(path string, defaultRPS float64, routeLimits map[string]float64) float64 {
	for prefix, rps := range routeLimits {
		if strings.HasPrefix(path, prefix) {
			return rps
		}
	}
	return defaultRPS
}

// routeKey extracts the first two path segments, e.g. "/api/accounts/foo" → "/api/accounts".
func routeKey(path string) string {
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 3)
	if len(parts) >= 2 {
		return "/" + parts[0] + "/" + parts[1]
	}
	return path
}

// extractIP returns the client IP from X-Forwarded-For (set by Cloud Run's
// load balancer) or falls back to the TCP remote address.
func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// The leftmost IP is the original client; subsequent ones are proxies.
		if ip := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
