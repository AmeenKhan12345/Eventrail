package postgres

import (
	"errors"
	"fmt"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// RunMigrations applies the SQL files in the migrations folder.
// It does nothing unless RUN_MIGRATIONS=true.
// The process must be started from the module root so file://migrations resolves.
func RunMigrations(databaseURL string) error {
	if os.Getenv("RUN_MIGRATIONS") != "true" {
		return nil
	}

	migrator, err := migrate.New("file://migrations", databaseURL)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer migrator.Close()

	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
