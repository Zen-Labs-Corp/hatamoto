package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/entity"
)

// parseDryRun reads ?dry_run: 1/true is on, 0/false off (case-insensitive),
// absent off. Anything else, an empty value included, is an error, so a typo
// never turns into a real write.
func parseDryRun(r *http.Request) (bool, error) {
	q := r.URL.Query()
	if !q.Has("dry_run") {
		return false, nil
	}
	switch strings.ToLower(q.Get("dry_run")) {
	case "1", "true":
		return true, nil
	case "0", "false":
		return false, nil
	}
	return false, errors.New("dry_run must be 1, true, 0 or false")
}

// writeDryRun answers what the update would store, storing nothing. Errors map
// exactly as the real write maps them.
func (s *Server) writeDryRun(w http.ResponseWriter, r *http.Request, id uuid.UUID, userID *uuid.UUID, in *entity.UpdateEntityInput) {
	p, err := s.entityService.DryRunUpdate(r.Context(), id, userID, in)
	if err != nil {
		s.writeEntityUpdateError(w, err)
		return
	}
	setETag(w, p.Version)
	writeJSON(w, http.StatusOK, p)
}
