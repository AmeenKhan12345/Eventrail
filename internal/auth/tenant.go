package auth

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"net/http"

	"github.com/google/uuid"

	"github.com/AmeenKhan12345/webhooks/internal/httpx"
)

// lookup is the API-key read the middleware needs. *Store satisfies it.
type lookup interface {
	GetByPrefix(ctx context.Context, prefix string) (Key, error)
}

type tenantIDKey struct{}

// TenantID returns the tenant id stored by the Tenant middleware.
func TenantID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(tenantIDKey{}).(uuid.UUID)
	return id, ok
}

// Tenant authenticates a tenant API key. Every failure writes the same 401.
// The key is never logged, and last_used_at is not updated.
func Tenant(keys lookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			plaintext := bearerToken(r.Header.Get("Authorization"))
			prefix, err := Parse(plaintext)
			if err != nil {
				unauthorized(w)
				return
			}
			record, err := keys.GetByPrefix(r.Context(), prefix)
			if err != nil || record.Revoked || !sameHash(record.Hash, plaintext) {
				unauthorized(w)
				return
			}
			ctx := context.WithValue(r.Context(), tenantIDKey{}, record.TenantID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func unauthorized(w http.ResponseWriter) {
	httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
}

func sameHash(storedHex, plaintext string) bool {
	stored, err := hex.DecodeString(storedHex)
	got, err2 := hex.DecodeString(Hash(plaintext))
	if err != nil || err2 != nil || len(stored) == 0 || len(got) != len(stored) {
		return false
	}
	return subtle.ConstantTimeCompare(got, stored) == 1
}
