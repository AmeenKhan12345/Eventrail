package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmeenKhan12345/Eventrail/internal/auth"
	"github.com/AmeenKhan12345/Eventrail/internal/httpx"
	"github.com/AmeenKhan12345/Eventrail/internal/platform/postgres"
	"github.com/AmeenKhan12345/Eventrail/internal/secrets"
)

const operatorKey = "test-operator-key"

var (
	pool    *pgxpool.Pool
	handler http.Handler
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
	box, err := secrets.NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		log.Fatal(err)
	}
	handler = NewRouter(Deps{
		Pool:              pool,
		OperatorAPIKey:    operatorKey,
		Box:               box,
		AllowInsecureURLs: false,
	})

	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func TestOperatorCreatesTwoTenants(t *testing.T) {
	truncate(t)

	a := createTenant(t, "A")
	b := createTenant(t, "B")
	if a.ID == b.ID {
		t.Fatal("tenants share an id")
	}
	if a.APIKey == "" || b.APIKey == "" || a.APIKey == b.APIKey {
		t.Fatalf("api keys = %q and %q", a.APIKey, b.APIKey)
	}
	if _, err := auth.Parse(a.APIKey); err != nil {
		t.Fatalf("parse A: %v", err)
	}
	if _, err := auth.Parse(b.APIKey); err != nil {
		t.Fatalf("parse B: %v", err)
	}
}

func TestTenantIsolation(t *testing.T) {
	truncate(t)

	a := createTenant(t, "A")
	b := createTenant(t, "B")
	created := createEndpoint(t, a.APIKey, "https://example.com/a", "owned by A")

	denied := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/v1/endpoints/" + created.ID, ""},
		{http.MethodPatch, "/v1/endpoints/" + created.ID, `{"description":"stolen"}`},
		{http.MethodDelete, "/v1/endpoints/" + created.ID, ""},
		{http.MethodPost, "/v1/endpoints/" + created.ID + "/rotate-secret", ""},
	}
	for _, tt := range denied {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := do(t, tt.method, tt.path, b.APIKey, tt.body)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
		})
	}

	list := do(t, http.MethodGet, "/v1/endpoints", b.APIKey, "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", list.Code, list.Body.String())
	}
	if ids := decode[listed](t, list).ids(); contains(ids, created.ID) {
		t.Fatalf("B's list = %v, contains A's endpoint", ids)
	}

	got := do(t, http.MethodGet, "/v1/endpoints/"+created.ID, a.APIKey, "")
	if got.Code != http.StatusOK {
		t.Fatalf("A get status = %d, body = %s", got.Code, got.Body.String())
	}
	patched := do(t, http.MethodPatch, "/v1/endpoints/"+created.ID, a.APIKey, `{"description":"still A's"}`)
	if patched.Code != http.StatusOK {
		t.Fatalf("A patch status = %d, body = %s", patched.Code, patched.Body.String())
	}
	if decode[endpointBody](t, patched).Description != "still A's" {
		t.Fatalf("description = %s", patched.Body.String())
	}
}

