package event

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when this tenant has no event with that id.
var ErrNotFound = errors.New("event not found")

// ErrConflict is returned when an idempotency key is reused with a different body.
var ErrConflict = errors.New("idempotency key already used with a different body")

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

// Event is one accepted webhook event. Payload is the raw JSON object.
type Event struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	Type           string
	Payload        []byte
	IdempotencyKey *string
	Fingerprint    string
	CreatedAt      time.Time
}
