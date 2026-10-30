package event

import (
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

func TestFanOutMatchesSubscribedEndpoints(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	owner := insertTenant(t, "owner")
	other := insertTenant(t, "other")

	all := insertEndpoint(t, owner, "https://example.com/all", true, nil, false)
	orders := insertEndpoint(t, owner, "https://example.com/orders", true, []string{"order.created"}, false)
	invoices := insertEndpoint(t, owner, "https://example.com/invoices", true, []string{"invoice.paid"}, false)
	inactive := insertEndpoint(t, owner, "https://example.com/inactive", false, nil, false)
	deleted := insertEndpoint(t, owner, "https://example.com/deleted", true, nil, true)
	foreign := insertEndpoint(t, other, "https://example.com/foreign", true, nil, false)

	event, n, replay, err := testStore.CreateWithFanOut(ctx, owner, "order.created", []byte(`{"id":1}`), nil, "fingerprint-1")
	if err != nil {
		t.Fatalf("CreateWithFanOut: %v", err)
	}
	if replay || n != 2 || event.ID == uuid.Nil {
		t.Fatalf("event %s deliveries %d replay %v", event.ID, n, replay)
	}

	got := deliveryEndpoints(t, event.ID)
	if !sameIDs(got, []uuid.UUID{all, orders}) {
		t.Fatalf("deliveries = %v, want all and orders", got)
	}
	for _, skipped := range []uuid.UUID{invoices, inactive, deleted, foreign} {
		if containsID(got, skipped) {
			t.Fatalf("deliveries included %s", skipped)
		}
	}

	var status string
	var attempts int
	var next time.Time
	err = pool.QueryRow(ctx, `
		SELECT status, attempt_count, next_attempt_at
		FROM deliveries
		WHERE event_id = $1
		LIMIT 1
	`, event.ID).Scan(&status, &attempts, &next)
	if err != nil {
		t.Fatalf("delivery row: %v", err)
	}
	if status != "pending" || attempts != 0 || next.IsZero() {
		t.Fatalf("status %q attempts %d next %s", status, attempts, next)
	}
}

func TestEventWithNoEndpointsIsStored(t *testing.T) {
	truncate(t)
	tenantID := insertTenant(t, "owner")
	event, n, replay, err := testStore.CreateWithFanOut(context.Background(), tenantID, "order.created", []byte(`{}`), nil, "fingerprint")
	if err != nil {
		t.Fatalf("CreateWithFanOut: %v", err)
	}
	if replay || n != 0 {
		t.Fatalf("deliveries %d replay %v", n, replay)
	}
	if countEvents(t, tenantID) != 1 || event.ID == uuid.Nil {
		t.Fatalf("events = %d id %s", countEvents(t, tenantID), event.ID)
	}
}

func TestIdempotencyReplayAndConflict(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	tenantID := insertTenant(t, "owner")
	insertEndpoint(t, tenantID, "https://example.com/all", true, nil, false)
	key := "order-1"

	first, n, replay, err := testStore.CreateWithFanOut(ctx, tenantID, "order.created", []byte(`{"n":1}`), &key, "same-body")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if replay || n != 1 {
		t.Fatalf("first deliveries %d replay %v", n, replay)
	}

	again, n, replay, err := testStore.CreateWithFanOut(ctx, tenantID, "order.created", []byte(`{"n":1}`), &key, "same-body")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay || n != 0 || again.ID != first.ID {
		t.Fatalf("replay id %s deliveries %d replay %v, first %s", again.ID, n, replay, first.ID)
	}
	if countEvents(t, tenantID) != 1 || countDeliveries(t, first.ID) != 1 {
		t.Fatalf("events %d deliveries %d", countEvents(t, tenantID), countDeliveries(t, first.ID))
	}

	_, _, _, err = testStore.CreateWithFanOut(ctx, tenantID, "order.created", []byte(`{"n":2}`), &key, "other-body")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if countEvents(t, tenantID) != 1 || countDeliveries(t, first.ID) != 1 {
		t.Fatalf("conflict changed rows: events %d deliveries %d", countEvents(t, tenantID), countDeliveries(t, first.ID))
	}
}

func TestIdempotencyKeyIsPerTenant(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	a := insertTenant(t, "a")
	b := insertTenant(t, "b")
	endpointA := insertEndpoint(t, a, "https://example.com/a", true, nil, false)
	endpointB := insertEndpoint(t, b, "https://example.com/b", true, nil, false)
	key := "shared-key"

	eventA, _, _, err := testStore.CreateWithFanOut(ctx, a, "order.created", []byte(`{"n":1}`), &key, "body-a")
	if err != nil {
		t.Fatalf("A: %v", err)
	}
	eventB, _, _, err := testStore.CreateWithFanOut(ctx, b, "order.created", []byte(`{"n":1}`), &key, "body-a")
	if err != nil {
		t.Fatalf("B: %v", err)
	}
	if eventA.ID == eventB.ID {
		t.Fatal("tenants share an event id")
	}
	if ids := deliveryEndpoints(t, eventA.ID); !sameIDs(ids, []uuid.UUID{endpointA}) {
		t.Fatalf("A deliveries = %v", ids)
	}
	if ids := deliveryEndpoints(t, eventB.ID); !sameIDs(ids, []uuid.UUID{endpointB}) {
		t.Fatalf("B deliveries = %v", ids)
	}
}

func TestConcurrentIdempotencyCreatesOneEvent(t *testing.T) {
	truncate(t)
	tenantID := insertTenant(t, "owner")
	insertEndpoint(t, tenantID, "https://example.com/all", true, nil, false)
	key := "race-key"

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	ids := make(chan uuid.UUID, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			event, _, _, err := testStore.CreateWithFanOut(context.Background(), tenantID, "order.created", []byte(`{"n":1}`), &key, "same-body")
			errs <- err
			ids <- event.ID
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		if err != nil {
			t.Fatalf("CreateWithFanOut: %v", err)
		}
	}
	var first uuid.UUID
	for id := range ids {
		if first == uuid.Nil {
			first = id
		} else if id != first {
			t.Fatalf("ids %s and %s", first, id)
		}
	}
	if countEvents(t, tenantID) != 1 || countDeliveries(t, first) != 1 {
		t.Fatalf("events %d deliveries %d", countEvents(t, tenantID), countDeliveries(t, first))
	}
}

func TestFanOutFailureRollsBackTheEvent(t *testing.T) {
	truncate(t)
	tenantID := insertTenant(t, "owner")
	insertEndpoint(t, tenantID, "https://example.com/all", true, nil, false)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS fail_fanout_delivery ON deliveries`)
		_, _ = pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS fail_fanout_delivery()`)
	})
	if _, err := pool.Exec(context.Background(), `
		CREATE OR REPLACE FUNCTION fail_fanout_delivery() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'fan-out failed';
		END;
		$$
	`); err != nil {
		t.Fatalf("function: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		CREATE TRIGGER fail_fanout_delivery
		BEFORE INSERT ON deliveries
		FOR EACH ROW
		EXECUTE FUNCTION fail_fanout_delivery()
	`); err != nil {
		t.Fatalf("trigger: %v", err)
	}

	_, _, _, err := testStore.CreateWithFanOut(context.Background(), tenantID, "order.created", []byte(`{}`), nil, "fingerprint")
	if err == nil || errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want a delivery failure", err)
	}
	if countEvents(t, tenantID) != 0 {
		t.Fatalf("events = %d, want 0", countEvents(t, tenantID))
	}
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

func insertEndpoint(t *testing.T, tenantID uuid.UUID, rawURL string, active bool, types []string, deleted bool) uuid.UUID {
	t.Helper()
	if types == nil {
		types = []string{}
	}
	var deletedAt *time.Time
	if deleted {
		now := time.Now()
		deletedAt = &now
	}
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO endpoints (tenant_id, url, secret_ciphertext, is_active, event_types, deleted_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, tenantID, rawURL, []byte("cipher"), active, types, deletedAt).Scan(&id)
	if err != nil {
		t.Fatalf("insert endpoint: %v", err)
	}
	return id
}

func deliveryEndpoints(t *testing.T, eventID uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT endpoint_id FROM deliveries WHERE event_id = $1`, eventID)
	if err != nil {
		t.Fatalf("deliveries: %v", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return ids
}

func countEvents(t *testing.T, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM events WHERE tenant_id = $1`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

func countDeliveries(t *testing.T, eventID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM deliveries WHERE event_id = $1`, eventID).Scan(&n); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	return n
}

func sameIDs(got, want []uuid.UUID) bool {
	if len(got) != len(want) {
		return false
	}
	for _, id := range want {
		if !containsID(got, id) {
			return false
		}
	}
	return true
}

func containsID(ids []uuid.UUID, want uuid.UUID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
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
