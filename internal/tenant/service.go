package tenant

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/AmeenKhan12345/Eventrail/internal/auth"
)

const (
	maxNameLen       = 100
	defaultListLimit = 20
	maxListLimit     = 100
)

// store is the data access the service needs. *Store satisfies it.
type store interface {
	CreateWithKey(ctx context.Context, name, keyPrefix, keyHash string) (Tenant, error)
	GetByID(ctx context.Context, id uuid.UUID) (Tenant, error)
	List(ctx context.Context, limit, offset int) ([]Tenant, error)
}

// Service applies tenant rules and calls a store.
type Service struct {
	store store
}

// NewService returns a service that uses the given store.
func NewService(s store) *Service {
	return &Service{store: s}
}

// Create trims the name, rejects an empty or overlong name, generates an API
// key, and stores the tenant and the key hash together.
// The plaintext key is returned once.
func (s *Service) Create(ctx context.Context, name string) (Tenant, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Tenant{}, "", InvalidInput("name is required")
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return Tenant{}, "", InvalidInput("name must be at most 100 characters")
	}

	plaintext, prefix, hash, err := auth.Generate()
	if err != nil {
		return Tenant{}, "", err
	}
	tenant, err := s.store.CreateWithKey(ctx, name, prefix, hash)
	if err != nil {
		return Tenant{}, "", err
	}
	return tenant, plaintext, nil
}

// Get returns the tenant with this id.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Tenant, error) {
	return s.store.GetByID(ctx, id)
}

// List returns a page of tenants. A limit below 1 becomes 20, a limit above
// 100 becomes 100, and a negative offset becomes 0.
func (s *Service) List(ctx context.Context, limit, offset int) ([]Tenant, error) {
	if limit < 1 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	if offset < 0 {
		offset = 0
	}
	return s.store.List(ctx, limit, offset)
}
