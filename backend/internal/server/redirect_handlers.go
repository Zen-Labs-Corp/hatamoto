package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/redirect"
)

// ── 404 Log Handlers ─────────────────────────────────────────────────────────

func (s *Server) handleList404Entries(w http.ResponseWriter, r *http.Request) {
	params := redirect.NotFoundListParams{
		Search: r.URL.Query().Get("search"),
	}
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil {
		params.Page = p
	}
	if pp, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil {
		params.PerPage = pp
	}

	entries, total, err := s.redirectService.List404Entries(r.Context(), params)
	if err != nil {
		s.logger.Error("failed to list 404 entries", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list 404 entries")
		return
	}
	if entries == nil {
		entries = []*redirect.NotFoundEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
		"total":   total,
	})
}

func (s *Server) handleIgnore404Entry(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ID")
		return
	}

	if err := s.redirectService.Ignore404Entry(r.Context(), id); err != nil {
		if errors.Is(err, redirect.ErrNotFoundEntryNotFound) {
			writeError(w, http.StatusNotFound, "entry not found")
			return
		}
		s.logger.Error("failed to ignore 404 entry", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to ignore entry")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDelete404Entry(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ID")
		return
	}

	if err := s.redirectService.Delete404Entry(r.Context(), id); err != nil {
		if errors.Is(err, redirect.ErrNotFoundEntryNotFound) {
			writeError(w, http.StatusNotFound, "entry not found")
			return
		}
		s.logger.Error("failed to delete 404 entry", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete entry")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ── Redirect Handlers ────────────────────────────────────────────────────────

func (s *Server) handleListRedirects(w http.ResponseWriter, r *http.Request) {
	params := redirect.RedirectListParams{
		Search: r.URL.Query().Get("search"),
	}
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil {
		params.Page = p
	}
	if pp, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil {
		params.PerPage = pp
	}

	redirects, total, err := s.redirectService.ListRedirects(r.Context(), params)
	if err != nil {
		s.logger.Error("failed to list redirects", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list redirects")
		return
	}
	if redirects == nil {
		redirects = []*redirect.Redirect{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"redirects": redirects,
		"total":     total,
	})
}

func (s *Server) handleCreateRedirect(w http.ResponseWriter, r *http.Request) {

	var input redirect.CreateRedirectInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if input.SourcePath == "" || input.TargetURL == "" {
		writeError(w, http.StatusBadRequest, "source_path and target_url are required")
		return
	}
	if input.StatusCode == 0 {
		input.StatusCode = 301
	}

	rd, err := s.redirectService.CreateRedirect(r.Context(), &input)
	if err != nil {
		if errors.Is(err, redirect.ErrInvalidStatusCode) {
			writeError(w, http.StatusBadRequest, "invalid status code — must be 301, 302, 307, or 308")
			return
		}
		if errors.Is(err, redirect.ErrInvalidPattern) || errors.Is(err, redirect.ErrUnsafePattern) || errors.Is(err, redirect.ErrExternalTarget) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, redirect.ErrDuplicateSourcePath) {
			writeError(w, http.StatusConflict, "a redirect with this source path already exists")
			return
		}
		s.logger.Error("failed to create redirect", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create redirect")
		return
	}
	writeJSON(w, http.StatusCreated, rd)
}

func (s *Server) handleUpdateRedirect(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ID")
		return
	}

	var input redirect.UpdateRedirectInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	rd, err := s.redirectService.UpdateRedirect(r.Context(), id, &input)
	if err != nil {
		if errors.Is(err, redirect.ErrRedirectNotFound) {
			writeError(w, http.StatusNotFound, "redirect not found")
			return
		}
		if errors.Is(err, redirect.ErrInvalidStatusCode) {
			writeError(w, http.StatusBadRequest, "invalid status code")
			return
		}
		if errors.Is(err, redirect.ErrInvalidPattern) || errors.Is(err, redirect.ErrUnsafePattern) || errors.Is(err, redirect.ErrExternalTarget) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, redirect.ErrDuplicateSourcePath) {
			writeError(w, http.StatusConflict, "a redirect with this source path already exists")
			return
		}
		s.logger.Error("failed to update redirect", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update redirect")
		return
	}
	writeJSON(w, http.StatusOK, rd)
}

func (s *Server) handleDeleteRedirect(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ID")
		return
	}

	if err := s.redirectService.DeleteRedirect(r.Context(), id); err != nil {
		if errors.Is(err, redirect.ErrRedirectNotFound) {
			writeError(w, http.StatusNotFound, "redirect not found")
			return
		}
		s.logger.Error("failed to delete redirect", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete redirect")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
