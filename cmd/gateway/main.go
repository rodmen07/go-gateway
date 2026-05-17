package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"net/http"
	"os"

	"github.com/rodmen07/go-gateway/internal/config"
	"github.com/rodmen07/go-gateway/internal/health"
	"github.com/rodmen07/go-gateway/internal/middleware"
	"github.com/rodmen07/go-gateway/internal/observer"
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

	rateLimiter := middleware.RateLimiter(cfg.RateLimitRPS, map[string]float64{
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
	})
	cors := middleware.CORS()

	mux := http.NewServeMux()

	// Gateway health — no rate limiting or logging needed
	mux.HandleFunc("/health", health.Handler())

	// Proxy routes — each wrapped with the full middleware chain
	for _, r := range routes {
		var routeObs *observer.Observer
		if r.observed {
			routeObs = obs
		}
		p := proxy.New(r.upstream, r.prefix, routeObs)
		handler := middleware.Chain(p, cors, middleware.Logger, middleware.Traceparent, middleware.RequestID, rateLimiter)
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
	)

	// JWTAuth wraps the entire mux. /health and /api/auth are exempted so
	// unauthenticated login and health-check requests still reach their handlers.
	jwtAuth := middleware.JWTAuth(cfg.JWTSecret, jwtPublicKey, cfg.JWTIssuer, []string{"/health", "/api/auth"})
	if err := http.ListenAndServe(addr, jwtAuth(mux)); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
