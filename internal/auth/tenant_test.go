package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

type fakeLookup struct {
	key   Key
	err   error
	calls int
}

func (f *fakeLookup) GetByPrefix(context.Context, string) (Key, error) {
	f.calls++
	if f.err != nil {
		return Key{}, f.err
	}
	return f.key, nil
}

func TestTenantMiddlewareFailuresAreIdentical(t *testing.T) {
	valid, _, hash, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	other, _, _, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	tenantID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")

	tests := []struct {
		name      string
		header    string
		lookup    fakeLookup
		wantCalls int
	}{
		{name: "missing header", lookup: fakeLookup{}, wantCalls: 0},
		{name: "malformed key", header: "Bearer whk_live_bad", lookup: fakeLookup{}, wantCalls: 0},
		{name: "unknown prefix", header: "Bearer " + valid, lookup: fakeLookup{err: ErrNotFound}, wantCalls: 1},
		{name: "revoked key", header: "Bearer " + valid, lookup: fakeLookup{key: Key{TenantID: tenantID, Hash: hash, Revoked: true}}, wantCalls: 1},
		{name: "wrong secret", header: "Bearer " + valid, lookup: fakeLookup{key: Key{TenantID: tenantID, Hash: Hash(other)}}, wantCalls: 1},
	}

	baseline := callTenant(t, &fakeLookup{}, "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookup := tt.lookup
			rec := callTenant(t, &lookup, tt.header)
			if lookup.calls != tt.wantCalls {
				t.Fatalf("lookup calls = %d, want %d", lookup.calls, tt.wantCalls)
			}
			if rec.Code != baseline.Code || rec.Body.String() != baseline.Body.String() || rec.Header().Get("Content-Type") != baseline.Header().Get("Content-Type") {
				t.Fatalf("got status %d body %s, want status %d body %s", rec.Code, rec.Body.String(), baseline.Code, baseline.Body.String())
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d", rec.Code)
			}
		})
	}
}

func TestTenantMiddlewareSetsTenantID(t *testing.T) {
	valid, _, hash, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	tenantID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	lookup := &fakeLookup{key: Key{TenantID: tenantID, Hash: hash}}

	var got uuid.UUID
	var ok bool
	handler := Tenant(lookup)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = TenantID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+valid)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if !ok || got != tenantID {
		t.Fatalf("tenant id = %s, ok = %v", got, ok)
	}
}

func callTenant(t *testing.T, lookup *fakeLookup, header string) *httptest.ResponseRecorder {
	t.Helper()
	handler := Tenant(lookup)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler ran")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
