package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no API key has this prefix.
var ErrNotFound = errors.New("api key not found")

// Key is the stored material needed to authenticate a tenant.
type Key struct {
	TenantID uuid.UUID
	Hash     string
	Revoked  bool
}

// Store reads API keys. The caller owns the pool.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps a pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// GetByPrefix loads the key row for a lookup prefix. It does not update last_used_at.
func (s *Store) GetByPrefix(ctx context.Context, prefix string) (Key, error) {
	var key Key
	err := s.pool.QueryRow(ctx, `
		SELECT tenant_id, key_hash, revoked_at IS NOT NULL
		FROM api_keys
		WHERE key_prefix = $1
	`, prefix).Scan(&key.TenantID, &key.Hash, &key.Revoked)
	if err != nil {
		return Key{}, wrapErr(err)
	}
	return key, nil
}

func wrapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("get api key: %w", err)
}
