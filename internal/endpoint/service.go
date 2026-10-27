package endpoint

import (
	"context"
	"net/url"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/AmeenKhan12345/Eventrail/internal/secrets"
)

const (
	maxURLLen         = 2048
	maxDescriptionLen = 500
	defaultListLimit  = 20
	maxListLimit      = 100
)

// store is the data access the service needs. *Store satisfies it.
type store interface {
	Create(ctx context.Context, tenantID uuid.UUID, url, description string, secretCiphertext []byte) (Endpoint, error)
	Get(ctx context.Context, tenantID, id uuid.UUID) (Endpoint, error)
	List(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]Endpoint, error)
	Update(ctx context.Context, tenantID, id uuid.UUID, url, description *string, isActive *bool) (Endpoint, error)
	SoftDelete(ctx context.Context, tenantID, id uuid.UUID) error
	RotateSecret(ctx context.Context, tenantID, id uuid.UUID, secretCiphertext []byte) (Endpoint, error)
}

// Service applies endpoint rules, then calls a store.
type Service struct {
	store             store
	box               *secrets.Box
	allowInsecureURLs bool
}

// NewService returns a service that encrypts secrets with box.
func NewService(s store, box *secrets.Box, allowInsecureURLs bool) *Service {
	return &Service{store: s, box: box, allowInsecureURLs: allowInsecureURLs}
}

// Create checks the URL and description, generates a signing secret, and stores
// only the ciphertext. The plaintext secret is returned once.
func (s *Service) Create(ctx context.Context, tenantID uuid.UUID, rawURL, description string) (Endpoint, string, error) {
	if err := validateURL(rawURL, s.allowInsecureURLs); err != nil {
		return Endpoint{}, "", err
	}
	if err := validateDescription(description); err != nil {
		return Endpoint{}, "", err
	}
	plaintext, ciphertext, err := s.newSecret()
	if err != nil {
		return Endpoint{}, "", err
	}
	endpoint, err := s.store.Create(ctx, tenantID, rawURL, description, ciphertext)
	if err != nil {
		return Endpoint{}, "", err
	}
	return endpoint, plaintext, nil
}

// Get returns one live endpoint for the tenant.
func (s *Service) Get(ctx context.Context, tenantID, id uuid.UUID) (Endpoint, error) {
	return s.store.Get(ctx, tenantID, id)
}

// List returns a page of the tenant's live endpoints, newest first.
// A limit below 1 becomes 20, a limit above 100 becomes 100, and a negative
// offset becomes 0.
func (s *Service) List(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]Endpoint, error) {
	if limit < 1 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	if offset < 0 {
		offset = 0
	}
	return s.store.List(ctx, tenantID, limit, offset)
}

// Update validates the provided fields and patches the tenant's endpoint.
func (s *Service) Update(ctx context.Context, tenantID, id uuid.UUID, rawURL, description *string, isActive *bool) (Endpoint, error) {
	if rawURL == nil && description == nil && isActive == nil {
		return Endpoint{}, InvalidInput("at least one field is required")
	}
	if rawURL != nil {
		if err := validateURL(*rawURL, s.allowInsecureURLs); err != nil {
			return Endpoint{}, err
		}
	}
	if description != nil {
		if err := validateDescription(*description); err != nil {
			return Endpoint{}, err
		}
	}
	return s.store.Update(ctx, tenantID, id, rawURL, description, isActive)
}

// SoftDelete hides the tenant's endpoint.
func (s *Service) SoftDelete(ctx context.Context, tenantID, id uuid.UUID) error {
	return s.store.SoftDelete(ctx, tenantID, id)
}

// RotateSecret generates a new signing secret and stores its ciphertext.
// The plaintext is returned once.
func (s *Service) RotateSecret(ctx context.Context, tenantID, id uuid.UUID) (Endpoint, string, error) {
	plaintext, ciphertext, err := s.newSecret()
	if err != nil {
		return Endpoint{}, "", err
	}
	endpoint, err := s.store.RotateSecret(ctx, tenantID, id, ciphertext)
	if err != nil {
		return Endpoint{}, "", err
	}
	return endpoint, plaintext, nil
}

func (s *Service) newSecret() (string, []byte, error) {
	plaintext, err := s.box.GenerateSecret()
	if err != nil {
		return "", nil, err
	}
	ciphertext, err := s.box.Encrypt(plaintext)
	if err != nil {
		return "", nil, err
	}
	return plaintext, ciphertext, nil
}

func validateURL(raw string, allowInsecure bool) error {
	if len(raw) > maxURLLen {
		return InvalidInput("url must be at most 2048 characters")
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return InvalidInput("url is invalid")
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !allowInsecure {
			return InvalidInput("url scheme must be https")
		}
	default:
		return InvalidInput("url scheme must be https")
	}
	if parsed.Host == "" {
		return InvalidInput("url host is required")
	}
	if parsed.User != nil {
		return InvalidInput("url must not include user info")
	}
	return nil
}

func validateDescription(description string) error {
	if utf8.RuneCountInString(description) > maxDescriptionLen {
		return InvalidInput("description must be at most 500 characters")
	}
	return nil
}
