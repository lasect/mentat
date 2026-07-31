package orgs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	apiauth "mentat/internal/api/auth"
	apimiddleware "mentat/internal/api/middleware"
	coreorgs "mentat/internal/orgs"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *coreorgs.Service
}

func NewHandler(service *coreorgs.Service) (*Handler, error) {
	if service == nil {
		return nil, fmt.Errorf("organization service is required")
	}
	return &Handler{service: service}, nil
}

func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.NotFound(apimiddleware.NotFound)
	r.MethodNotAllowed(apimiddleware.MethodNotAllowed)
	r.Get("/", h.listOrganizations)
	r.Post("/", h.createOrganization)
	r.Route("/{orgSlug}", func(r chi.Router) {
		r.Get("/", h.getOrganization)
		r.Patch("/analytics-store", h.updateAnalyticsStore)
		r.Get("/databases", h.listDatabases)
		r.Post("/databases", h.createDatabase)
		r.Put("/databases/{dbSlug}/extensions", h.setDatabaseExtensions)
		r.Delete("/databases/{dbSlug}", h.deleteDatabase)
	})
	return r
}

func (h *Handler) createOrganization(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	userID := apiauth.PrincipalFromContext(r.Context()).User.ID
	organization, err := h.service.CreateOrganization(r.Context(), userID, input.Name, input.Slug)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"organization": organization})
}

func (h *Handler) listOrganizations(w http.ResponseWriter, r *http.Request) {
	userID := apiauth.PrincipalFromContext(r.Context()).User.ID
	organizations, err := h.service.ListOrganizations(r.Context(), userID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"organizations": organizations})
}

func (h *Handler) getOrganization(w http.ResponseWriter, r *http.Request) {
	userID := apiauth.PrincipalFromContext(r.Context()).User.ID
	organization, err := h.service.GetOrganization(r.Context(), userID, chi.URLParam(r, "orgSlug"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"organization": organization})
}

func (h *Handler) updateAnalyticsStore(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AnalyticsStore string `json:"analytics_store"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	userID := apiauth.PrincipalFromContext(r.Context()).User.ID
	organization, err := h.service.UpdateAnalyticsStore(
		r.Context(), userID, chi.URLParam(r, "orgSlug"), input.AnalyticsStore,
	)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"organization": organization})
}

func (h *Handler) createDatabase(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name             string         `json:"name"`
		Slug             string         `json:"slug"`
		ConnectionString string         `json:"connection_string"`
		Extensions       map[string]int `json:"extensions"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	userID := apiauth.PrincipalFromContext(r.Context()).User.ID
	database, err := h.service.CreateDatabase(
		r.Context(), userID, chi.URLParam(r, "orgSlug"), input.Name,
		input.Slug, input.ConnectionString, input.Extensions,
	)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"database": database})
}

func (h *Handler) listDatabases(w http.ResponseWriter, r *http.Request) {
	userID := apiauth.PrincipalFromContext(r.Context()).User.ID
	databases, err := h.service.ListDatabases(r.Context(), userID, chi.URLParam(r, "orgSlug"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"databases": databases})
}

func (h *Handler) setDatabaseExtensions(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Extensions map[string]int `json:"extensions"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	userID := apiauth.PrincipalFromContext(r.Context()).User.ID
	database, err := h.service.SetDatabaseExtensions(
		r.Context(), userID, chi.URLParam(r, "orgSlug"),
		chi.URLParam(r, "dbSlug"), input.Extensions,
	)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"database": database})
}

func (h *Handler) deleteDatabase(w http.ResponseWriter, r *http.Request) {
	userID := apiauth.PrincipalFromContext(r.Context()).User.ID
	err := h.service.DeleteDatabase(
		r.Context(), userID, chi.URLParam(r, "orgSlug"), chi.URLParam(r, "dbSlug"),
	)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain one JSON value")
		return false
	}
	return true
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, coreorgs.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "organization resource was not found")
	case errors.Is(err, coreorgs.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "organization permission is required")
	case errors.Is(err, coreorgs.ErrSlugTaken):
		writeError(w, http.StatusConflict, "slug_taken", "this slug is already in use")
	case errors.Is(err, coreorgs.ErrPaidPlanRequired):
		writeError(w, http.StatusPaymentRequired, "paid_plan_required", "a Pro or Ultra plan is required")
	case errors.Is(err, coreorgs.ErrInvalidStore):
		writeError(w, http.StatusUnprocessableEntity, "invalid_analytics_store", "analytics store must be duckdb or clickhouse")
	case errors.Is(err, coreorgs.ErrInvalidExtension):
		writeError(w, http.StatusUnprocessableEntity, "invalid_extension", "one or more extensions are unsupported")
	case errors.Is(err, coreorgs.ErrInvalidConnection):
		writeError(w, http.StatusUnprocessableEntity, "invalid_connection_string", "a valid PostgreSQL connection string is required")
	case errors.Is(err, coreorgs.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
