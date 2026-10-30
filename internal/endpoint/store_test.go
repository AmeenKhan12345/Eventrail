package endpoint

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmeenKhan12345/Eventrail/internal/platform/postgres"
)

var (
	testStore *Store
	pool      *pgxpool.Pool
)

func TestMain(m *testing.M) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("TEST_DATABASE_URL is required")
	}

	root, err := moduleRoot()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		log.Fatal(err)
	}
	if err := os.Setenv("RUN_MIGRATIONS", "true"); err != nil {
		log.Fatal(err)
	}
	if err := postgres.RunMigrations(databaseURL); err != nil {
		log.Fatal(err)
	}

	pool, err = postgres.NewPool(context.Background(), databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	testStore = NewStore(pool)

	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func TestSoftDeleteExcludedFromGetAndList(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	tenantID := insertTenant(t, "owner")
	created := createEndpoint(t, tenantID, "https://example.com/hook", "first")

	if err := testStore.SoftDelete(ctx, tenantID, created.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, err := testStore.Get(ctx, tenantID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get err = %v, want ErrNotFound", err)
	}
	got, err := testStore.List(ctx, tenantID, 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("list = %+v, want empty", got)
	}
}

func TestReregisterDeletedURL(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	tenantID := insertTenant(t, "owner")
	const rawURL = "https://example.com/hook"
	first := createEndpoint(t, tenantID, rawURL, "first")
	if err := testStore.SoftDelete(ctx, tenantID, first.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	second := createEndpoint(t, tenantID, rawURL, "second")
	if second.ID == first.ID {
		t.Fatal("re-register reused the deleted id")
	}
	got, err := testStore.List(ctx, tenantID, 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != second.ID {
		t.Fatalf("list = %+v, want the new endpoint", got)
	}
}

func TestDuplicateActiveURL(t *testing.T) {
	truncate(t)
	tenantID := insertTenant(t, "owner")
	createEndpoint(t, tenantID, "https://example.com/hook", "first")
	_, err := testStore.Create(context.Background(), tenantID, "https://example.com/hook", "second", []byte("cipher"), nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestTenantIsolation(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	owner := insertTenant(t, "owner")
	other := insertTenant(t, "other")
	created := createEndpoint(t, owner, "https://example.com/hook", "owned")

	tests := []struct {
		name string
		call func() error
	}{
		{name: "get", call: func() error {
			_, err := testStore.Get(ctx, other, created.ID)
			return err
		}},
		{name: "update", call: func() error {
			next := "https://example.com/other"
			_, err := testStore.Update(ctx, other, created.ID, &next, nil, nil, nil)
			return err
		}},
		{name: "delete", call: func() error {
			return testStore.SoftDelete(ctx, other, created.ID)
		}},
		{name: "rotate", call: func() error {
			_, err := testStore.RotateSecret(ctx, other, created.ID, []byte("new-cipher"))
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
		})
	}

	still, err := testStore.Get(ctx, owner, created.ID)
	if err != nil {
		t.Fatalf("owner Get: %v", err)
	}
	if still.URL != created.URL || string(mustCiphertext(t, created.ID)) == "new-cipher" {
		t.Fatalf("owner endpoint changed: %+v", still)
	}
}

func TestUpdateSetsOnlyProvidedFields(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	tenantID := insertTenant(t, "owner")
	created, err := testStore.Create(ctx, tenantID, "https://example.com/hook", "keep", []byte("cipher"), []string{"order.created"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `UPDATE endpoints SET updated_at = $2 WHERE id = $1`, created.ID, past); err != nil {
		t.Fatalf("set updated_at: %v", err)
	}
	created.UpdatedAt = past
	active := false
	updated, err := testStore.Update(ctx, tenantID, created.ID, nil, nil, &active, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.URL != created.URL || updated.Description != "keep" || updated.IsActive || len(updated.EventTypes) != 1 || updated.EventTypes[0] != "order.created" {
		t.Fatalf("updated = %+v", updated)
	}
	if !updated.UpdatedAt.After(past) {
		t.Fatalf("updated_at = %s, want after %s", updated.UpdatedAt, past)
	}
}

func TestUpdateReplacesEventTypes(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	tenantID := insertTenant(t, "owner")
	created := createEndpoint(t, tenantID, "https://example.com/hook", "")
	types := []string{"invoice.paid"}
	updated, err := testStore.Update(ctx, tenantID, created.ID, nil, nil, nil, &types)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.EventTypes) != 1 || updated.EventTypes[0] != "invoice.paid" {
		t.Fatalf("event types = %v", updated.EventTypes)
	}
	cleared := []string{}
	updated, err = testStore.Update(ctx, tenantID, created.ID, nil, nil, nil, &cleared)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if len(updated.EventTypes) != 0 {
		t.Fatalf("event types = %v, want empty", updated.EventTypes)
	}
	got, err := testStore.Get(ctx, tenantID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.EventTypes) != 0 {
		t.Fatalf("stored event types = %v", got.EventTypes)
	}
}

func TestListNewestFirst(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	tenantID := insertTenant(t, "owner")
	older := createEndpoint(t, tenantID, "https://example.com/older", "")
	newer := createEndpoint(t, tenantID, "https://example.com/newer", "")
	setCreatedAt(t, older.ID, time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC))
	setCreatedAt(t, newer.ID, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))

	got, err := testStore.List(ctx, tenantID, 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].ID != newer.ID || got[1].ID != older.ID {
		t.Fatalf("order = %v", ids(got))
	}
}

func createEndpoint(t *testing.T, tenantID uuid.UUID, rawURL, description string) Endpoint {
	t.Helper()
	created, err := testStore.Create(context.Background(), tenantID, rawURL, description, []byte("cipher-"+rawURL), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return created
}

func insertTenant(t *testing.T, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	return id
}

func setCreatedAt(t *testing.T, id uuid.UUID, at time.Time) {
	t.Helper()
	tag, err := pool.Exec(context.Background(), `UPDATE endpoints SET created_at = $2 WHERE id = $1`, id, at)
	if err != nil {
		t.Fatalf("set created_at: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("set created_at affected %d rows", tag.RowsAffected())
	}
}

func mustCiphertext(t *testing.T, id uuid.UUID) []byte {
	t.Helper()
	var ciphertext []byte
	err := pool.QueryRow(context.Background(), `SELECT secret_ciphertext FROM endpoints WHERE id = $1`, id).Scan(&ciphertext)
	if err != nil {
		t.Fatalf("ciphertext: %v", err)
	}
	return ciphertext
}

func ids(endpoints []Endpoint) []uuid.UUID {
	out := make([]uuid.UUID, len(endpoints))
	for i := range endpoints {
		out[i] = endpoints[i].ID
	}
	return out
}

func truncate(t *testing.T) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `TRUNCATE tenants CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}
