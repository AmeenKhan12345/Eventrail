package tenant

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store runs tenant SQL. Callers pass context and receive domain errors.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps a pool. The caller owns the pool and closes it.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const createSQL = `
INSERT INTO tenants (name)
VALUES ($1)
RETURNING id, name, created_at, updated_at
`

const getByIDSQL = `
SELECT id, name, created_at, updated_at
FROM tenants
WHERE id = $1::uuid
`

const listSQL = `
SELECT id, name, created_at, updated_at
FROM tenants
ORDER BY created_at DESC, id ASC
LIMIT $1 OFFSET $2
`

// Create inserts a tenant and returns the row Postgres stored.
func (s *Store) Create(ctx context.Context, name string) (Tenant, error) {
	tenant, err := scanTenant(s.pool.QueryRow(ctx, createSQL, name))
	if err != nil {
		return Tenant{}, wrapErr("create tenant", err)
	}
	return tenant, nil
}

// GetByID returns one tenant. A missing id is ErrNotFound.
func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (Tenant, error) {
	tenant, err := scanTenant(s.pool.QueryRow(ctx, getByIDSQL, id))
	if err != nil {
		return Tenant{}, wrapErr("get tenant", err)
	}
	return tenant, nil
}

// List returns tenants ordered by created_at descending, then id ascending.
func (s *Store) List(ctx context.Context, limit, offset int) ([]Tenant, error) {
	rows, err := s.pool.Query(ctx, listSQL, limit, offset)
	if err != nil {
		return nil, wrapErr("list tenants", err)
	}
	defer rows.Close()

	tenants := []Tenant{}
	for rows.Next() {
		tenant, err := scanTenant(rows)
		if err != nil {
			return nil, wrapErr("list tenants", err)
		}
		tenants = append(tenants, tenant)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapErr("list tenants", err)
	}
	return tenants, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTenant(row rowScanner) (Tenant, error) {
	var tenant Tenant
	err := row.Scan(&tenant.ID, &tenant.Name, &tenant.CreatedAt, &tenant.UpdatedAt)
	return tenant, err
}

// wrapErr turns a missing row into ErrNotFound.
// Every other error is wrapped with %w so errors.Is can still see
// context.Canceled and context.DeadlineExceeded.
func wrapErr(action string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", action, err)
}
