package config

import (
	"fmt"
	"os"
)

// Config is the process settings read once at startup.
type Config struct {
	Port        string
	DatabaseURL string
	LogLevel    string
}

// Load reads PORT, DATABASE_URL, and LOG_LEVEL from the environment.
func Load() (Config, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
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
		Port:        port,
		DatabaseURL: databaseURL,
		LogLevel:    logLevel,
	}, nil
}
