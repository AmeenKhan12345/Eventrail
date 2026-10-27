package config

import (
	"bytes"
	"encoding/base64"
	"testing"
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

func equalConfig(got, want Config) bool {
	return got.Port == want.Port &&
		got.DatabaseURL == want.DatabaseURL &&
		got.LogLevel == want.LogLevel &&
		got.OperatorAPIKey == want.OperatorAPIKey &&
		got.AllowInsecureURLs == want.AllowInsecureURLs &&
		bytes.Equal(got.SigningSecretKey, want.SigningSecretKey)
}
