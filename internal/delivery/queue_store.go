package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrStaleClaim means this claim no longer owns the delivery.
// A newer claim, or a result already recorded for this one, got there first.
var ErrStaleClaim = errors.New("stale delivery claim")

// OutcomeKind says how a claimed delivery finishes.
type OutcomeKind string

const (
	// OutcomeSuccess marks the delivery succeeded.
	OutcomeSuccess OutcomeKind = "success"
	// OutcomeRetry puts the delivery back, to be claimed after Delay.
	OutcomeRetry OutcomeKind = "retry"
	// OutcomeFailure marks the delivery failed on its last attempt.
	OutcomeFailure OutcomeKind = "failure"
)

// Outcome is the decision for one claimed delivery. Delay is used only for a retry.
type Outcome struct {
	Kind  OutcomeKind
	Delay time.Duration
}

// Claimed is one delivery a worker owns until the lease ends or the result is recorded.
// SecretCiphertext is the encrypted endpoint secret. Callers must not log it.
type Claimed struct {
	DeliveryID       uuid.UUID
	TenantID         uuid.UUID
	AttemptCount     int
	EventID          uuid.UUID
	WebhookBody      string
	URL              string
	SecretCiphertext []byte
	IsActive         bool
	DeletedAt        *time.Time
}

// QueueStore claims due deliveries and records one attempt.
// The worker is the caller. This type does not send HTTP.
type QueueStore struct {
	pool *pgxpool.Pool
}

// NewQueueStore wraps a pool. The caller owns the pool and closes it.
func NewQueueStore(pool *pgxpool.Pool) *QueueStore {
	return &QueueStore{pool: pool}
}

// Claim takes one due delivery and returns. The statement ends before Claim
// returns, so no transaction stays open while the caller does its HTTP work.
//
// A due row is pending, its next_attempt_at has arrived, and nobody holds a
// live lease. lease is how long this claim owns the row. attempt_count
// increases here, before the HTTP call, and next_attempt_at is left unchanged.
// The worker reads every tenant's queue; each returned row still carries its tenant_id.
// A nil Claimed and a nil error means nothing is due.
func (s *QueueStore) Claim(ctx context.Context, lease time.Duration) (*Claimed, error) {
	if lease <= 0 {
		return nil, fmt.Errorf("claim delivery: lease must be positive")
	}
	var claimed Claimed
	err := s.pool.QueryRow(ctx, `
		UPDATE deliveries AS d
		SET attempt_count = d.attempt_count + 1,
		    lease_until = now() + ($1 * interval '1 millisecond'),
		    updated_at = now()
		FROM events AS e, endpoints AS ep
		WHERE d.id = (
		    SELECT id
		    FROM deliveries
		    WHERE status = 'pending'
		      AND next_attempt_at <= now()
		      AND (lease_until IS NULL OR lease_until <= now())
		    ORDER BY next_attempt_at, id
		    FOR UPDATE SKIP LOCKED
		    LIMIT 1
		)
		  AND e.id = d.event_id
		  AND e.tenant_id = d.tenant_id
		  AND ep.id = d.endpoint_id
		  AND ep.tenant_id = d.tenant_id
		RETURNING d.id, d.tenant_id, d.attempt_count, d.event_id,
		          e.webhook_body, ep.url, ep.secret_ciphertext, ep.is_active, ep.deleted_at
	`, lease.Milliseconds()).Scan(
		&claimed.DeliveryID,
		&claimed.TenantID,
		&claimed.AttemptCount,
		&claimed.EventID,
		&claimed.WebhookBody,
		&claimed.URL,
		&claimed.SecretCiphertext,
		&claimed.IsActive,
		&claimed.DeletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim delivery: %w", err)
	}
	return &claimed, nil
}

// RecordResult finishes a claim in one transaction. The delivery update is
// guarded by the attempt_count from the claim and by status pending. Zero
// rows means a newer claim owns the row, or this result was already recorded:
// the transaction rolls back and returns ErrStaleClaim, and no attempt is inserted.
// The attempt row is inserted only after that guard matches.
func (s *QueueStore) RecordResult(ctx context.Context, claimed *Claimed, result Result, outcome Outcome) error {
	if claimed == nil {
		return fmt.Errorf("record delivery result: claimed delivery is required")
	}
	query, args, err := finishDelivery(claimed, outcome)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("record delivery result: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("record delivery result: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrStaleClaim
	}
	if err := insertAttempt(ctx, tx, claimed, result); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("record delivery result: %w", err)
	}
	return nil
}

func finishDelivery(claimed *Claimed, outcome Outcome) (string, []any, error) {
	args := []any{claimed.DeliveryID, claimed.TenantID, claimed.AttemptCount}
	switch outcome.Kind {
	case OutcomeSuccess:
		return `
			UPDATE deliveries
			SET lease_until = NULL,
			    status = 'succeeded',
			    completed_at = now(),
			    updated_at = now()
			WHERE id = $1
			  AND tenant_id = $2
			  AND attempt_count = $3
			  AND status = 'pending'
		`, args, nil
	case OutcomeFailure:
		return `
			UPDATE deliveries
			SET lease_until = NULL,
			    status = 'failed',
			    completed_at = now(),
			    updated_at = now()
			WHERE id = $1
			  AND tenant_id = $2
			  AND attempt_count = $3
			  AND status = 'pending'
		`, args, nil
	case OutcomeRetry:
		if outcome.Delay < 0 {
			return "", nil, fmt.Errorf("record delivery result: retry delay must not be negative")
		}
		args = append(args, outcome.Delay.Milliseconds())
		return `
			UPDATE deliveries
			SET lease_until = NULL,
			    next_attempt_at = now() + ($4 * interval '1 millisecond'),
			    updated_at = now()
			WHERE id = $1
			  AND tenant_id = $2
			  AND attempt_count = $3
			  AND status = 'pending'
		`, args, nil
	default:
		return "", nil, fmt.Errorf("record delivery result: unknown outcome %q", outcome.Kind)
	}
}

func insertAttempt(ctx context.Context, tx pgx.Tx, claimed *Claimed, result Result) error {
	var statusCode *int
	if result.StatusCode != 0 {
		code := result.StatusCode
		statusCode = &code
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO delivery_attempts (
		    delivery_id, tenant_id, attempt_number, status_code, latency_ms,
		    failure_kind, error_text, response_excerpt
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`,
		claimed.DeliveryID,
		claimed.TenantID,
		claimed.AttemptCount,
		statusCode,
		result.Latency.Milliseconds(),
		string(result.FailureKind),
		result.ErrorText,
		result.ResponseExcerpt,
	)
	if err != nil {
		return fmt.Errorf("record delivery attempt: %w", err)
	}
	return nil
}
