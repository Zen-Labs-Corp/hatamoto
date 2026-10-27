package author

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

// Handlers is the admin API of authors (/api/v1/authors).
//
// Deprecated: authors are people now. Manage them as entities of the `person`
// type (Admin > People, or the entities API). This API only edits the rollback
// mirror: edits here do not change any byline on the site.
type Handlers struct {
	store  *Store
	logger *slog.Logger
	// OnChange runs after a write, for example to purge rendered pages.
	OnChange func(r *http.Request)
}

// NewHandlers returns the admin handlers over store.
func NewHandlers(store *Store, logger *slog.Logger) *Handlers {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handlers{store: store, logger: logger}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handlers) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "author not found"})
	case errors.Is(err, ErrSlugConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "author slug already exists"})
	case errors.Is(err, ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		h.logger.Error("authors: request failed", slog.String("error", err.Error()))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func (h *Handlers) changed(r *http.Request) {
	if h.OnChange != nil {
		h.OnChange(r)
	}
}

func decode(r *http.Request) (Input, error) {
	var in Input
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, errors.Join(ErrInvalid, err)
	}
	return in, nil
}

// List handles GET /authors.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.List(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// Get handles GET /authors/{id}.
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.fail(w, ErrNotFound)
		return
	}
	a, err := h.store.Get(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// Create handles POST /authors.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	in, err := decode(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	a, err := h.store.Create(r.Context(), in)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.changed(r)
	writeJSON(w, http.StatusCreated, a)
}

// Update handles PUT /authors/{id}.
func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.fail(w, ErrNotFound)
		return
	}
	in, err := decode(r)
	if err != nil {
		h.fail(w, err)
		return
	}
	a, err := h.store.Update(r.Context(), id, in)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.changed(r)
	writeJSON(w, http.StatusOK, a)
}

// Delete handles DELETE /authors/{id}.
func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.fail(w, ErrNotFound)
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		h.fail(w, err)
		return
	}
	h.changed(r)
	w.WriteHeader(http.StatusNoContent)
}
