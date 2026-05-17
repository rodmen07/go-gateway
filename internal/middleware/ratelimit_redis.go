package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisRateLimiter enforces a distributed fixed-window rate limit backed by Redis.
//
// The key shape is: rl:<ip>:<routeKey>:<epochSecond>
// Each request does INCR on the current-second key and sets EXPIRE 2s.
//
// This keeps limiter state shared across all gateway instances and survives
// individual instance restarts.
func RedisRateLimiter(client *redis.Client, defaultRPS float64, routeLimits map[string]float64) func(http.Handler) http.Handler {
	if client == nil {
		return RateLimiter(defaultRPS, routeLimits)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rps := routeRPS(r.URL.Path, defaultRPS, routeLimits)
			limitVal := int(rps)
			if limitVal < 1 {
				limitVal = 1
			}

			ip := extractIP(r)
			route := routeKey(r.URL.Path)
			now := time.Now().Unix()
			key := fmt.Sprintf("rl:%s:%s:%d", ip, route, now)

			ctx, cancel := context.WithTimeout(r.Context(), 300*time.Millisecond)
			defer cancel()

			count, err := client.Incr(ctx, key).Result()
			if err != nil {
				// Fail-open so a transient Redis issue does not take down the API.
				next.ServeHTTP(w, r)
				return
			}

			if count == 1 {
				_ = client.Expire(ctx, key, 2*time.Second).Err()
			}

			remaining := limitVal - int(count)
			if remaining < 0 {
				remaining = 0
			}
			reset := now + 1

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limitVal))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))

			if count > int64(limitVal) {
				retryAfter := 1
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"code":    "RATE_LIMITED",
					"message": fmt.Sprintf("rate limit exceeded (%d req/s) - retry after %ds", limitVal, retryAfter),
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
