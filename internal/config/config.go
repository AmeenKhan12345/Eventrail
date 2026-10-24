package config

import (
	"fmt"
	"os"
)

// Config is the process settings read once at startup.
type Config struct {
	Port           string
	DatabaseURL    string
	LogLevel       string
	OperatorAPIKey string
}

// Load reads PORT, DATABASE_URL, LOG_LEVEL, and OPERATOR_API_KEY from the environment.
// DATABASE_URL and OPERATOR_API_KEY have no default: a missing value is a startup error.
func Load() (Config, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}

	operatorAPIKey := os.Getenv("OPERATOR_API_KEY")
	if operatorAPIKey == "" {
		return Config{}, fmt.Errorf("OPERATOR_API_KEY is required")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	return Config{
		Port:           port,
		DatabaseURL:    databaseURL,
		LogLevel:       logLevel,
		OperatorAPIKey: operatorAPIKey,
	}, nil
}
