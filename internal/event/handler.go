package event

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmeenKhan12345/Eventrail/internal/auth"
	"github.com/AmeenKhan12345/Eventrail/internal/httpx"
)

const maxBodyBytes = 256 << 10

// service is what the handler needs. *Service satisfies it.
type service interface {
	Ingest(ctx context.Context, tenantID uuid.UUID, eventType string, payload []byte, idempotencyKey string, rawBody []byte) (Event, int, bool, error)
}

// Handler serves event routes. The tenant id comes from the request context.
type Handler struct {
	svc service
}

// NewHandler returns a handler that calls the given service.
func NewHandler(svc service) *Handler {
	return &Handler{svc: svc}
}

// Routes returns the event routes. Mount them behind auth.Tenant.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/", h.create)
	return r
}

type ingestRequest struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type ingestResponse struct {
	ID                uuid.UUID `json:"id"`
	Type              string    `json:"type"`
	CreatedAt         time.Time `json:"created_at"`
	DeliveriesCreated int       `json:"deliveries_created"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantID(w, r)
	if !ok {
		return
	}
	body, err := readBody(w, r)
	if err != nil {
		writeReadError(w, err)
		return
	}
	var req ingestRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields() //json.Unmarshal cannot reject unknown fields, so we use a decoder to do it.
	if err := dec.Decode(&req); err != nil || dec.More() {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "invalid json")
		return
	}
	created, n, replay, err := h.svc.Ingest(r.Context(), tenantID, req.Type, req.Payload, r.Header.Get("Idempotency-Key"), body)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, ingestResponse{
		ID:                created.ID,
		Type:              created.Type,
		CreatedAt:         created.CreatedAt,
		DeliveriesCreated: n,
	})
}

func tenantID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := auth.TenantID(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return uuid.UUID{}, false
	}
	return id, true
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	return io.ReadAll(r.Body)
}

func writeReadError(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "invalid_input", "body too large")
		return
	}
	httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "invalid json")
}

// writeServiceError is the only place service errors become HTTP responses.
func (h *Handler) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrInvalidInput) {
		message := err.Error()
		var invalid *ValidationError
		if errors.As(err, &invalid) {
			message = invalid.Message
		}
		httpx.WriteError(w, http.StatusBadRequest, "invalid_input", message)
		return
	}
	if errors.Is(err, ErrConflict) {
		httpx.WriteError(w, http.StatusConflict, "conflict", ErrConflict.Error())
		return
	}
	slog.Error("event request", "err", err, "method", r.Method, "path", r.URL.Path)
	httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
}