func TestAuthBoundaries(t *testing.T) {
	truncate(t)

	tenant := createTenant(t, "A")
	unknown, _, _, err := auth.Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	revoked := createTenant(t, "revoked")
	prefix, err := auth.Parse(revoked.APIKey)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE api_keys SET revoked_at = now() WHERE key_prefix = $1`, prefix); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	tests := []struct {
		name  string
		token string
		path  string
	}{
		{name: "no key", path: "/v1/endpoints"},
		{name: "malformed key", token: "nope", path: "/v1/endpoints"},
		{name: "unknown key", token: unknown, path: "/v1/endpoints"},
		{name: "operator key on endpoints", token: operatorKey, path: "/v1/endpoints"},
		{name: "tenant key on tenants", token: tenant.APIKey, path: "/v1/tenants"},
		{name: "revoked key", token: revoked.APIKey, path: "/v1/endpoints"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, http.MethodGet, tt.path, tt.token, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			body := decode[httpx.ErrorBody](t, rec)
			if body.Error.Code != "unauthorized" {
				t.Fatalf("code = %q, body = %s", body.Error.Code, rec.Body.String())
			}
		})
	}
}

func TestSecretExposure(t *testing.T) {
	truncate(t)

	tenant := createTenant(t, "A")
	created := createEndpoint(t, tenant.APIKey, "https://example.com/hook", "orders")
	if created.Secret == "" || !strings.HasPrefix(created.Secret, "whsec_") {
		t.Fatalf("create secret = %q", created.Secret)
	}

	got := do(t, http.MethodGet, "/v1/endpoints/"+created.ID, tenant.APIKey, "")
	if got.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", got.Code, got.Body.String())
	}
	assertNoSecret(t, got.Body.String(), created.Secret)

	list := do(t, http.MethodGet, "/v1/endpoints", tenant.APIKey, "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", list.Code, list.Body.String())
	}
	assertNoSecret(t, list.Body.String(), created.Secret)

	stored := ciphertext(t, created.ID)
	if bytes.Contains(stored, []byte(created.Secret)) {
		t.Fatal("secret_ciphertext contains the plaintext secret")
	}

	var prefix, hash string
	err := pool.QueryRow(context.Background(), `SELECT key_prefix, key_hash FROM api_keys WHERE tenant_id = $1`, tenant.ID).Scan(&prefix, &hash)
	if err != nil {
		t.Fatalf("api key row: %v", err)
	}
	if strings.Contains(prefix, tenant.APIKey) || strings.Contains(hash, tenant.APIKey) {
		t.Fatal("api_keys holds the plaintext key")
	}
}

func TestRotation(t *testing.T) {
	truncate(t)

	tenant := createTenant(t, "A")
	created := createEndpoint(t, tenant.APIKey, "https://example.com/hook", "orders")
	before := ciphertext(t, created.ID)

	rec := do(t, http.MethodPost, "/v1/endpoints/"+created.ID+"/rotate-secret", tenant.APIKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rotated := decode[endpointBody](t, rec)
	if rotated.Secret == "" || rotated.Secret == created.Secret {
		t.Fatalf("rotated secret = %q, create secret = %q", rotated.Secret, created.Secret)
	}
	after := ciphertext(t, created.ID)
	if bytes.Equal(before, after) {
		t.Fatal("secret_ciphertext did not change")
	}
	if bytes.Contains(after, []byte(rotated.Secret)) {
		t.Fatal("rotated ciphertext contains the plaintext secret")
	}
}

func TestSoftDeleteAndConflict(t *testing.T) {
	truncate(t)

	tenant := createTenant(t, "A")
	const rawURL = "https://example.com/hook"
	created := createEndpoint(t, tenant.APIKey, rawURL, "first")

	deleted := do(t, http.MethodDelete, "/v1/endpoints/"+created.ID, tenant.APIKey, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
	if deleted.Body.Len() != 0 {
		t.Fatalf("delete body = %s", deleted.Body.String())
	}

	got := do(t, http.MethodGet, "/v1/endpoints/"+created.ID, tenant.APIKey, "")
	if got.Code != http.StatusNotFound {
		t.Fatalf("get status = %d, body = %s", got.Code, got.Body.String())
	}
	list := do(t, http.MethodGet, "/v1/endpoints", tenant.APIKey, "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", list.Code, list.Body.String())
	}
	if ids := decode[listed](t, list).ids(); contains(ids, created.ID) {
		t.Fatalf("list = %v, still contains deleted endpoint", ids)
	}

	again := createEndpoint(t, tenant.APIKey, rawURL, "second")
	if again.ID == created.ID {
		t.Fatal("recreated endpoint reused the deleted id")
	}

	dup := do(t, http.MethodPost, "/v1/endpoints", tenant.APIKey, `{"url":"`+rawURL+`","description":"third"}`)
	if dup.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, body = %s", dup.Code, dup.Body.String())
	}
}

func TestValidation(t *testing.T) {
	truncate(t)

	tenant := createTenant(t, "A")
	created := createEndpoint(t, tenant.APIKey, "https://example.com/hook", "ok")

	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		message string
	}{
		{
			name:    "http url",
			method:  http.MethodPost,
			path:    "/v1/endpoints",
			body:    `{"url":"http://example.com/hook"}`,
			message: "url scheme must be https",
		},
		{
			name:    "embedded credentials",
			method:  http.MethodPost,
			path:    "/v1/endpoints",
			body:    `{"url":"https://user:pass@example.com/hook"}`,
			message: "url must not include user info",
		},
		{
			name:    "empty patch",
			method:  http.MethodPatch,
			path:    "/v1/endpoints/" + created.ID,
			body:    `{}`,
			message: "at least one field is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, tt.method, tt.path, tenant.APIKey, tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			body := decode[httpx.ErrorBody](t, rec)
			if body.Error.Message != tt.message {
				t.Fatalf("message = %q, want %q", body.Error.Message, tt.message)
			}
		})
	}
}

type createdTenant struct {
	ID     string `json:"id"`
	APIKey string `json:"api_key"`
}

type endpointBody struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Secret      string `json:"secret"`
}

type listed struct {
	Data []endpointBody `json:"data"`
}

func (l listed) ids() []string {
	ids := make([]string, len(l.Data))
	for i, endpoint := range l.Data {
		ids[i] = endpoint.ID
	}
	return ids
}

func createTenant(t *testing.T, name string) createdTenant {
	t.Helper()
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := do(t, http.MethodPost, "/v1/tenants", operatorKey, string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create tenant %s: %d %s", name, rec.Code, rec.Body.String())
	}
	created := decode[createdTenant](t, rec)
	if created.ID == "" || created.APIKey == "" {
		t.Fatalf("tenant response = %s", rec.Body.String())
	}
	return created
}

func createEndpoint(t *testing.T, apiKey, rawURL, description string) endpointBody {
	t.Helper()
	body, err := json.Marshal(map[string]string{"url": rawURL, "description": description})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := do(t, http.MethodPost, "/v1/endpoints", apiKey, string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create endpoint: %d %s", rec.Code, rec.Body.String())
	}
	created := decode[endpointBody](t, rec)
	if created.ID == "" {
		t.Fatalf("endpoint response = %s", rec.Body.String())
	}
	return created
}

func do(t *testing.T, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	return out
}

func assertNoSecret(t *testing.T, body, plaintext string) {
	t.Helper()
	if strings.Contains(body, "secret") || strings.Contains(body, plaintext) {
		t.Fatalf("response exposed a secret: %s", body)
	}
}

func ciphertext(t *testing.T, id string) []byte {
	t.Helper()
	var stored []byte
	err := pool.QueryRow(context.Background(), `SELECT secret_ciphertext FROM endpoints WHERE id = $1`, uuid.MustParse(id)).Scan(&stored)
	if err != nil {
		t.Fatalf("ciphertext: %v", err)
	}
	return stored
}

func contains(ids []string, want string) bool {
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
