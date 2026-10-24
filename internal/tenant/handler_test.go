package tenant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmeenKhan12345/webhooks/internal/auth"
)

const operatorKey = "test-operator-key"

type fakeService struct {
	createErr error
	getErr    error
	listErr   error
	listed    []Tenant
}

func (f *fakeService) Create(_ context.Context, name string) (Tenant, error) {
	if strings.TrimSpace(name) == "" {
		return Tenant{}, InvalidInput("name is required")
	}
	if f.createErr != nil {
		return Tenant{}, f.createErr
	}
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	return Tenant{
		ID:        uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (f *fakeService) Get(context.Context, uuid.UUID) (Tenant, error) {
	if f.getErr != nil {
		return Tenant{}, f.getErr
	}
	return Tenant{}, nil
}

func (f *fakeService) List(context.Context, int, int) ([]Tenant, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listed, nil
}

func TestTenantHTTP(t *testing.T) {
	internalErr := errors.New("secret-db-failure")

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		auth       string
		svc        fakeService
		wantStatus int
		wantText   string
		rejectText string
	}{
		{
			name:       "missing auth",
			method:     http.MethodPost,
			path:       "/v1/tenants",
			body:       `{"name":"Acme"}`,
			auth:       "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong key",
			method:     http.MethodPost,
			path:       "/v1/tenants",
			body:       `{"name":"Acme"}`,
			auth:       "Bearer wrong-key",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty name",
			method:     http.MethodPost,
			path:       "/v1/tenants",
			body:       `{"name":""}`,
			auth:       "Bearer " + operatorKey,
			wantStatus: http.StatusBadRequest,
			wantText:   "name is required",
		},
		{
			name:       "whitespace name",
			method:     http.MethodPost,
			path:       "/v1/tenants",
			body:       `{"name":"   "}`,
			auth:       "Bearer " + operatorKey,
			wantStatus: http.StatusBadRequest,
			wantText:   "name is required",
		},
		{
			name:       "malformed json",
			method:     http.MethodPost,
			path:       "/v1/tenants",
			body:       `{`,
			auth:       "Bearer " + operatorKey,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown field",
			method:     http.MethodPost,
			path:       "/v1/tenants",
			body:       `{"name":"Acme","extra":true}`,
			auth:       "Bearer " + operatorKey,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "bad uuid",
			method:     http.MethodGet,
			path:       "/v1/tenants/not-a-uuid",
			auth:       "Bearer " + operatorKey,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown tenant",
			method:     http.MethodGet,
			path:       "/v1/tenants/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
			auth:       "Bearer " + operatorKey,
			svc:        fakeService{getErr: ErrNotFound},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "internal error",
			method:     http.MethodPost,
			path:       "/v1/tenants",
			body:       `{"name":"Acme"}`,
			auth:       "Bearer " + operatorKey,
			svc:        fakeService{createErr: internalErr},
			wantStatus: http.StatusInternalServerError,
			wantText:   "internal error",
			rejectText: internalErr.Error(),
		},
		{
			name:       "non-numeric limit",
			method:     http.MethodGet,
			path:       "/v1/tenants?limit=abc",
			auth:       "Bearer " + operatorKey,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := request(t, &tt.svc, tt.method, tt.path, tt.body, tt.auth)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantText != "" && !strings.Contains(rec.Body.String(), tt.wantText) {
				t.Fatalf("body %s does not contain %q", rec.Body.String(), tt.wantText)
			}
			if tt.rejectText != "" && strings.Contains(rec.Body.String(), tt.rejectText) {
				t.Fatalf("body leaked internal error %q: %s", tt.rejectText, rec.Body.String())
			}
		})
	}
}

func TestCreateOK(t *testing.T) {
	rec := request(t, &fakeService{}, http.MethodPost, "/v1/tenants", `{"name":"Acme"}`, "Bearer "+operatorKey)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	var got tenantResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got.Name != "Acme" || got.ID == uuid.Nil {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(rec.Body.String(), `"created_at"`) || !strings.Contains(rec.Body.String(), `"updated_at"`) {
		t.Fatalf("body missing snake_case times: %s", rec.Body.String())
	}
}

func TestListEmptyIsArray(t *testing.T) {
	rec := request(t, &fakeService{}, http.MethodGet, "/v1/tenants", "", "Bearer "+operatorKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Data []tenantResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got.Data == nil {
		t.Fatalf("data is null, body %s", rec.Body.String())
	}
	if len(got.Data) != 0 {
		t.Fatalf("len = %d, want 0", len(got.Data))
	}
}

func request(t *testing.T, svc service, method, path, body, authHeader string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	testRouter(svc).ServeHTTP(rec, req)
	return rec
}

func testRouter(svc service) http.Handler {
	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(auth.Operator(operatorKey))
		r.Mount("/v1/tenants", NewHandler(svc).Routes())
	})
	return router
}
