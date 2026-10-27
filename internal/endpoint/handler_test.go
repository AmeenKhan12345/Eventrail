package endpoint

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

	"github.com/AmeenKhan12345/Eventrail/internal/auth"
)

type fakeService struct {
	createErr error
	getErr    error
	updateErr error
	deleteErr error
	rotateErr error
	listed    []Endpoint
	one       Endpoint
	secret    string
}

func (f *fakeService) Create(_ context.Context, tenantID uuid.UUID, rawURL, description string) (Endpoint, string, error) {
	if f.createErr != nil {
		return Endpoint{}, "", f.createErr
	}
	return Endpoint{
		ID:          uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
		TenantID:    tenantID,
		URL:         rawURL,
		Description: description,
		IsActive:    true,
		CreatedAt:   time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		UpdatedAt:   time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
	}, "whsec_once", nil
}

func (f *fakeService) Get(_ context.Context, tenantID, id uuid.UUID) (Endpoint, error) {
	if f.getErr != nil {
		return Endpoint{}, f.getErr
	}
	if f.one.ID == uuid.Nil {
		f.one.ID = id
		f.one.TenantID = tenantID
	}
	return f.one, nil
}

func (f *fakeService) List(context.Context, uuid.UUID, int, int) ([]Endpoint, error) {
	return f.listed, nil
}

func (f *fakeService) Update(_ context.Context, _, _ uuid.UUID, _, _ *string, _ *bool) (Endpoint, error) {
	if f.updateErr != nil {
		return Endpoint{}, f.updateErr
	}
	return f.one, nil
}

func (f *fakeService) SoftDelete(context.Context, uuid.UUID, uuid.UUID) error {
	return f.deleteErr
}

func (f *fakeService) RotateSecret(_ context.Context, tenantID, id uuid.UUID) (Endpoint, string, error) {
	if f.rotateErr != nil {
		return Endpoint{}, "", f.rotateErr
	}
	return Endpoint{ID: id, TenantID: tenantID, URL: "https://example.com/hook", IsActive: true}, f.secret, nil
}

func TestEndpointHTTP(t *testing.T) {
	tenantID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	endpointID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	internalErr := errors.New("secret-db-failure")

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		svc        fakeService
		wantStatus int
		wantText   string
		rejectText string
	}{
		{
			name:       "create",
			method:     http.MethodPost,
			path:       "/v1/endpoints",
			body:       `{"url":"https://example.com/hook","description":"prod"}`,
			wantStatus: http.StatusCreated,
			wantText:   `"secret":"whsec_once"`,
		},
		{
			name:       "bad url",
			method:     http.MethodPost,
			path:       "/v1/endpoints",
			body:       `{"url":"http://example.com/hook"}`,
			svc:        fakeService{createErr: InvalidInput("url scheme must be https")},
			wantStatus: http.StatusBadRequest,
			wantText:   "url scheme must be https",
		},
		{
			name:       "bad uuid",
			method:     http.MethodGet,
			path:       "/v1/endpoints/not-a-uuid",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing",
			method:     http.MethodGet,
			path:       "/v1/endpoints/" + endpointID.String(),
			svc:        fakeService{getErr: ErrNotFound},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "conflict",
			method:     http.MethodPost,
			path:       "/v1/endpoints",
			body:       `{"url":"https://example.com/hook"}`,
			svc:        fakeService{createErr: ErrConflict},
			wantStatus: http.StatusConflict,
		},
		{
			name:       "internal",
			method:     http.MethodPost,
			path:       "/v1/endpoints",
			body:       `{"url":"https://example.com/hook"}`,
			svc:        fakeService{createErr: internalErr},
			wantStatus: http.StatusInternalServerError,
			wantText:   "internal error",
			rejectText: internalErr.Error(),
		},
		{
			name:       "delete",
			method:     http.MethodDelete,
			path:       "/v1/endpoints/" + endpointID.String(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "rotate",
			method:     http.MethodPost,
			path:       "/v1/endpoints/" + endpointID.String() + "/rotate-secret",
			svc:        fakeService{secret: "whsec_rotated"},
			wantStatus: http.StatusOK,
			wantText:   `"secret":"whsec_rotated"`,
		},
		{
			name:       "update requires a field",
			method:     http.MethodPatch,
			path:       "/v1/endpoints/" + endpointID.String(),
			body:       `{}`,
			svc:        fakeService{updateErr: InvalidInput("at least one field is required")},
			wantStatus: http.StatusBadRequest,
			wantText:   "at least one field is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := request(t, tenantID, &tt.svc, tt.method, tt.path, tt.body)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantText != "" && !strings.Contains(rec.Body.String(), tt.wantText) {
				t.Fatalf("body %s does not contain %q", rec.Body.String(), tt.wantText)
			}
			if tt.rejectText != "" && strings.Contains(rec.Body.String(), tt.rejectText) {
				t.Fatalf("body leaked internal error %q: %s", tt.rejectText, rec.Body.String())
			}
			if tt.wantStatus == http.StatusCreated && !strings.Contains(rec.Body.String(), secretWarning) {
				t.Fatalf("body missing warning: %s", rec.Body.String())
			}
		})
	}
}

func TestGetAndListOmitSecret(t *testing.T) {
	tenantID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	one := Endpoint{
		ID:       uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
		TenantID: tenantID,
		URL:      "https://example.com/hook",
		IsActive: true,
	}
	svc := &fakeService{one: one, listed: []Endpoint{one}}
	getRec := request(t, tenantID, svc, http.MethodGet, "/v1/endpoints/"+one.ID.String(), "")
	listRec := request(t, tenantID, svc, http.MethodGet, "/v1/endpoints", "")
	for _, rec := range []*httptest.ResponseRecorder{getRec, listRec} {
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "whsec_") {
			t.Fatalf("response included a secret: %s", rec.Body.String())
		}
	}

	var listed struct {
		Data []endpointResponse `json:"data"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("json: %v", err)
	}
	if listed.Data == nil {
		t.Fatal("data is null")
	}

	empty := request(t, tenantID, &fakeService{}, http.MethodGet, "/v1/endpoints", "")
	var noRows struct {
		Data []endpointResponse `json:"data"`
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &noRows); err != nil {
		t.Fatalf("json: %v", err)
	}
	if noRows.Data == nil {
		t.Fatalf("empty list is null: %s", empty.Body.String())
	}
}

func TestMissingTenantContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/endpoints", nil)
	rec := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Mount("/v1/endpoints", NewHandler(&fakeService{}).Routes())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
}

type fakeLookup struct {
	key auth.Key
}

func (f *fakeLookup) GetByPrefix(context.Context, string) (auth.Key, error) {
	return f.key, nil
}

func request(t *testing.T, tenantID uuid.UUID, svc service, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	plaintext, _, hash, err := auth.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(auth.Tenant(&fakeLookup{key: auth.Key{TenantID: tenantID, Hash: hash}}))
		r.Mount("/v1/endpoints", NewHandler(svc).Routes())
	})
	router.ServeHTTP(rec, req)
	return rec
}
