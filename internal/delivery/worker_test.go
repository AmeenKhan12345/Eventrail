package delivery

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AmeenKhan12345/Eventrail/internal/secrets"
)

func TestNewWorkerConfig(t *testing.T) {
	box, _ := newBox(t)
	sender := NewSender(Config{Timeout: 10 * time.Second}, PermissivePolicy)
	tests := []struct {
		name        string
		cfg         WorkerConfig
		wantErr     bool
		concurrency int
		lease       time.Duration
		poll        time.Duration
		grace       time.Duration
	}{
		{
			name:        "defaults",
			concurrency: 10,
			lease:       60 * time.Second,
			poll:        time.Second,
			grace:       15 * time.Second,
		},
		{
			name:    "lease below timeout plus 10s",
			cfg:     WorkerConfig{Concurrency: 1, Lease: 19 * time.Second},
			wantErr: true,
		},
		{
			name:        "lease equal to timeout plus 10s",
			cfg:         WorkerConfig{Concurrency: 2, Lease: 20 * time.Second},
			concurrency: 2,
			lease:       20 * time.Second,
			poll:        time.Second,
			grace:       15 * time.Second,
		},
		{
			name:    "concurrency below 1",
			cfg:     WorkerConfig{Concurrency: -1, Lease: time.Minute},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker, err := NewWorker(testQueue, box, sender, tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewWorker: %v", err)
			}
			if worker.concurrency != tt.concurrency || worker.lease != tt.lease || worker.pollInterval != tt.poll || worker.shutdownGrace != tt.grace {
				t.Fatalf("concurrency %d lease %s poll %s grace %s", worker.concurrency, worker.lease, worker.pollInterval, worker.shutdownGrace)
			}
		})
	}
}

func TestWorkerDeliversStoredBody(t *testing.T) {
	truncate(t)
	body := []byte(`{"id":"evt","type":"order.created","data":{"n":1}}`)
	box, secret := newBox(t)
	var got inbox
	server := httptest.NewServer(got.handler(http.StatusOK))
	defer server.Close()

	_, ids := seedDeliveries(t, box, secret, server.URL, string(body), 1, true, false)
	runWorker(t, newTestWorker(t, box, 1, 0))
	waitFor(t, 5*time.Second, func() bool {
		return mustDelivery(t, ids[0]).status == "succeeded"
	})

	calls := got.snapshot()
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	call := calls[0]
	if call.contentType != "application/json" || call.userAgent != "Eventrail/1.0" || call.id != ids[0].String() {
		t.Fatalf("headers type %q agent %q id %q", call.contentType, call.userAgent, call.id)
	}
	if !bytes.Equal(call.body, body) {
		t.Fatalf("body = %s, want %s", call.body, body)
	}
	if err := Verify(secret, call.id, call.timestamp, call.signature, call.body, time.Now(), time.Minute); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	attempt := mustAttempt(t, ids[0])
	if attempt.number != 1 || attempt.kind != string(FailureNone) || attempt.statusCode == nil || *attempt.statusCode != http.StatusOK {
		t.Fatalf("attempt = %+v", attempt)
	}
}

func TestWorkerRetryAfter500(t *testing.T) {
	truncate(t)
	box, secret := newBox(t)
	var got inbox
	server := httptest.NewServer(got.handler(http.StatusInternalServerError))
	defer server.Close()

	_, ids := seedDeliveries(t, box, secret, server.URL, `{"n":1}`, 1, true, false)
	runWorker(t, newTestWorker(t, box, 1, 0))
	waitFor(t, 5*time.Second, func() bool {
		return countAttempts(t, ids[0]) == 1
	})

	row := mustDelivery(t, ids[0])
	if row.status != "pending" || row.completed != nil {
		t.Fatalf("delivery = %+v", row)
	}
	if !row.next.After(time.Now().Add(20 * time.Second)) {
		t.Fatalf("next_attempt_at = %s", row.next)
	}
	if len(got.snapshot()) != 1 {
		t.Fatalf("calls = %d", len(got.snapshot()))
	}
	attempt := mustAttempt(t, ids[0])
	if attempt.kind != string(FailureNon2xx) || attempt.statusCode == nil || *attempt.statusCode != http.StatusInternalServerError {
		t.Fatalf("attempt = %+v", attempt)
	}
}

