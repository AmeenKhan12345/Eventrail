package event

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store runs event SQL. Every query is scoped to one tenant.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps a pool. The caller owns the pool and closes it.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const eventColumns = `id, tenant_id, event_type, payload, idempotency_key, request_fingerprint, created_at`

// CreateWithFanOut inserts the event and one pending delivery per matching
// endpoint in a single transaction. A matching endpoint is active, not
// deleted, owned by the same tenant, and either has no event types or lists
// this event type.
//
// When idempotencyKey is set and that key already exists, the existing event
// is returned with replay true and no new deliveries. A different fingerprint
// for the same key is ErrConflict. A nil key always inserts a new event.
func (s *Store) CreateWithFanOut(ctx context.Context, tenantID uuid.UUID, eventType string, payload []byte, idempotencyKey *string, fingerprint string) (Event, int, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Event{}, 0, false, wrapErr("create event", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	event, inserted, err := insertEvent(ctx, tx, tenantID, eventType, payload, idempotencyKey, fingerprint)
	if err != nil {
		return Event{}, 0, false, err
	}
	if !inserted {
		existing, err := eventByKey(ctx, tx, tenantID, *idempotencyKey)
		if err != nil {
			return Event{}, 0, false, err
		}
		if !sameFingerprint(existing.Fingerprint, fingerprint) {
			return Event{}, 0, false, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Event{}, 0, false, wrapErr("create event", err)
		}
		return existing, 0, true, nil
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO deliveries (tenant_id, event_id, endpoint_id, next_attempt_at)
		SELECT $1, $2, id, now()
		FROM endpoints
		WHERE tenant_id = $1
		  AND deleted_at IS NULL
		  AND is_active = true
		  AND (cardinality(event_types) = 0 OR $3 = ANY (event_types))
	`, tenantID, event.ID, eventType)
	if err != nil {
		return Event{}, 0, false, wrapErr("create deliveries", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Event{}, 0, false, wrapErr("create event", err)
	}
	return event, int(tag.RowsAffected()), false, nil
}

func insertEvent(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, eventType string, payload []byte, idempotencyKey *string, fingerprint string) (Event, bool, error) {
	query := `
		INSERT INTO events (tenant_id, event_type, payload, idempotency_key, request_fingerprint)
		VALUES ($1, $2, $3::jsonb, $4, $5)
		RETURNING ` + eventColumns
	args := []any{tenantID, eventType, string(payload), idempotencyKey, fingerprint}
	if idempotencyKey != nil {
		query = `
			INSERT INTO events (tenant_id, event_type, payload, idempotency_key, request_fingerprint)
			VALUES ($1, $2, $3::jsonb, $4, $5)
			ON CONFLICT (tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL
			DO NOTHING
			RETURNING ` + eventColumns
	}
	event, err := scanEvent(tx.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, false, nil
	}
	if err != nil {
		return Event{}, false, wrapErr("create event", err)
	}
	return event, true, nil
}

func eventByKey(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, idempotencyKey string) (Event, error) {
	event, err := scanEvent(tx.QueryRow(ctx, `
		SELECT `+eventColumns+`
		FROM events
		WHERE tenant_id = $1 AND idempotency_key = $2
	`, tenantID, idempotencyKey))
	if err != nil {
		return Event{}, wrapErr("get event", err)
	}
	return event, nil
}

func sameFingerprint(stored, got string) bool {
	if len(stored) != len(got) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(got)) == 1
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEvent(row rowScanner) (Event, error) {
	var event Event
	err := row.Scan(
		&event.ID,
		&event.TenantID,
		&event.Type,
		&event.Payload,
		&event.IdempotencyKey,
		&event.Fingerprint,
		&event.CreatedAt,
	)
	return event, err
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
