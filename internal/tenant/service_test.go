package tenant

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type fakeStore struct {
	createName   string
	createCalled bool
	createErr    error

	getErr error

	listLimit  int
	listOffset int
	listErr    error
}

func (f *fakeStore) Create(_ context.Context, name string) (Tenant, error) {
	f.createCalled = true
	f.createName = name
	if f.createErr != nil {
		return Tenant{}, f.createErr
	}
	return Tenant{ID: uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"), Name: name}, nil
}

func (f *fakeStore) GetByID(context.Context, uuid.UUID) (Tenant, error) {
	if f.getErr != nil {
		return Tenant{}, f.getErr
	}
	return Tenant{}, nil
}

func (f *fakeStore) List(_ context.Context, limit, offset int) ([]Tenant, error) {
	f.listLimit = limit
	f.listOffset = offset
	if f.listErr != nil {
		return nil, f.listErr
	}
	return []Tenant{}, nil
}

func TestCreate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantMsg  string
	}{
		{name: "whitespace only", input: " \t\n ", wantMsg: "name is required"},
		{name: "101 characters", input: strings.Repeat("a", 101), wantMsg: "name must be at most 100 characters"},
		{name: "trimmed", input: "  Acme  ", wantName: "Acme"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStore{}
			got, err := NewService(fake).Create(context.Background(), tt.input)

			if tt.wantMsg != "" {
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("err = %v, want ErrInvalidInput", err)
				}
				if err.Error() != tt.wantMsg {
					t.Fatalf("message = %q, want %q", err.Error(), tt.wantMsg)
				}
				if fake.createCalled {
					t.Fatal("store.Create was called")
				}
				if got != (Tenant{}) {
					t.Fatalf("got %+v, want zero Tenant", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if fake.createName != tt.wantName {
				t.Fatalf("store name = %q, want %q", fake.createName, tt.wantName)
			}
			if got.Name != tt.wantName {
				t.Fatalf("name = %q, want %q", got.Name, tt.wantName)
			}
		})
	}
}

func TestListPage(t *testing.T) {
	tests := []struct {
		name       string
		limit      int
		offset     int
		wantLimit  int
		wantOffset int
	}{
		{name: "default limit", limit: 0, offset: 0, wantLimit: 20, wantOffset: 0},
		{name: "negative limit", limit: -1, offset: 4, wantLimit: 20, wantOffset: 4},
		{name: "cap limit", limit: 101, offset: 2, wantLimit: 100, wantOffset: 2},
		{name: "negative offset", limit: 10, offset: -5, wantLimit: 10, wantOffset: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeStore{}
			svc := NewService(fake)

			if _, err := svc.List(context.Background(), tt.limit, tt.offset); err != nil {
				t.Fatalf("List: %v", err)
			}
			if fake.listLimit != tt.wantLimit || fake.listOffset != tt.wantOffset {
				t.Fatalf("store got limit=%d offset=%d, want limit=%d offset=%d", fake.listLimit, fake.listOffset, tt.wantLimit, tt.wantOffset)
			}
		})
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
			name: "create",
			fake: fakeStore{createErr: boom},
			call: func(s *Service) error {
				_, err := s.Create(context.Background(), "Acme")
				return err
			},
			want: boom,
		},
		{
			name: "get not found",
			fake: fakeStore{getErr: ErrNotFound},
			call: func(s *Service) error {
				_, err := s.Get(context.Background(), uuid.New())
				return err
			},
			want: ErrNotFound,
		},
		{
			name: "list",
			fake: fakeStore{listErr: boom},
			call: func(s *Service) error {
				_, err := s.List(context.Background(), 20, 0)
				return err
			},
			want: boom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := tt.fake
			err := tt.call(NewService(&fake))
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if err != tt.want {
				t.Fatalf("err = %v, want the store error unchanged", err)
			}
		})
	}
}
