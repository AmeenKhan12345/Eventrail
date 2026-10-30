package event

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type fakeStore struct {
	called      bool
	eventType   string
	payload     []byte
	key         *string
	fingerprint string
	event       Event
	created     int
	replay      bool
	err         error
}

func (f *fakeStore) CreateWithFanOut(_ context.Context, _ uuid.UUID, eventType string, payload []byte, key *string, fingerprint string) (Event, int, bool, error) {
	f.called = true
	f.eventType = eventType
	f.payload = payload
	f.key = key
	f.fingerprint = fingerprint
	if f.err != nil {
		return Event{}, 0, false, f.err
	}
	if f.event.ID == uuid.Nil {
		f.event.ID = uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
		f.event.Type = eventType
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
