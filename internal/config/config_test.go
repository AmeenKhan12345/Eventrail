package config

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	signingKey := bytes.Repeat([]byte{7}, 32)
	signingKeyB64 := base64.StdEncoding.EncodeToString(signingKey)
	shortKeyB64 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 16))

	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name:    "missing database url",
			env:     map[string]string{"DATABASE_URL": "", "OPERATOR_API_KEY": "operator-key", "SIGNING_SECRET_KEY": signingKeyB64, "PORT": "", "LOG_LEVEL": ""},
			wantErr: true,
		},
		{
			name:    "missing operator api key",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/webhooks", "OPERATOR_API_KEY": "", "SIGNING_SECRET_KEY": signingKeyB64, "PORT": "", "LOG_LEVEL": ""},
			wantErr: true,
		},
		{
			name:    "missing signing secret key",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/webhooks", "OPERATOR_API_KEY": "operator-key", "SIGNING_SECRET_KEY": "", "PORT": "", "LOG_LEVEL": ""},
			wantErr: true,
		},
		{
			name:    "invalid signing secret key",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/webhooks", "OPERATOR_API_KEY": "operator-key", "SIGNING_SECRET_KEY": "not base64!!!", "PORT": "", "LOG_LEVEL": ""},
			wantErr: true,
		},
		{
			name:    "short signing secret key",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/webhooks", "OPERATOR_API_KEY": "operator-key", "SIGNING_SECRET_KEY": shortKeyB64, "PORT": "", "LOG_LEVEL": ""},
			wantErr: true,
		},
		{
			name: "defaults",
			env:  map[string]string{"DATABASE_URL": "postgres://localhost/webhooks", "OPERATOR_API_KEY": "operator-key", "SIGNING_SECRET_KEY": signingKeyB64, "PORT": "", "LOG_LEVEL": ""},
			want: Config{Port: "8080", DatabaseURL: "postgres://localhost/webhooks", LogLevel: "info", OperatorAPIKey: "operator-key", SigningSecretKey: signingKey},
		},
		{
			name: "overrides",
			env:  map[string]string{"DATABASE_URL": "postgres://db/app", "OPERATOR_API_KEY": "other-key", "SIGNING_SECRET_KEY": signingKeyB64, "PORT": "9090", "LOG_LEVEL": "debug", "ALLOW_INSECURE_URLS": "true"},
			want: Config{Port: "9090", DatabaseURL: "postgres://db/app", LogLevel: "debug", OperatorAPIKey: "other-key", SigningSecretKey: signingKey, AllowInsecureURLs: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := tt.env["ALLOW_INSECURE_URLS"]; !ok {
				tt.env["ALLOW_INSECURE_URLS"] = ""
			}
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			got, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !equalConfig(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadWorker(t *testing.T) {
	signingKey := bytes.Repeat([]byte{7}, 32)
	signingKeyB64 := base64.StdEncoding.EncodeToString(signingKey)
	base := map[string]string{
		"DATABASE_URL":         "postgres://localhost/webhooks",
		"SIGNING_SECRET_KEY":   signingKeyB64,
		"ALLOW_INSECURE_URLS":  "",
		"LOG_LEVEL":            "",
		"WORKER_CONCURRENCY":   "",
		"WORKER_POLL_INTERVAL": "",
		"WORKER_LEASE":         "",
		"DELIVERY_TIMEOUT":     "",
	}
	tests := []struct {
		name    string
		env     map[string]string
		want    WorkerSettings
		wantErr bool
	}{
		{
			name:    "missing database url",
			env:     map[string]string{"DATABASE_URL": ""},
			wantErr: true,
		},
		{
			name:    "bad concurrency",
			env:     map[string]string{"WORKER_CONCURRENCY": "many"},
			wantErr: true,
		},
		{
			name:    "concurrency below 1",
			env:     map[string]string{"WORKER_CONCURRENCY": "0"},
			wantErr: true,
		},
		{
			name:    "bad lease",
			env:     map[string]string{"WORKER_LEASE": "soon"},
			wantErr: true,
		},
		{
			name: "defaults",
			want: WorkerSettings{
				DatabaseURL:      "postgres://localhost/webhooks",
				LogLevel:         "info",
				SigningSecretKey: signingKey,
				Concurrency:      10,
				PollInterval:     time.Second,
				Lease:            60 * time.Second,
				DeliveryTimeout:  10 * time.Second,
			},
		},
		{
			name: "overrides",
			env: map[string]string{
				"DATABASE_URL":         "postgres://db/app",
				"LOG_LEVEL":            "debug",
				"ALLOW_INSECURE_URLS":  "true",
				"WORKER_CONCURRENCY":   "4",
				"WORKER_POLL_INTERVAL": "250ms",
				"WORKER_LEASE":         "30s",
				"DELIVERY_TIMEOUT":     "2s",
			},
			want: WorkerSettings{
				DatabaseURL:       "postgres://db/app",
				LogLevel:          "debug",
				SigningSecretKey:  signingKey,
				AllowInsecureURLs: true,
				Concurrency:       4,
				PollInterval:      250 * time.Millisecond,
				Lease:             30 * time.Second,
				DeliveryTimeout:   2 * time.Second,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := make(map[string]string, len(base)+len(tt.env))
			for key, value := range base {
				env[key] = value
			}
			for key, value := range tt.env {
				env[key] = value
			}
			for key, value := range env {
				t.Setenv(key, value)
			}
			got, err := LoadWorker()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadWorker: %v", err)
			}
			if got.DatabaseURL != tt.want.DatabaseURL || got.LogLevel != tt.want.LogLevel || got.AllowInsecureURLs != tt.want.AllowInsecureURLs || got.Concurrency != tt.want.Concurrency || got.PollInterval != tt.want.PollInterval || got.Lease != tt.want.Lease || got.DeliveryTimeout != tt.want.DeliveryTimeout || !bytes.Equal(got.SigningSecretKey, tt.want.SigningSecretKey) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func equalConfig(got, want Config) bool {
	return got.Port == want.Port &&
		got.DatabaseURL == want.DatabaseURL &&
		got.LogLevel == want.LogLevel &&
		got.OperatorAPIKey == want.OperatorAPIKey &&
		got.AllowInsecureURLs == want.AllowInsecureURLs &&
		bytes.Equal(got.SigningSecretKey, want.SigningSecretKey)
}
