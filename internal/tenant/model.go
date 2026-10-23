package tenant

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a tenant id does not match a row.
var ErrNotFound = errors.New("tenant not found")

// ErrInvalidInput marks a tenant field the service refused.
// Attach the text an API client should see with InvalidInput.
var ErrInvalidInput = errors.New("invalid input")

// ValidationError is a refused field. Message is safe to show to API clients.
// errors.Is(err, ErrInvalidInput) is true because Unwrap returns that sentinel.
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

// Tenant is one customer of the webhook service.
type Tenant struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}
