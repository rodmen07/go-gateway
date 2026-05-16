package config

import (
	"os"
	"strconv"
)

// Config holds all runtime configuration for the gateway.
type Config struct {
	Port string

	// Rate limiting — defaults applied when env vars are absent.
	// RateLimitRPS is the fallback for any route not matched by a tier.
	RateLimitRPS      float64 // default 15  (unclassified routes)
	AuthRateLimitRPS  float64 // default 5   (/api/auth/*)
	WriteRateLimitRPS float64 // default 30  (CRM mutation routes)
	ReadRateLimitRPS  float64 // default 60  (reporting, search, events)

	// Upstream service URLs
	AuthURL          string
	ProjectsURL      string
	TasksURL         string
	AccountsURL      string
	ContactsURL      string
	OpportunitiesURL string
	ActivitiesURL    string
	AutomationURL    string
	IntegrationsURL  string
	ReportingURL     string
	SearchURL        string
	EventsURL        string

	// Observaboard mutation observer
	ObservaboardURL    string
	ObservaboardAPIKey string
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseRPS(key string, fallback float64) float64 {
	v, err := strconv.ParseFloat(getenv(key, ""), 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

// Load reads configuration from environment variables with production defaults.
func Load() Config {
	return Config{
		Port: getenv("PORT", "8080"),

		RateLimitRPS:      parseRPS("RATE_LIMIT_RPS", 15),
		AuthRateLimitRPS:  parseRPS("RATE_LIMIT_AUTH_RPS", 5),
		WriteRateLimitRPS: parseRPS("RATE_LIMIT_WRITE_RPS", 30),
		ReadRateLimitRPS:  parseRPS("RATE_LIMIT_READ_RPS", 60),

		AuthURL:          getenv("AUTH_URL", "http://127.0.0.1:8082"),
		ProjectsURL:      getenv("PROJECTS_URL", "http://127.0.0.1:8083"),
		TasksURL:         getenv("TASKS_URL", "https://backend-service-5gcrg4oiza-uc.a.run.app"),
		AccountsURL:      getenv("ACCOUNTS_URL", "https://accounts-service-5gcrg4oiza-uc.a.run.app"),
		ContactsURL:      getenv("CONTACTS_URL", "https://contacts-service-5gcrg4oiza-uc.a.run.app"),
		OpportunitiesURL: getenv("OPPORTUNITIES_URL", "https://opportunities-service-5gcrg4oiza-uc.a.run.app"),
		ActivitiesURL:    getenv("ACTIVITIES_URL", "https://activities-service-5gcrg4oiza-uc.a.run.app"),
		AutomationURL:    getenv("AUTOMATION_URL", "https://automation-service-5gcrg4oiza-uc.a.run.app"),
		IntegrationsURL:  getenv("INTEGRATIONS_URL", "https://integrations-service-5gcrg4oiza-uc.a.run.app"),
		ReportingURL:     getenv("REPORTING_URL", "https://reporting-service-5gcrg4oiza-uc.a.run.app"),
		SearchURL:        getenv("SEARCH_URL", "https://search-service-5gcrg4oiza-uc.a.run.app"),
		EventsURL:        getenv("EVENTS_URL", "https://observaboard-5gcrg4oiza-uc.a.run.app"),

		ObservaboardURL:    getenv("OBSERVABOARD_URL", "https://observaboard-5gcrg4oiza-uc.a.run.app"),
		ObservaboardAPIKey: getenv("OBSERVABOARD_API_KEY", ""),
	}
}
