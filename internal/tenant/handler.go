package tenant

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/AmeenKhan12345/webhooks/internal/httpx"
)

const maxBodyBytes = 1 << 20

const apiKeyWarning = "This key will not be shown again."

// service is what the handler needs. *Service satisfies it.
type service interface {
	Create(ctx context.Context, name string) (Tenant, string, error)
	Get(ctx context.Context, id uuid.UUID) (Tenant, error)
	List(ctx context.Context, limit, offset int) ([]Tenant, error)
}

// Handler serves tenant HTTP routes.
type Handler struct {
	svc service
}

// NewHandler returns a handler that calls the given service.
func NewHandler(svc service) *Handler {
	return &Handler{svc: svc}
}

// Routes returns the tenant routes. Mount them behind operator auth.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/", h.create)
	r.Get("/", h.list)
	r.Get("/{id}", h.get)
	return r
}

type tenantResponse struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type listResponse struct {
	Data []tenantResponse `json:"data"`
}

type createRequest struct {
	Name string `json:"name"`
}

type createResponse struct {
	tenantResponse
	APIKey  string `json:"api_key"`
	Warning string `json:"warning"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := decodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "invalid json")
		return
	}

	created, apiKey, err := h.svc.Create(r.Context(), req.Name)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, createResponse{
		tenantResponse: toResponse(created),
		APIKey:         apiKey,
		Warning:        apiKeyWarning,
	})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "invalid limit")
		return
	}
	offset, err := queryInt(r, "offset")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "invalid offset")
		return
	}

	tenants, err := h.svc.List(r.Context(), limit, offset)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}

	data := make([]tenantResponse, 0, len(tenants))
	for _, tenant := range tenants {
		data = append(data, toResponse(tenant))
	}
	httpx.WriteJSON(w, http.StatusOK, listResponse{Data: data})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "invalid id")
		return
	}

	tenant, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toResponse(tenant))
}

// writeServiceError is the only place service errors become HTTP responses.
// ErrInvalidInput and ErrNotFound keep a client-facing message. Every other
// error is logged and answered with a generic 500 so internal text stays in the log.
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
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "tenant not found")
		return
	}
	slog.Error("tenant request", "err", err, "method", r.Method, "path", r.URL.Path)
	httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
}

func toResponse(tenant Tenant) tenantResponse {
	return tenantResponse{
		ID:        tenant.ID,
		Name:      tenant.Name,
		CreatedAt: tenant.CreatedAt,
		UpdatedAt: tenant.UpdatedAt,
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dest)
}

func queryInt(r *http.Request, name string) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	return strconv.Atoi(raw)
}
