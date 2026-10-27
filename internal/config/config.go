package config

import (
	"encoding/base64"
	"fmt"
	"os"
)

const signingSecretKeyLen = 32

// Config is the process settings read once at startup.
type Config struct {
	Port              string
	DatabaseURL       string
	LogLevel          string
	OperatorAPIKey    string
	SigningSecretKey  []byte
	AllowInsecureURLs bool
}

// Load reads PORT, DATABASE_URL, LOG_LEVEL, OPERATOR_API_KEY,
// SIGNING_SECRET_KEY, and ALLOW_INSECURE_URLS from the environment.
// DATABASE_URL, OPERATOR_API_KEY, and SIGNING_SECRET_KEY have no default:
// a missing or invalid value is a startup error.
// ALLOW_INSECURE_URLS is true only when the value is the exact text "true".
func Load() (Config, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}

	operatorAPIKey := os.Getenv("OPERATOR_API_KEY")
	if operatorAPIKey == "" {
		return Config{}, fmt.Errorf("OPERATOR_API_KEY is required")
	}

	signingSecretKey, err := decodeSigningSecretKey(os.Getenv("SIGNING_SECRET_KEY"))
	if err != nil {
		return Config{}, err
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
		Port:              port,
		DatabaseURL:       databaseURL,
		LogLevel:          logLevel,
		OperatorAPIKey:    operatorAPIKey,
		SigningSecretKey:  signingSecretKey,
		AllowInsecureURLs: os.Getenv("ALLOW_INSECURE_URLS") == "true",
	}, nil
}

func decodeSigningSecretKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, fmt.Errorf("SIGNING_SECRET_KEY is required")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("SIGNING_SECRET_KEY must be valid base64")
	}
	if len(key) != signingSecretKeyLen {
		return nil, fmt.Errorf("SIGNING_SECRET_KEY must decode to 32 bytes")
	}
	return key, nil
}
