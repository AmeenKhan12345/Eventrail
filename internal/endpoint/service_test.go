package endpoint

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/AmeenKhan12345/Eventrail/internal/secrets"
)

type fakeStore struct {
	createURL        string
	createDesc       string
	ciphertext       []byte
	createCalled     bool
	createErr        error
	rotateCalled     bool
	rotateCiphertext []byte
	rotateErr        error
	updateURL        *string
	updateDesc       *string
	updateActive     *bool
	listLimit        int
	listOffset       int
}

func (f *fakeStore) Create(_ context.Context, _ uuid.UUID, rawURL, description string, ciphertext []byte) (Endpoint, error) {
	f.createCalled = true
	f.createURL = rawURL
	f.createDesc = description
	f.ciphertext = ciphertext
	if f.createErr != nil {
		return Endpoint{}, f.createErr
	}
	return Endpoint{ID: uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"), URL: rawURL, Description: description, IsActive: true}, nil
}

func (f *fakeStore) Get(context.Context, uuid.UUID, uuid.UUID) (Endpoint, error) {
	return Endpoint{}, nil
}

func (f *fakeStore) List(_ context.Context, _ uuid.UUID, limit, offset int) ([]Endpoint, error) {
	f.listLimit = limit
	f.listOffset = offset
	return []Endpoint{}, nil
}

func (f *fakeStore) Update(_ context.Context, _, _ uuid.UUID, rawURL, description *string, isActive *bool) (Endpoint, error) {
	f.updateURL = rawURL
	f.updateDesc = description
	f.updateActive = isActive
	return Endpoint{URL: deref(rawURL)}, nil
}

func (f *fakeStore) SoftDelete(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (f *fakeStore) RotateSecret(_ context.Context, _, _ uuid.UUID, ciphertext []byte) (Endpoint, error) {
	f.rotateCalled = true
	f.rotateCiphertext = ciphertext
	if f.rotateErr != nil {
		return Endpoint{}, f.rotateErr
	}
	return Endpoint{ID: uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"), URL: "https://example.com/hook"}, nil
}

func TestCreateValidation(t *testing.T) {
	longURL := "https://example.com/" + strings.Repeat("a", 2048)
	tests := []struct {
		name          string
		url           string
		description   string
		allowInsecure bool
		wantMsg       string
	}{
		{name: "http rejected", url: "http://example.com/hook", wantMsg: "url scheme must be https"},
		{name: "http allowed", url: "http://example.com/hook", allowInsecure: true},
		{name: "https", url: "https://example.com/hook"},
		{name: "missing host", url: "https:///hook", wantMsg: "url host is required"},
		{name: "user info", url: "https://user:pass@example.com/hook", wantMsg: "url must not include user info"},
		{name: "too long", url: longURL, wantMsg: "url must be at most 2048 characters"},
		{name: "description too long", url: "https://example.com/hook", description: strings.Repeat("d", 501), wantMsg: "description must be at most 500 characters"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStore{}
			_, secret, err := NewService(fake, testBox(t), tt.allowInsecure).Create(context.Background(), uuid.New(), tt.url, tt.description)
			if tt.wantMsg != "" {
				if !errors.Is(err, ErrInvalidInput) || err.Error() != tt.wantMsg {
					t.Fatalf("err = %v, want %q", err, tt.wantMsg)
				}
				if fake.createCalled || secret != "" {
					t.Fatal("store was called or a secret was returned")
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if !fake.createCalled || fake.createURL != tt.url {
				t.Fatalf("stored url %q", fake.createURL)
			}
			plain, err := testBox(t).Decrypt(fake.ciphertext)
			if err != nil {
				t.Fatalf("ciphertext does not open to the returned secret: %v", err)
			}
			if plain != secret || !strings.HasPrefix(secret, "whsec_") {
				t.Fatalf("secret %q plaintext %q", secret, plain)
			}
		})
	}
}

func TestUpdateRequiresAField(t *testing.T) {
	_, err := NewService(&fakeStore{}, testBox(t), false).Update(context.Background(), uuid.New(), uuid.New(), nil, nil, nil)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestRotateSecretReturnsPlaintextOnce(t *testing.T) {
	fake := &fakeStore{}
	box := testBox(t)
	_, secret, err := NewService(fake, box, false).RotateSecret(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}
	plain, err := box.Decrypt(fake.rotateCiphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if plain != secret {
		t.Fatalf("stored plaintext %q, returned %q", plain, secret)
	}
}

func TestStoreErrorsPassThrough(t *testing.T) {
	boom := errors.New("database unavailable")
	tests := []struct {
		name string
		fake fakeStore
		call func(*Service) error
		want error
	}{
		{
			name: "create conflict",
			fake: fakeStore{createErr: ErrConflict},
			call: func(s *Service) error {
				_, _, err := s.Create(context.Background(), uuid.New(), "https://example.com/hook", "")
				return err
			},
			want: ErrConflict,
		},
		{
			name: "rotate not found",
			fake: fakeStore{rotateErr: ErrNotFound},
			call: func(s *Service) error {
				_, _, err := s.RotateSecret(context.Background(), uuid.New(), uuid.New())
				return err
			},
			want: ErrNotFound,
		},
		{
			name: "create internal",
			fake: fakeStore{createErr: boom},
			call: func(s *Service) error {
				_, _, err := s.Create(context.Background(), uuid.New(), "https://example.com/hook", "")
				return err
			},
			want: boom,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := tt.fake
			err := tt.call(NewService(&fake, testBox(t), false))
			if !errors.Is(err, tt.want) || err != tt.want {
				t.Fatalf("err = %v, want the store error unchanged", err)
			}
		})
	}
}

func TestListPage(t *testing.T) {
	fake := &fakeStore{}
	if _, err := NewService(fake, testBox(t), false).List(context.Background(), uuid.New(), 0, -3); err != nil {
		t.Fatalf("List: %v", err)
	}
	if fake.listLimit != 20 || fake.listOffset != 0 {
		t.Fatalf("limit %d offset %d", fake.listLimit, fake.listOffset)
	}
}

func testBox(t *testing.T) *secrets.Box {
	t.Helper()
	box, err := secrets.NewBox(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	return box
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
