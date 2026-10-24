package config

import "testing"

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name:    "missing database url",
			env:     map[string]string{"DATABASE_URL": "", "OPERATOR_API_KEY": "operator-key", "PORT": "", "LOG_LEVEL": ""},
			wantErr: true,
		},
		{
			name:    "missing operator api key",
			env:     map[string]string{"DATABASE_URL": "postgres://localhost/webhooks", "OPERATOR_API_KEY": "", "PORT": "", "LOG_LEVEL": ""},
			wantErr: true,
		},
		{
			name: "defaults",
			env:  map[string]string{"DATABASE_URL": "postgres://localhost/webhooks", "OPERATOR_API_KEY": "operator-key", "PORT": "", "LOG_LEVEL": ""},
			want: Config{Port: "8080", DatabaseURL: "postgres://localhost/webhooks", LogLevel: "info", OperatorAPIKey: "operator-key"},
		},
		{
			name: "overrides",
			env:  map[string]string{"DATABASE_URL": "postgres://db/app", "OPERATOR_API_KEY": "other-key", "PORT": "9090", "LOG_LEVEL": "debug"},
			want: Config{Port: "9090", DatabaseURL: "postgres://db/app", LogLevel: "debug", OperatorAPIKey: "other-key"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
