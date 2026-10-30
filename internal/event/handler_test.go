package event

import (
	"bytes"
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
	"github.com/AmeenKhan12345/Eventrail/internal/httpx"
)

func TestIngestHTTP(t *testing.T) {
	tenantID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	eventID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	createdAt := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	internalErr := errors.New("secret-db-failure")

	tests := []struct {
		name       string
		body       string
		key        string
		store      fakeStore
		wantStatus int
		wantText   string
		rejectText string
	}{
		{
			name: "create",
			body: `{"type":"order.created","payload":{"n":1}}`,
			store: fakeStore{
				created: 2,
				event:   Event{ID: eventID, Type: "order.created", CreatedAt: createdAt},
			},
			wantStatus: http.StatusCreated,
			wantText:   `"deliveries_created":2`,
		},
		{
			name: "replay",
			body: `{"type":"order.created","payload":{}}`,
			key:  "same",
			store: fakeStore{
				replay: true,
				event:  Event{ID: eventID, Type: "order.created", CreatedAt: createdAt},
			},
			wantStatus: http.StatusOK,
			wantText:   `"deliveries_created":0`,
		},
		{
			name:       "invalid type",
			body:       `{"type":"Order","payload":{}}`,
			wantStatus: http.StatusBadRequest,
			wantText:   "event type is invalid",
		},
		{
			name:       "array payload",
			body:       `{"type":"order.created","payload":[]}`,
			wantStatus: http.StatusBadRequest,
			wantText:   "payload must be a json object",
		},
		{
			name:       "conflict",
			body:       `{"type":"order.created","payload":{}}`,
			key:        "same",
			store:      fakeStore{err: ErrConflict},
			wantStatus: http.StatusConflict,
			wantText:   ErrConflict.Error(),
		},
		{
			name:       "internal",
			body:       `{"type":"order.created","payload":{}}`,
			store:      fakeStore{err: internalErr},
			wantStatus: http.StatusInternalServerError,
			wantText:   "internal error",
			rejectText: internalErr.Error(),
		},
		{
			name:       "invalid json",
			body:       `{`,
			wantStatus: http.StatusBadRequest,
			wantText:   "invalid json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := tt.store
			rec := request(t, tenantID, NewService(&store), http.MethodPost, "/v1/events", tt.body, tt.key)
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

func TestBodyTooLarge(t *testing.T) {
	tenantID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	body := bytes.Repeat([]byte("a"), maxBodyBytes+1)
	rec := request(t, tenantID, NewService(&fakeStore{}), http.MethodPost, "/v1/events", string(body), "")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var parsed httpx.ErrorBody
	if err := decodeError(rec, &parsed); err != nil {
		t.Fatalf("json: %v", err)
	}
	if parsed.Error.Message != "body too large" {
		t.Fatalf("message = %q", parsed.Error.Message)
	}
}

func TestMissingTenantContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/events", strings.NewReader(`{"type":"order.created","payload":{}}`))
	rec := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Mount("/v1/events", NewHandler(NewService(&fakeStore{})).Routes())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
}

func decodeError(rec *httptest.ResponseRecorder, dest *httpx.ErrorBody) error {
	return json.NewDecoder(rec.Body).Decode(dest)
}

type fakeLookup struct {
	key auth.Key
}

func (f *fakeLookup) GetByPrefix(context.Context, string) (auth.Key, error) {
	return f.key, nil
}

func request(t *testing.T, tenantID uuid.UUID, svc service, method, path, body, idempotencyKey string) *httptest.ResponseRecorder {
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
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	rec := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(auth.Tenant(&fakeLookup{key: auth.Key{TenantID: tenantID, Hash: hash}}))
		r.Mount("/v1/events", NewHandler(svc).Routes())
	})
	router.ServeHTTP(rec, req)
	return rec
}
