package endpoint

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

	"github.com/AmeenKhan12345/Eventrail/internal/auth"
	"github.com/AmeenKhan12345/Eventrail/internal/httpx"
)

const maxBodyBytes = 1 << 20

const secretWarning = "This secret will not be shown again."

// service is what the handler needs. *Service satisfies it.
type service interface {
	Create(ctx context.Context, tenantID uuid.UUID, url, description string, eventTypes []string) (Endpoint, string, error)
	Get(ctx context.Context, tenantID, id uuid.UUID) (Endpoint, error)
	List(ctx context.Context, tenantID uuid.UUID, limit, offset int) ([]Endpoint, error)
	Update(ctx context.Context, tenantID, id uuid.UUID, url, description *string, isActive *bool, eventTypes *[]string) (Endpoint, error)
	SoftDelete(ctx context.Context, tenantID, id uuid.UUID) error
	RotateSecret(ctx context.Context, tenantID, id uuid.UUID) (Endpoint, string, error)
}

// Handler serves endpoint routes. The tenant id comes from the request context.
type Handler struct {
	svc service
}

// NewHandler returns a handler that calls the given service.
func NewHandler(svc service) *Handler {
	return &Handler{svc: svc}
}

// Routes returns the endpoint routes. Mount them behind auth.Tenant.
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/", h.create)
	r.Get("/", h.list)
	r.Get("/{id}", h.get)
	r.Patch("/{id}", h.update)
	r.Delete("/{id}", h.delete)
	r.Post("/{id}/rotate-secret", h.rotate)
	return r
}

type endpointResponse struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenant_id"`
	URL         string    `json:"url"`
	Description string    `json:"description"`
	IsActive    bool      `json:"is_active"`
	EventTypes  []string  `json:"event_types"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type listResponse struct {
	Data []endpointResponse `json:"data"`
}

type createRequest struct {
	URL         string   `json:"url"`
	Description string   `json:"description"`
	EventTypes  []string `json:"event_types"`
}

type updateRequest struct {
	URL         *string   `json:"url"`
	Description *string   `json:"description"`
	IsActive    *bool     `json:"is_active"`
	EventTypes  *[]string `json:"event_types"`
}

type secretResponse struct {
	endpointResponse
	Secret  string `json:"secret"`
	Warning string `json:"warning"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantID(w, r)
	if !ok {
		return
	}
	var req createRequest
	if err := decodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "invalid json")
		return
	}
	created, secret, err := h.svc.Create(r.Context(), tenantID, req.URL, req.Description, req.EventTypes)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, secretResponse{
		endpointResponse: toResponse(created),
		Secret:           secret,
		Warning:          secretWarning,
	})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantID(w, r)
	if !ok {
		return
	}
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
	endpoints, err := h.svc.List(r.Context(), tenantID, limit, offset)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	data := make([]endpointResponse, 0, len(endpoints))
	for _, endpoint := range endpoints {
		data = append(data, toResponse(endpoint))
	}
	httpx.WriteJSON(w, http.StatusOK, listResponse{Data: data})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantID(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	endpoint, err := h.svc.Get(r.Context(), tenantID, id)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toResponse(endpoint))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantID(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req updateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "invalid json")
		return
	}
	updated, err := h.svc.Update(r.Context(), tenantID, id, req.URL, req.Description, req.IsActive, req.EventTypes)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toResponse(updated))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantID(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.svc.SoftDelete(r.Context(), tenantID, id); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) rotate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantID(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rotated, secret, err := h.svc.RotateSecret(r.Context(), tenantID, id)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, secretResponse{
		endpointResponse: toResponse(rotated),
		Secret:           secret,
		Warning:          secretWarning,
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

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_input", "invalid id")
		return uuid.UUID{}, false
	}
	return id, true
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
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "endpoint not found")
		return
	}
	if errors.Is(err, ErrConflict) {
		httpx.WriteError(w, http.StatusConflict, "conflict", ErrConflict.Error())
		return
	}
	slog.Error("endpoint request", "err", err, "method", r.Method, "path", r.URL.Path)
	httpx.WriteError(w, http.StatusInternalServerError, "internal", "internal error")
}

func toResponse(endpoint Endpoint) endpointResponse {
	return endpointResponse{
		ID:          endpoint.ID,
		TenantID:    endpoint.TenantID,
		URL:         endpoint.URL,
		Description: endpoint.Description,
		IsActive:    endpoint.IsActive,
		EventTypes:  eventTypesOrEmpty(endpoint.EventTypes),
		CreatedAt:   endpoint.CreatedAt,
		UpdatedAt:   endpoint.UpdatedAt,
	}
}

func eventTypesOrEmpty(types []string) []string {
	if types == nil {
		return []string{}
	}
	return types
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
