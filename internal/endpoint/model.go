package endpoint

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when this tenant has no live endpoint with that id.
var ErrNotFound = errors.New("endpoint not found")

// ErrConflict is returned when the tenant already has a live endpoint with that URL.
var ErrConflict = errors.New("endpoint already exists")

// ErrInvalidInput marks a field the service refused.
// Attach the text an API client should see with InvalidInput.
var ErrInvalidInput = errors.New("invalid input")

// ValidationError is a refused field. Message is safe to show to API clients.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

func (e *ValidationError) Unwrap() error {
	return ErrInvalidInput
}

// InvalidInput builds a ValidationError with a client-facing message.
func InvalidInput(message string) error {
	return &ValidationError{Message: message}
}

// Endpoint is one webhook destination. It never carries the signing secret.
type Endpoint struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	URL         string
	Description string
	IsActive    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
