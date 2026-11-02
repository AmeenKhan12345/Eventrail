package event

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeStore struct {
	called      bool
	id          uuid.UUID
	createdAt   time.Time
	eventType   string
	payload     []byte
	webhookBody []byte
	key         *string
	fingerprint string
	event       Event
	created     int
	replay      bool
	err         error
}

func (f *fakeStore) CreateWithFanOut(_ context.Context, _ uuid.UUID, id uuid.UUID, createdAt time.Time, eventType string, payload, webhookBody []byte, key *string, fingerprint string) (Event, int, bool, error) {
	f.called = true
	f.id = id
	f.createdAt = createdAt
	f.eventType = eventType
	f.payload = payload
	f.webhookBody = webhookBody
	f.key = key
	f.fingerprint = fingerprint
	if f.err != nil {
		return Event{}, 0, false, f.err
	}
	if f.event.ID == uuid.Nil {
		f.event.ID = id
		f.event.Type = eventType
		f.event.CreatedAt = createdAt
		f.event.WebhookBody = string(webhookBody)
	}
	return f.event, f.created, f.replay, nil
}

func TestIngestValidation(t *testing.T) {
	longKey := strings.Repeat("k", 256)
	tests := []struct {
		name    string
		typ     string
		payload string
		key     string
		wantMsg string
	}{
		{name: "valid", typ: "order.created", payload: `{"n":1}`, key: "abc"},
		{name: "uppercase type", typ: "Order", payload: `{}`, wantMsg: "event type is invalid"},
		{name: "empty type", typ: "", payload: `{}`, wantMsg: "event type is invalid"},
		{name: "array payload", typ: "order.created", payload: `[]`, wantMsg: "payload must be a json object"},
		{name: "null payload", typ: "order.created", payload: `null`, wantMsg: "payload must be a json object"},
		{name: "missing payload", typ: "order.created", wantMsg: "payload must be a json object"},
		{name: "long key", typ: "order.created", payload: `{}`, key: longKey, wantMsg: "idempotency key must be at most 255 characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStore{}
			_, _, _, err := NewService(fake).Ingest(context.Background(), uuid.New(), tt.typ, []byte(tt.payload), tt.key, []byte("raw"))
			if tt.wantMsg != "" {
				if !errors.Is(err, ErrInvalidInput) || err.Error() != tt.wantMsg {
					t.Fatalf("err = %v, want %q", err, tt.wantMsg)
				}
				if fake.called {
					t.Fatal("store was called")
				}
				return
			}
			if err != nil {
				t.Fatalf("Ingest: %v", err)
			}
			if fake.eventType != tt.typ || string(fake.payload) != tt.payload {
				t.Fatalf("stored type %q payload %s", fake.eventType, fake.payload)
			}
			if fake.key == nil || *fake.key != tt.key {
				t.Fatalf("key = %v", fake.key)
			}
		})
	}
}

func TestIngestBuildsWebhookBody(t *testing.T) {
	fake := &fakeStore{}
	payload := []byte(`{"n":1}`)
	_, _, _, err := NewService(fake).Ingest(context.Background(), uuid.New(), "order.created", payload, "", []byte(`{"type":"order.created","payload":{"n":1}}`))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if fake.id == uuid.Nil || fake.createdAt.Location() != time.UTC {
		t.Fatalf("id %s created_at %s", fake.id, fake.createdAt)
	}
	var body struct {
		ID        uuid.UUID       `json:"id"`
		Type      string          `json:"type"`
		CreatedAt time.Time       `json:"created_at"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(fake.webhookBody, &body); err != nil {
		t.Fatalf("unmarshal: %v body %s", err, fake.webhookBody)
	}
	if body.ID != fake.id || body.Type != "order.created" || string(body.Data) != string(payload) || !body.CreatedAt.Equal(fake.createdAt) {
		t.Fatalf("body = %+v", body)
	}
}

func TestIngestFingerprintAndEmptyKey(t *testing.T) {
	raw := []byte(`{"type":"order.created","payload":{}}`)
	fake := &fakeStore{}
	if _, _, _, err := NewService(fake).Ingest(context.Background(), uuid.New(), "order.created", []byte(`{}`), "", raw); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	sum := sha256.Sum256(raw)
	if fake.fingerprint != hex.EncodeToString(sum[:]) {
		t.Fatalf("fingerprint = %s", fake.fingerprint)
	}
	if fake.key != nil {
		t.Fatal("empty idempotency key was stored")
	}
}

func TestIngestStoreErrorsPassThrough(t *testing.T) {
	boom := errors.New("database unavailable")
	tests := []struct {
		name string
		err  error
	}{
		{name: "conflict", err: ErrConflict},
		{name: "internal", err: boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStore{err: tt.err}
			_, _, _, err := NewService(fake).Ingest(context.Background(), uuid.New(), "order.created", []byte(`{}`), "", []byte(`{}`))
			if err != tt.err || !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want the store error unchanged", err)
			}
		})
	}
}
