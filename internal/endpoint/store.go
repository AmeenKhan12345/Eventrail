package endpoint

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store runs endpoint SQL. Every query is scoped to one tenant and ignores
// soft-deleted rows.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps a pool. The caller owns the pool and closes it.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const endpointColumns = `id, tenant_id, url, description, is_active, created_at, updated_at`

// Create inserts a live endpoint and returns the stored row. The secret column
// is ciphertext only.
func (s *Store) Create(ctx context.Context, tenantID uuid.UUID, url, description string, secretCiphertext []byte) (Endpoint, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO endpoints (tenant_id, url, description, secret_ciphertext)
		VALUES ($1, $2, $3, $4)
		RETURNING `+endpointColumns,
		tenantID, url, description, secretCiphertext)
	endpoint, err := scanEndpoint(row)
	if err != nil {
		return Endpoint{}, wrapErr("create endpoint", err)
	}
	return endpoint, nil
}

// Get returns one live endpoint owned by the tenant.
func (s *Store) Get(ctx context.Context, tenantID, id uuid.UUID) (Endpoint, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+endpointColumns+`
		FROM endpoints
		WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
	`, tenantID, id)
	endpoint, err := scanEndpoint(row)
	if err != nil {
		return Endpoint{}, wrapErr("get endpoint", err)
	}
	return endpoint, nil
}

// List returns the tenant's live endpoints, newest first.
func (s *Store) List(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]Endpoint, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+endpointColumns+`
		FROM endpoints
		WHERE tenant_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC, id ASC
		LIMIT $2 OFFSET $3
	`, tenantID, limit, offset)
	if err != nil {
		return nil, wrapErr("list endpoints", err)
	}
	defer rows.Close()

	endpoints := []Endpoint{}
	for rows.Next() {
		endpoint, err := scanEndpoint(rows)
		if err != nil {
			return nil, wrapErr("list endpoints", err)
		}
		endpoints = append(endpoints, endpoint)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapErr("list endpoints", err)
	}
	return endpoints, nil
}

// Update changes only the fields whose pointers are non-nil and always sets updated_at.
func (s *Store) Update(ctx context.Context, tenantID, id uuid.UUID, url, description *string, isActive *bool) (Endpoint, error) {
	sets := []string{"updated_at = now()"}
	args := []any{tenantID, id}
	if url != nil {
		args = append(args, *url)
		sets = append(sets, fmt.Sprintf("url = $%d", len(args)))
	}
	if description != nil {
		args = append(args, *description)
		sets = append(sets, fmt.Sprintf("description = $%d", len(args)))
	}
	if isActive != nil {
		args = append(args, *isActive)
		sets = append(sets, fmt.Sprintf("is_active = $%d", len(args)))
	}

	query := `
		UPDATE endpoints
		SET ` + strings.Join(sets, ", ") + `
		WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
		RETURNING ` + endpointColumns
	endpoint, err := scanEndpoint(s.pool.QueryRow(ctx, query, args...))
	if err != nil {
		return Endpoint{}, wrapErr("update endpoint", err)
	}
	return endpoint, nil
}

// SoftDelete marks the tenant's live endpoint deleted. A missing or already
// deleted row is ErrNotFound.
func (s *Store) SoftDelete(ctx context.Context, tenantID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE endpoints
		SET deleted_at = now()
		WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
	`, tenantID, id)
	if err != nil {
		return wrapErr("delete endpoint", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RotateSecret replaces the ciphertext for the tenant's live endpoint.
func (s *Store) RotateSecret(ctx context.Context, tenantID, id uuid.UUID, secretCiphertext []byte) (Endpoint, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE endpoints
		SET secret_ciphertext = $3, updated_at = now()
		WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL
		RETURNING `+endpointColumns,
		tenantID, id, secretCiphertext)
	endpoint, err := scanEndpoint(row)
	if err != nil {
		return Endpoint{}, wrapErr("rotate endpoint secret", err)
	}
	return endpoint, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEndpoint(row rowScanner) (Endpoint, error) {
	var endpoint Endpoint
	err := row.Scan(
		&endpoint.ID,
		&endpoint.TenantID,
		&endpoint.URL,
		&endpoint.Description,
		&endpoint.IsActive,
		&endpoint.CreatedAt,
		&endpoint.UpdatedAt,
	)
	return endpoint, err
}

func wrapErr(action string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return fmt.Errorf("%s: %w", action, err)
}