func TestWorkerFailsAfterEightAttempts(t *testing.T) {
	truncate(t)
	box, secret := newBox(t)
	var got inbox
	server := httptest.NewServer(got.handler(http.StatusInternalServerError))
	defer server.Close()

	_, ids := seedDeliveries(t, box, secret, server.URL, `{"n":1}`, 1, true, false)
	id := ids[0]
	runWorker(t, newTestWorker(t, box, 1, 0))

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if mustDelivery(t, id).status == "failed" {
			break
		}
		if _, err := pool.Exec(context.Background(), `
			UPDATE deliveries
			SET next_attempt_at = now()
			WHERE id = $1 AND status = 'pending' AND lease_until IS NULL
		`, id); err != nil {
			t.Fatalf("make due: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if mustDelivery(t, id).status != "failed" {
		t.Fatalf("status = %s", mustDelivery(t, id).status)
	}
	if countAttempts(t, id) != 8 || len(got.snapshot()) != 8 {
		t.Fatalf("attempts %d calls %d", countAttempts(t, id), len(got.snapshot()))
	}
}

func TestWorkerExhaustedClaimSkipsHTTP(t *testing.T) {
	truncate(t)
	box, secret := newBox(t)
	var got inbox
	server := httptest.NewServer(got.handler(http.StatusOK))
	defer server.Close()

	_, ids := seedDeliveries(t, box, secret, server.URL, `{"n":1}`, 1, true, false)
	if _, err := pool.Exec(context.Background(), `UPDATE deliveries SET attempt_count = 8 WHERE id = $1`, ids[0]); err != nil {
		t.Fatalf("set attempt count: %v", err)
	}
	runWorker(t, newTestWorker(t, box, 1, 0))
	waitFor(t, 5*time.Second, func() bool {
		return mustDelivery(t, ids[0]).status == "failed"
	})

	if len(got.snapshot()) != 0 {
		t.Fatalf("receiver calls = %d", len(got.snapshot()))
	}
	attempt := mustAttempt(t, ids[0])
	if attempt.kind != string(FailureAttemptsExhausted) || attempt.number != 9 {
		t.Fatalf("attempt = %+v", attempt)
	}
}

func TestWorkerSendsEachDeliveryOnce(t *testing.T) {
	truncate(t)
	box, secret := newBox(t)
	var got inbox
	server := httptest.NewServer(got.handler(http.StatusOK))
	defer server.Close()

	tenantID, ids := seedDeliveries(t, box, secret, server.URL, `{"n":1}`, 50, true, false)
	runWorker(t, newTestWorker(t, box, 5, 0))
	waitFor(t, 15*time.Second, func() bool {
		return countStatus(t, tenantID, "succeeded") == 50 && len(got.snapshot()) == 50
	})
	time.Sleep(50 * time.Millisecond)

	calls := got.snapshot()
	if len(calls) != 50 {
		t.Fatalf("calls = %d", len(calls))
	}
	seen := make(map[string]int, len(calls))
	for _, call := range calls {
		seen[call.id]++
	}
	for _, id := range ids {
		if seen[id.String()] != 1 {
			t.Fatalf("delivery %s calls = %d", id, seen[id.String()])
		}
	}
}

func TestWorkerDecryptFailureIsPermanent(t *testing.T) {
	truncate(t)
	box, secret := newBox(t)
	var got inbox
	server := httptest.NewServer(got.handler(http.StatusOK))
	defer server.Close()

	_, ids := seedDeliveries(t, box, secret, server.URL, `{"n":1}`, 1, true, false)
	if _, err := pool.Exec(context.Background(), `
		UPDATE endpoints
		SET secret_ciphertext = $1
		WHERE id = (SELECT endpoint_id FROM deliveries WHERE id = $2)
	`, []byte("not-a-valid-ciphertext"), ids[0]); err != nil {
		t.Fatalf("corrupt secret: %v", err)
	}

	runWorker(t, newTestWorker(t, box, 1, 0))
	waitFor(t, 5*time.Second, func() bool {
		return mustDelivery(t, ids[0]).status == "failed"
	})
	time.Sleep(30 * time.Millisecond)

	if len(got.snapshot()) != 0 {
		t.Fatalf("receiver calls = %d", len(got.snapshot()))
	}
	row := mustDelivery(t, ids[0])
	if row.status != "failed" || countAttempts(t, ids[0]) != 1 {
		t.Fatalf("status %s attempts %d", row.status, countAttempts(t, ids[0]))
	}
	attempt := mustAttempt(t, ids[0])
	if attempt.kind != string(FailureDecrypt) {
		t.Fatalf("attempt = %+v", attempt)
	}
}

func TestWorkerUnavailableEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		active  bool
		deleted bool
	}{
		{name: "deleted", active: true, deleted: true},
		{name: "inactive", active: false, deleted: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncate(t)
			box, secret := newBox(t)
			var got inbox
			server := httptest.NewServer(got.handler(http.StatusOK))
			defer server.Close()

			_, ids := seedDeliveries(t, box, secret, server.URL, `{"n":1}`, 1, tt.active, tt.deleted)
			runWorker(t, newTestWorker(t, box, 1, 0))
			waitFor(t, 5*time.Second, func() bool {
				return mustDelivery(t, ids[0]).status == "failed"
			})
			time.Sleep(30 * time.Millisecond)

			if len(got.snapshot()) != 0 {
				t.Fatalf("receiver calls = %d", len(got.snapshot()))
			}
			attempt := mustAttempt(t, ids[0])
			if attempt.kind != string(FailureEndpointUnavailable) {
				t.Fatalf("attempt = %+v", attempt)
			}
		})
	}
}

