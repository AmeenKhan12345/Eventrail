package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AmeenKhan12345/Eventrail/internal/auth"
	"github.com/AmeenKhan12345/Eventrail/internal/endpoint"
	"github.com/AmeenKhan12345/Eventrail/internal/httpx"
	"github.com/AmeenKhan12345/Eventrail/internal/secrets"
	"github.com/AmeenKhan12345/Eventrail/internal/tenant"
)

// Deps are the runtime values the routes need.
type Deps struct {
	Pool              *pgxpool.Pool
	OperatorAPIKey    string
	Box               *secrets.Box
	AllowInsecureURLs bool
}

// NewRouter builds the API routes used by the server and the end-to-end tests.
func NewRouter(deps Deps) http.Handler {
	tenants := tenant.NewHandler(tenant.NewService(tenant.NewStore(deps.Pool)))
	endpoints := endpoint.NewHandler(endpoint.NewService(endpoint.NewStore(deps.Pool), deps.Box, deps.AllowInsecureURLs))

	router := chi.NewRouter()
	router.Get("/healthz", healthz(deps.Pool))
	router.Group(func(r chi.Router) {
		r.Use(auth.Operator(deps.OperatorAPIKey))
		r.Mount("/v1/tenants", tenants.Routes())
	})
	router.Group(func(r chi.Router) {
		r.Use(auth.Tenant(auth.NewStore(deps.Pool)))
		r.Mount("/v1/endpoints", endpoints.Routes())
	})
	return router
}

func healthz(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			slog.Error("healthz ping", "err", err)
			httpx.WriteError(w, http.StatusServiceUnavailable, "unavailable", "database unreachable")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
