package event

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxIdempotencyKeyLen = 255

// eventTypePattern matches the endpoint package rule for one event type name.
var eventTypePattern = regexp.MustCompile(`^[a-z0-9_.-]{1,100}$`)

// store is the data access the service needs. *Store satisfies it.
type store interface {
	CreateWithFanOut(ctx context.Context, tenantID uuid.UUID, eventType string, payload []byte, idempotencyKey *string, fingerprint string) (Event, int, bool, error)
}

// Service checks an ingested event, then stores it and its deliveries.
type Service struct {
	store store
}

// NewService returns a service that calls the given store.
func NewService(s store) *Service {
	return &Service{store: s}
}

// Ingest validates the event and stores it. rawBody is the exact request bytes
// and is hashed into the idempotency fingerprint. An empty idempotency key
// means the caller did not send one. The bool is true when the key replayed
// an existing event.
func (s *Service) Ingest(ctx context.Context, tenantID uuid.UUID, eventType string, payload []byte, idempotencyKey string, rawBody []byte) (Event, int, bool, error) {
	if !eventTypePattern.MatchString(eventType) {
		return Event{}, 0, false, InvalidInput("event type is invalid")
	}
	if !isJSONObject(payload) {
		return Event{}, 0, false, InvalidInput("payload must be a json object")
	}
	if utf8.RuneCountInString(idempotencyKey) > maxIdempotencyKeyLen {
		return Event{}, 0, false, InvalidInput("idempotency key must be at most 255 characters")
	}
	var key *string
	if idempotencyKey != "" {
		key = &idempotencyKey
	}
	return s.store.CreateWithFanOut(ctx, tenantID, eventType, payload, key, fingerprint(rawBody))
}

func isJSONObject(payload []byte) bool {
	trimmed := bytes.TrimSpace(payload)
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed)
}

func fingerprint(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
