package main

import (
	"fmt"
	"log"
	"net/http"

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
	cfg := config.Load()

	obs := observer.New(cfg.ObservaboardURL, cfg.ObservaboardAPIKey)

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

	rateLimiter := middleware.RateLimiter(cfg.RateLimitRPS)
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
		handler := middleware.Chain(p, cors, middleware.Logger, middleware.RequestID, rateLimiter)
		mux.Handle(r.prefix+"/", handler)
		// Also match the prefix exactly (no trailing slash)
		mux.Handle(r.prefix, handler)
		fmt.Printf("  %-22s → %s\n", r.prefix+"/*", r.upstream)
	}

	addr := ":" + cfg.Port
	log.Printf("go-gateway listening on %s (rate limit: %.0f rps per route)\n", addr, cfg.RateLimitRPS)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
