package tenant

import (
	"bytes"
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

	"github.com/AmeenKhan12345/webhooks/internal/platform/postgres"
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

func TestCreateThenGet(t *testing.T) {
	truncate(t)

	tests := []struct {
		name string
	}{
		{name: "Acme"},
		{name: "Northwind"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created, err := testStore.Create(context.Background(), tt.name)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if created.ID == uuid.Nil {
				t.Fatal("Create returned a nil id")
			}
			if created.Name != tt.name {
				t.Fatalf("name = %q, want %q", created.Name, tt.name)
			}

			got, err := testStore.GetByID(context.Background(), created.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if !equalTenant(got, created) {
				t.Fatalf("got %+v, want %+v", got, created)
			}
		})
	}
}

func TestGetByIDNotFound(t *testing.T) {
	truncate(t)

	tests := []struct {
		name string
		id   uuid.UUID
	}{
		{name: "nil id", id: uuid.Nil},
		{name: "random id", id: uuid.MustParse("11111111-1111-1111-1111-111111111111")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testStore.GetByID(context.Background(), tt.id)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
			if got != (Tenant{}) {
				t.Fatalf("got %+v, want zero Tenant", got)
			}
		})
	}
}

func TestGetByIDCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := testStore.GetByID(ctx, uuid.New())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestList(t *testing.T) {
	truncate(t)
	ordered := seedOrdered(t)

	tests := []struct {
		name   string
		limit  int
		offset int
		want   []Tenant
	}{
		{name: "created_at desc then id", limit: 10, offset: 0, want: ordered},
		{name: "limit", limit: 2, offset: 0, want: ordered[:2]},
		{name: "offset", limit: 2, offset: 1, want: ordered[1:]},
		{name: "offset past end", limit: 5, offset: len(ordered), want: []Tenant{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := testStore.List(context.Background(), tt.limit, tt.offset)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d (%+v)", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if !equalTenant(got[i], tt.want[i]) {
					t.Fatalf("row %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func seedOrdered(t *testing.T) []Tenant {
	t.Helper()
	ctx := context.Background()

	olderAt := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	newerAt := time.Date(2024, 6, 7, 8, 9, 10, 0, time.UTC)

	left, err := testStore.Create(ctx, "older-a")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	right, err := testStore.Create(ctx, "older-b")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	newest, err := testStore.Create(ctx, "newer")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	setCreatedAt(t, left.ID, olderAt)
	setCreatedAt(t, right.ID, olderAt)
	setCreatedAt(t, newest.ID, newerAt)
	left.CreatedAt = olderAt
	right.CreatedAt = olderAt
	newest.CreatedAt = newerAt

	if bytes.Compare(left.ID[:], right.ID[:]) > 0 {
		left, right = right, left
	}
	return []Tenant{newest, left, right}
}

func setCreatedAt(t *testing.T, id uuid.UUID, at time.Time) {
	t.Helper()
	tag, err := pool.Exec(context.Background(), `UPDATE tenants SET created_at = $2 WHERE id = $1::uuid`, id, at)
	if err != nil {
		t.Fatalf("set created_at: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("set created_at affected %d rows", tag.RowsAffected())
	}
}

func truncate(t *testing.T) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `TRUNCATE tenants CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func equalTenant(got, want Tenant) bool {
	return got.ID == want.ID &&
		got.Name == want.Name &&
		got.CreatedAt.Equal(want.CreatedAt) &&
		got.UpdatedAt.Equal(want.UpdatedAt)
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
