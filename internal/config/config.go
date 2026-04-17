package config

import (
	"os"
	"strconv"
)

// Config holds all runtime configuration for the gateway.
type Config struct {
	Port         string
	RateLimitRPS float64

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
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Load reads configuration from environment variables with production defaults.
func Load() Config {
	rps, err := strconv.ParseFloat(getenv("RATE_LIMIT_RPS", "10"), 64)
	if err != nil || rps <= 0 {
		rps = 10
	}

	return Config{
		Port:         getenv("PORT", "8080"),
		RateLimitRPS: rps,

		AuthURL:          getenv("AUTH_URL", "http://127.0.0.1:8082"),
		ProjectsURL:      getenv("PROJECTS_URL", "http://127.0.0.1:8083"),
		TasksURL:         getenv("TASKS_URL", "https://backend-service-rodmen07-v2.fly.dev"),
		AccountsURL:      getenv("ACCOUNTS_URL", "https://accounts-service-5gcrg4oiza-uc.a.run.app"),
		ContactsURL:      getenv("CONTACTS_URL", "https://contacts-service-5gcrg4oiza-uc.a.run.app"),
		OpportunitiesURL: getenv("OPPORTUNITIES_URL", "https://opportunities-service-5gcrg4oiza-uc.a.run.app"),
		ActivitiesURL:    getenv("ACTIVITIES_URL", "https://activities-service-5gcrg4oiza-uc.a.run.app"),
		AutomationURL:    getenv("AUTOMATION_URL", "https://automation-service-5gcrg4oiza-uc.a.run.app"),
		IntegrationsURL:  getenv("INTEGRATIONS_URL", "https://integrations-service-5gcrg4oiza-uc.a.run.app"),
		ReportingURL:     getenv("REPORTING_URL", "https://reporting-service-5gcrg4oiza-uc.a.run.app"),
		SearchURL:        getenv("SEARCH_URL", "https://search-service-5gcrg4oiza-uc.a.run.app"),
		EventsURL:        getenv("EVENTS_URL", "https://observaboard-rodmen07.fly.dev"),
	}
}