func TestWorkerRecordsInFlightDeliveryOnShutdown(t *testing.T) {
	truncate(t)
	box, secret := newBox(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		startOnce.Do(func() { close(started) })
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, ids := seedDeliveries(t, box, secret, server.URL, `{"n":1}`, 1, true, false)
	grace := 3 * time.Second
	worker := newTestWorker(t, box, 1, grace)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = worker.Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("worker did not stop")
		}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("send did not start")
	}
	begin := time.Now()
	cancel()
	releaseOnce.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(grace + time.Second):
		t.Fatal("Run did not return within grace")
	}
	if elapsed := time.Since(begin); elapsed > grace {
		t.Fatalf("Run took %s, grace is %s", elapsed, grace)
	}
	if mustDelivery(t, ids[0]).status != "succeeded" || countAttempts(t, ids[0]) != 1 {
		t.Fatalf("status %s attempts %d", mustDelivery(t, ids[0]).status, countAttempts(t, ids[0]))
	}
}

type httpCall struct {
	id          string
	timestamp   string
	signature   string
	contentType string
	userAgent   string
	body        []byte
}

type inbox struct {
	mu    sync.Mutex
	calls []httpCall
}

func (b *inbox) handler(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		b.mu.Lock()
		b.calls = append(b.calls, httpCall{
			id:          r.Header.Get("webhook-id"),
			timestamp:   r.Header.Get("webhook-timestamp"),
			signature:   r.Header.Get("webhook-signature"),
			contentType: r.Header.Get("Content-Type"),
			userAgent:   r.Header.Get("User-Agent"),
			body:        body,
		})
		b.mu.Unlock()
		w.WriteHeader(status)
	}
}

func (b *inbox) snapshot() []httpCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]httpCall, len(b.calls))
	copy(out, b.calls)
	return out
}

func newBox(t *testing.T) (*secrets.Box, string) {
	t.Helper()
	box, err := secrets.NewBox(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatalf("box: %v", err)
	}
	secret, err := box.GenerateSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	return box, secret
}

func newTestWorker(t *testing.T, box *secrets.Box, concurrency int, grace time.Duration) *Worker {
	t.Helper()
	timeout := time.Second
	sender := NewSender(Config{Timeout: timeout, AllowInsecure: true}, PermissivePolicy)
	worker, err := NewWorker(testQueue, box, sender, WorkerConfig{
		Concurrency:   concurrency,
		PollInterval:  20 * time.Millisecond,
		Lease:         timeout + leaseMargin,
		ShutdownGrace: grace,
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	return worker
}

func runWorker(t *testing.T, worker *Worker) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = worker.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("worker did not stop")
		}
	})
}

func seedDeliveries(t *testing.T, box *secrets.Box, secret, rawURL, body string, n int, active, deleted bool) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	ciphertext, err := box.Encrypt(secret)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	var tenantID uuid.UUID
	if err := pool.QueryRow(context.Background(), `INSERT INTO tenants (name) VALUES ('owner') RETURNING id`).Scan(&tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
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
	`, tenantID, rawURL, ciphertext, active, deletedAt).Scan(&endpointID); err != nil {
		t.Fatalf("insert endpoint: %v", err)
	}
	ids := make([]uuid.UUID, 0, n)
	for range n {
		var eventID uuid.UUID
		if err := pool.QueryRow(context.Background(), `
			INSERT INTO events (tenant_id, event_type, payload, webhook_body)
			VALUES ($1, 'order.created', '{}'::jsonb, $2)
			RETURNING id
		`, tenantID, body).Scan(&eventID); err != nil {
			t.Fatalf("insert event: %v", err)
		}
		var deliveryID uuid.UUID
		if err := pool.QueryRow(context.Background(), `
			INSERT INTO deliveries (tenant_id, event_id, endpoint_id, next_attempt_at)
			VALUES ($1, $2, $3, now() - interval '1 second')
			RETURNING id
		`, tenantID, eventID, endpointID).Scan(&deliveryID); err != nil {
			t.Fatalf("insert delivery: %v", err)
		}
		ids = append(ids, deliveryID)
	}
	return tenantID, ids
}

func waitFor(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func countStatus(t *testing.T, tenantID uuid.UUID, status string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM deliveries WHERE tenant_id = $1 AND status = $2`, tenantID, status).Scan(&n); err != nil {
		t.Fatalf("count status: %v", err)
	}
	return n
}
