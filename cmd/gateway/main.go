package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rodmen07/go-gateway/internal/config"
	"github.com/rodmen07/go-gateway/internal/health"
	"github.com/rodmen07/go-gateway/internal/middleware"
	"github.com/rodmen07/go-gateway/internal/observer"
	"github.com/rodmen07/go-gateway/internal/openapi"
	"github.com/rodmen07/go-gateway/internal/proxy"
)

type route struct {
	prefix   string
	upstream string
	observed bool
}

func main() {
	// Use JSON structured logs so Cloud Logging can parse key/value fields.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg := config.Load()

	// Parse RSA public key when AUTH_JWT_PUBLIC_KEY is set (RS256 mode).
	var jwtPublicKey *rsa.PublicKey
	if cfg.JWTPublicKey != "" {
		block, _ := pem.Decode([]byte(cfg.JWTPublicKey))
		if block == nil {
			slog.Error("AUTH_JWT_PUBLIC_KEY contains invalid PEM data")
			os.Exit(1)
		}
		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			slog.Error("failed to parse AUTH_JWT_PUBLIC_KEY", "error", err)
			os.Exit(1)
		}
		var ok bool
		jwtPublicKey, ok = pub.(*rsa.PublicKey)
		if !ok {
			slog.Error("AUTH_JWT_PUBLIC_KEY is not an RSA public key")
			os.Exit(1)
		}
	}

	obs := observer.New(cfg.ObservaboardURL, cfg.ObservaboardAPIKey, cfg.PubSubProject, cfg.PubSubTopic)

	routes := []route{
		{"/api/auth", cfg.AuthURL, false},
		{"/api/v1/projects", cfg.ProjectsURL, false},
		{"/api/tasks", cfg.TasksURL, false},
		{"/api/accounts", cfg.AccountsURL, true},
		{"/api/contacts", cfg.ContactsURL, true},
		{"/api/opportunities", cfg.OpportunitiesURL, true},
		{"/api/activities", cfg.ActivitiesURL, true},
		{"/api/automation", cfg.AutomationURL, true},
		{"/api/integrations", cfg.IntegrationsURL, true},
		{"/api/reporting", cfg.ReportingURL, false},
		{"/api/search", cfg.SearchURL, false},
		{"/api/events", cfg.EventsURL, false},
	}

	routeLimits := map[string]float64{
		// Auth routes: tightest limit to slow credential-stuffing attempts.
		"/api/auth": cfg.AuthRateLimitRPS,
		// CRM write-capable routes: moderate limit.
		"/api/accounts":      cfg.WriteRateLimitRPS,
		"/api/contacts":      cfg.WriteRateLimitRPS,
		"/api/opportunities": cfg.WriteRateLimitRPS,
		"/api/activities":    cfg.WriteRateLimitRPS,
		"/api/automation":    cfg.WriteRateLimitRPS,
		"/api/integrations":  cfg.WriteRateLimitRPS,
		"/api/tasks":         cfg.WriteRateLimitRPS,
		"/api/v1/projects":   cfg.WriteRateLimitRPS,
		// Read-heavy routes: most generous limit.
		"/api/reporting": cfg.ReadRateLimitRPS,
		"/api/search":    cfg.ReadRateLimitRPS,
		"/api/events":    cfg.ReadRateLimitRPS,
	}

	rateLimiter := middleware.RateLimiter(cfg.RateLimitRPS, routeLimits)
	if cfg.EnableRedisRateLimiter {
		if cfg.RedisURL == "" {
			slog.Warn("ENABLE_REDIS_RATE_LIMITER=true but REDIS_URL is empty; using in-memory limiter")
		} else {
			redisOpts, err := redis.ParseURL(cfg.RedisURL)
			if err != nil {
				// Backward-compatible fallback for plain host:port values.
				redisOpts = &redis.Options{Addr: cfg.RedisURL}
			}
			redisClient := redis.NewClient(redisOpts)
			rateLimiter = middleware.RedisRateLimiter(redisClient, cfg.RateLimitRPS, routeLimits)
			slog.Info("redis rate limiter enabled", "redis_addr", cfg.RedisURL)
		}
	}

	responseCache := func(next http.Handler) http.Handler { return next }
	if cfg.EnableResponseCache {
		responseCache = middleware.ResponseCache(time.Duration(cfg.CacheTTLSeconds)*time.Second, cfg.CacheMaxEntries)
		slog.Info("response cache enabled", "cache_ttl_seconds", cfg.CacheTTLSeconds, "cache_max_entries", cfg.CacheMaxEntries)
	}
	cors := middleware.CORS()

	mux := http.NewServeMux()

	// Gateway health — no rate limiting or logging needed
	mux.HandleFunc("/health", health.Handler())

	// OpenAPI spec and Swagger UI — public documentation, no auth required
	mux.HandleFunc("/api/openapi.json", openapi.SpecHandler)
	mux.HandleFunc("/api/docs", openapi.UIHandler)

	// Upstream health fan-out — probes each service's /health and aggregates.
	// Also exempt from JWT so monitoring systems can call it unauthenticated.
	upstreamURLs := map[string]string{
		"auth":          cfg.AuthURL,
		"projects":      cfg.ProjectsURL,
		"tasks":         cfg.TasksURL,
		"accounts":      cfg.AccountsURL,
		"contacts":      cfg.ContactsURL,
		"opportunities": cfg.OpportunitiesURL,
		"activities":    cfg.ActivitiesURL,
		"automation":    cfg.AutomationURL,
		"integrations":  cfg.IntegrationsURL,
		"reporting":     cfg.ReportingURL,
		"search":        cfg.SearchURL,
		"events":        cfg.EventsURL,
	}
	mux.HandleFunc("/health/upstreams", health.UpstreamsHandler(upstreamURLs))

	// Proxy routes — each wrapped with the full middleware chain
	for _, r := range routes {
		var routeObs *observer.Observer
		if r.observed {
			routeObs = obs
		}
		p := proxy.New(r.upstream, r.prefix, routeObs)
		// Each route gets its own circuit breaker: opens after 5 consecutive 5xx
		// responses and allows a probe after 30 s.
		cb := proxy.NewCircuitBreaker(5, 30*time.Second)
		handler := middleware.Chain(proxy.WithCircuitBreaker(cb)(p), cors, middleware.Logger, middleware.Traceparent, middleware.RequestID, responseCache, rateLimiter)
		mux.Handle(r.prefix+"/", handler)
		// Also match the prefix exactly (no trailing slash)
		mux.Handle(r.prefix, handler)
		slog.Info("route registered", "prefix", r.prefix, "upstream", r.upstream, "observed", r.observed)
	}

	addr := ":" + cfg.Port
	slog.Info("go-gateway listening",
		"addr", addr,
		"auth_rps", cfg.AuthRateLimitRPS,
		"write_rps", cfg.WriteRateLimitRPS,
		"read_rps", cfg.ReadRateLimitRPS,
		"default_rps", cfg.RateLimitRPS,
		"enable_redis_rate_limiter", cfg.EnableRedisRateLimiter,
		"enable_response_cache", cfg.EnableResponseCache,
		"cache_ttl_seconds", cfg.CacheTTLSeconds,
		"cache_max_entries", cfg.CacheMaxEntries,
	)

	// JWTAuth wraps the entire mux. /health, /api/auth, /api/openapi.json, and /api/docs are exempted so
	// unauthenticated login, health-check, and documentation requests still reach their handlers.
	jwtAuth := middleware.JWTAuth(cfg.JWTSecret, jwtPublicKey, cfg.JWTIssuer, []string{"/health", "/api/auth", "/api/openapi.json", "/api/docs"})
	// SecurityHeaders and BlockScannerPaths are outermost so every response
	// carries hardening headers and scanner probes are rejected before JWT
	// validation or rate-limiting runs.
	handler := middleware.SecurityHeaders(middleware.BlockScannerPaths(jwtAuth(mux)))
	if err := http.ListenAndServe(addr, handler); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
