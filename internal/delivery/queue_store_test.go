package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmeenKhan12345/Eventrail/internal/platform/postgres"
)

var (
	testQueue *QueueStore
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
	testQueue = NewQueueStore(pool)

	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func TestClaimReturnsNilWhenNothingIsDue(t *testing.T) {
	truncate(t)
	claimed, err := testQueue.Claim(context.Background(), time.Minute)
	if err != nil || claimed != nil {
		t.Fatalf("claimed = %+v, err = %v", claimed, err)
	}
}

func TestClaimIncrementsAttemptAndKeepsNextAttempt(t *testing.T) {
	truncate(t)
	deliveryID := insertDelivery(t, -time.Second, "https://example.com/hook", true, false)
	before := mustDelivery(t, deliveryID)

	claimed, err := testQueue.Claim(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed == nil || claimed.DeliveryID != deliveryID || claimed.AttemptCount != 1 {
		t.Fatalf("claimed = %+v", claimed)
	}
	if claimed.URL != "https://example.com/hook" || !claimed.IsActive || claimed.DeletedAt != nil {
		t.Fatalf("endpoint fields = %+v", claimed)
	}
	if !bytes.Equal(claimed.SecretCiphertext, []byte("cipher")) || claimed.WebhookBody != `{"n":1}` {
		t.Fatalf("body %s secret %q", claimed.WebhookBody, claimed.SecretCiphertext)
	}

	after := mustDelivery(t, deliveryID)
	if after.attemptCount != 1 || !after.next.Equal(before.next) || after.lease == nil {
		t.Fatalf("after claim: %+v, before next %s", after, before.next)
	}
}

func TestClaimLeaseExpiresThenClaimableAgain(t *testing.T) {
	truncate(t)
	deliveryID := insertDelivery(t, -time.Second, "https://example.com/hook", true, false)
	before := mustDelivery(t, deliveryID)

	first, err := testQueue.Claim(context.Background(), 200*time.Millisecond)
	if err != nil || first == nil {
		t.Fatalf("first claim = %+v, err = %v", first, err)
	}
	held, err := testQueue.Claim(context.Background(), time.Minute)
	if err != nil || held != nil {
		t.Fatalf("leased row claimed again: %+v, err = %v", held, err)
	}

	time.Sleep(500 * time.Millisecond)

	second, err := testQueue.Claim(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	if second == nil || second.DeliveryID != deliveryID || second.AttemptCount != 2 {
		t.Fatalf("second claim = %+v", second)
	}
	after := mustDelivery(t, deliveryID)
	if after.attemptCount != 2 || !after.next.Equal(before.next) {
		t.Fatalf("after second claim: %+v, original next %s", after, before.next)
	}
}

func TestClaimSkipsFutureDeliveries(t *testing.T) {
	truncate(t)
	due := insertDelivery(t, -time.Second, "https://example.com/due", true, false)
	insertDelivery(t, time.Hour, "https://example.com/later", true, false)

	claimed, err := testQueue.Claim(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed == nil || claimed.DeliveryID != due {
		t.Fatalf("claimed = %+v, want the due row", claimed)
	}
	again, err := testQueue.Claim(context.Background(), time.Minute)
	if err != nil || again != nil {
		t.Fatalf("later claim = %+v, err = %v", again, err)
	}
}

func TestRecordResultRejectsStaleClaim(t *testing.T) {
	truncate(t)
	deliveryID := insertDelivery(t, -time.Second, "https://example.com/hook", true, false)

	first, err := testQueue.Claim(context.Background(), 200*time.Millisecond)
	if err != nil || first == nil {
		t.Fatalf("first claim = %+v, err = %v", first, err)
	}
	time.Sleep(500 * time.Millisecond)
	second, err := testQueue.Claim(context.Background(), time.Minute)
	if err != nil || second == nil || second.AttemptCount != 2 {
		t.Fatalf("second claim = %+v, err = %v", second, err)
	}

	err = testQueue.RecordResult(context.Background(), first, Result{FailureKind: FailureNone, StatusCode: 200}, Outcome{Kind: OutcomeSuccess})
	if !errors.Is(err, ErrStaleClaim) {
		t.Fatalf("err = %v, want ErrStaleClaim", err)
	}
	if n := countAttempts(t, deliveryID); n != 0 {
		t.Fatalf("attempts = %d, want 0", n)
	}
	row := mustDelivery(t, deliveryID)
	if row.status != "pending" || row.attemptCount != 2 {
		t.Fatalf("delivery changed by the stale result: %+v", row)
	}
}

func TestConcurrentClaimsAreDistinct(t *testing.T) {
	truncate(t)
	want := make(map[uuid.UUID]struct{}, 10)
	for i := range 10 {
		id := insertDelivery(t, -time.Second, fmt.Sprintf("https://example.com/hook/%d", i), true, false)
		want[id] = struct{}{}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	got := make([]uuid.UUID, 0, 10)
	start := make(chan struct{})
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := testQueue.Claim(context.Background(), time.Minute)
			if err != nil {
				t.Errorf("Claim: %v", err)
				return
			}
			if claimed == nil {
				return
			}
			mu.Lock()
			got = append(got, claimed.DeliveryID)
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if len(got) != len(want) {
		t.Fatalf("claimed %d rows, want %d", len(got), len(want))
	}
	seen := make(map[uuid.UUID]struct{}, len(got))
	for _, id := range got {
		if _, ok := want[id]; !ok {
			t.Fatalf("claimed unexpected delivery %s", id)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("delivery %s claimed twice", id)
		}
		seen[id] = struct{}{}
	}
}

func TestRecordResultOutcomes(t *testing.T) {
	tests := []struct {
		name           string
		outcome        Outcome
		result         Result
		wantStatus     string
		wantCompleted  bool
		wantStatusCode *int
	}{
		{
			name:           "success",
			outcome:        Outcome{Kind: OutcomeSuccess},
			result:         Result{StatusCode: 204, Latency: 1500 * time.Millisecond, FailureKind: FailureNone},
			wantStatus:     "succeeded",
			wantCompleted:  true,
			wantStatusCode: intPtr(204),
		},
		{
			name:    "final failure",
			outcome: Outcome{Kind: OutcomeFailure},
			result: Result{
				StatusCode:      500,
				Latency:         20 * time.Millisecond,
				FailureKind:     FailureNon2xx,
				ErrorText:       "status 500",
				ResponseExcerpt: "nope",
			},
			wantStatus:     "failed",
			wantCompleted:  true,
			wantStatusCode: intPtr(500),
		},
		{
			name:    "retry",
			outcome: Outcome{Kind: OutcomeRetry, Delay: time.Hour},
			result: Result{
				Latency:     10 * time.Second,
				FailureKind: FailureTimeout,
				ErrorText:   "timeout",
			},
			wantStatus:    "pending",
			wantCompleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncate(t)
			deliveryID := insertDelivery(t, -time.Second, "https://example.com/hook", true, false)
			claimed, err := testQueue.Claim(context.Background(), time.Minute)
			if err != nil || claimed == nil {
				t.Fatalf("Claim: claimed %+v err %v", claimed, err)
			}
			if err := testQueue.RecordResult(context.Background(), claimed, tt.result, tt.outcome); err != nil {
				t.Fatalf("RecordResult: %v", err)
			}

			row := mustDelivery(t, deliveryID)
			if row.status != tt.wantStatus || row.lease != nil || (row.completed != nil) != tt.wantCompleted {
				t.Fatalf("delivery = %+v", row)
			}
			if tt.outcome.Kind == OutcomeRetry {
				earliest := time.Now().Add(59 * time.Minute)
				latest := time.Now().Add(61 * time.Minute)
				if row.next.Before(earliest) || row.next.After(latest) {
					t.Fatalf("next_attempt_at = %s", row.next)
				}
			}

			attempt := mustAttempt(t, deliveryID)
			if attempt.number != claimed.AttemptCount || attempt.latency != int(tt.result.Latency.Milliseconds()) {
				t.Fatalf("attempt = %+v", attempt)
			}
			if attempt.kind != string(tt.result.FailureKind) || attempt.errorText != tt.result.ErrorText || attempt.excerpt != tt.result.ResponseExcerpt {
				t.Fatalf("attempt text = %+v", attempt)
			}
			if !sameIntPtr(attempt.statusCode, tt.wantStatusCode) {
				t.Fatalf("status_code = %v, want %v", attempt.statusCode, tt.wantStatusCode)
			}

			again, err := testQueue.Claim(context.Background(), time.Minute)
			if err != nil || again != nil {
				t.Fatalf("claimed after record = %+v, err = %v", again, err)
			}
		})
	}
}

type deliveryState struct {
	attemptCount int
	next         time.Time
	lease        *time.Time
	status       string
	completed    *time.Time
}

type attemptState struct {
	number     int
	statusCode *int
	latency    int
	kind       string
	errorText  string
	excerpt    string
}

func insertDelivery(t *testing.T, dueIn time.Duration, rawURL string, active, deleted bool) uuid.UUID {
	t.Helper()
	var tenantID uuid.UUID
	if err := pool.QueryRow(context.Background(), `INSERT INTO tenants (name) VALUES ('owner') RETURNING id`).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	var eventID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO events (tenant_id, event_type, payload, webhook_body)
		VALUES ($1, 'order.created', '{"n":1}'::jsonb, '{"n":1}')
		RETURNING id
	`, tenantID).Scan(&eventID); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	var deletedAt *time.Time
	if deleted {
		now := time.Now()
		deletedAt = &now
	}
	var endpointID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO endpoints (tenant_id, url, secret_ciphertext, is_active, deleted_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, tenantID, rawURL, []byte("cipher"), active, deletedAt).Scan(&endpointID); err != nil {
		t.Fatalf("insert endpoint: %v", err)
	}
	var deliveryID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO deliveries (tenant_id, event_id, endpoint_id, next_attempt_at)
		VALUES ($1, $2, $3, now() + ($4 * interval '1 millisecond'))
		RETURNING id
	`, tenantID, eventID, endpointID, dueIn.Milliseconds()).Scan(&deliveryID); err != nil {
		t.Fatalf("insert delivery: %v", err)
	}
	return deliveryID
}

func mustDelivery(t *testing.T, id uuid.UUID) deliveryState {
	t.Helper()
	var row deliveryState
	err := pool.QueryRow(context.Background(), `
		SELECT attempt_count, next_attempt_at, lease_until, status, completed_at
		FROM deliveries
		WHERE id = $1
	`, id).Scan(&row.attemptCount, &row.next, &row.lease, &row.status, &row.completed)
	if err != nil {
		t.Fatalf("delivery: %v", err)
	}
	return row
}

func mustAttempt(t *testing.T, deliveryID uuid.UUID) attemptState {
	t.Helper()
	var row attemptState
	err := pool.QueryRow(context.Background(), `
		SELECT attempt_number, status_code, latency_ms, failure_kind, error_text, response_excerpt
		FROM delivery_attempts
		WHERE delivery_id = $1
	`, deliveryID).Scan(&row.number, &row.statusCode, &row.latency, &row.kind, &row.errorText, &row.excerpt)
	if err != nil {
		t.Fatalf("attempt: %v", err)
	}
	return row
}

func countAttempts(t *testing.T, deliveryID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM delivery_attempts WHERE delivery_id = $1`, deliveryID).Scan(&n); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	return n
}

func intPtr(n int) *int {
	return &n
}

func sameIntPtr(got, want *int) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
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
