package postgres

import "testing"

func TestRunMigrationsSkipsUnlessEnabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "unset", value: ""},
		{name: "false", value: "false"},
		{name: "one", value: "1"},
		{name: "uppercase", value: "TRUE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("RUN_MIGRATIONS", tt.value)

			if err := RunMigrations("postgres://localhost/does-not-exist"); err != nil {
				t.Fatalf("RunMigrations: %v", err)
			}
		})
	}
}
