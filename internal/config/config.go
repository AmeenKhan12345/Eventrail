package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"time"
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

// WorkerSettings is the environment the delivery worker reads at startup.
// The operator API key is not required. An empty worker setting uses the default.
type WorkerSettings struct {
	DatabaseURL       string
	LogLevel          string
	SigningSecretKey  []byte
	AllowInsecureURLs bool
	Concurrency       int
	PollInterval      time.Duration
	Lease             time.Duration
	DeliveryTimeout   time.Duration
}

// LoadWorker reads the worker process settings.
// DATABASE_URL and SIGNING_SECRET_KEY are required.
// WORKER_CONCURRENCY defaults to 10, WORKER_POLL_INTERVAL to 1s,
// WORKER_LEASE to 60s, and DELIVERY_TIMEOUT to 10s.
func LoadWorker() (WorkerSettings, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return WorkerSettings{}, fmt.Errorf("DATABASE_URL is required")
	}
	signingSecretKey, err := decodeSigningSecretKey(os.Getenv("SIGNING_SECRET_KEY"))
	if err != nil {
		return WorkerSettings{}, err
	}
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}
	concurrency, err := envInt("WORKER_CONCURRENCY", 10)
	if err != nil {
		return WorkerSettings{}, err
	}
	pollInterval, err := envDuration("WORKER_POLL_INTERVAL", time.Second)
	if err != nil {
		return WorkerSettings{}, err
	}
	lease, err := envDuration("WORKER_LEASE", 60*time.Second)
	if err != nil {
		return WorkerSettings{}, err
	}
	deliveryTimeout, err := envDuration("DELIVERY_TIMEOUT", 10*time.Second)
	if err != nil {
		return WorkerSettings{}, err
	}
	return WorkerSettings{
		DatabaseURL:       databaseURL,
		LogLevel:          logLevel,
		SigningSecretKey:  signingSecretKey,
		AllowInsecureURLs: os.Getenv("ALLOW_INSECURE_URLS") == "true",
		Concurrency:       concurrency,
		PollInterval:      pollInterval,
		Lease:             lease,
		DeliveryTimeout:   deliveryTimeout,
	}, nil
}

func envInt(name string, fallback int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	if n < 1 {
		return 0, fmt.Errorf("%s must be at least 1", name)
	}
	return n, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return d, nil
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
