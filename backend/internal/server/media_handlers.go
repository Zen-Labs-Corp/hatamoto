package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/middleware"
)

// handleMediaUpload handles POST /api/v1/media/upload
// Accepts multipart/form-data with a file field.
func (s *Server) handleMediaUpload(w http.ResponseWriter, r *http.Request) {
	claims, _ := middleware.GetClaims(r.Context())

	// Parse multipart form with max size limit. MaxBytesReader caps the RAW
	// request body up front so a huge or non-multipart body can't be read into
	// memory before ParseMultipartForm's own limit applies.
	maxSize := int64(s.mediaService.Config().MaxSizeMB) * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)
	if err := r.ParseMultipartForm(maxSize); err != nil {
		writeError(w, http.StatusBadRequest, "file too large or invalid form data")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	// Upload file
	m, err := s.mediaService.Upload(
		r.Context(),
		&claims.UserID,
		file,
		header.Filename,
		"",
		header.Size,
	)

	if err != nil {
		var svgErr *media.SVGError
		if errors.As(err, &svgErr) {
			writeError(w, http.StatusBadRequest, "the SVG was refused: "+svgErr.Error())
			return
		}
		if errors.Is(err, media.ErrInvalidMimeType) {
			writeError(w, http.StatusBadRequest, "unsupported file type, or the file extension does not match its content")
			return
		}
		if errors.Is(err, media.ErrFileTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "file too large")
			return
		}
		if errors.Is(err, media.ErrQuotaExceeded) {
			writeError(w, http.StatusInsufficientStorage, "the demo's storage is full; it resets every hour")
			return
		}
		if errors.Is(err, media.ErrImageTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "image dimensions too large")
			return
		}
		s.logger.Error("failed to upload media", "error", err)
		if errors.Is(err, media.ErrStorageFailed) && s.mediaService.ObjectStorage() {
			writeError(w, http.StatusBadGateway, "media storage is unavailable")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to upload file")
		return
	}

	writeJSON(w, http.StatusCreated, m)
}

// handleMediaList handles GET /api/v1/media
func (s *Server) handleMediaList(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	filter := &media.ListMediaFilter{
		Folder:   r.URL.Query().Get("folder"),
		MimeType: r.URL.Query().Get("mime_type"),
		Search:   r.URL.Query().Get("search"),
	}

	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	offset := 0
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	result, err := s.mediaService.List(r.Context(), filter, limit, offset)
	if err != nil {
		s.logger.Error("failed to list media", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list media")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"media":  result.Items,
		"total":  result.Total,
		"limit":  limit,
		"offset": offset,
	})
}

// handleMediaGet handles GET /api/v1/media/{id}
func (s *Server) handleMediaGet(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid media ID")
		return
	}

	m, err := s.mediaService.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrMediaNotFound) {
			writeError(w, http.StatusNotFound, "media not found")
			return
		}
		s.logger.Error("failed to get media", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get media")
		return
	}

	writeJSON(w, http.StatusOK, m)
}

// handleMediaUpdate handles PUT /api/v1/media/{id}
func (s *Server) handleMediaUpdate(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid media ID")
		return
	}

	var input media.UpdateMediaInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	m, err := s.mediaService.Update(r.Context(), id, &input)
	if err != nil {
		if errors.Is(err, media.ErrInvalidFolder) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, media.ErrMediaNotFound) {
			writeError(w, http.StatusNotFound, "media not found")
			return
		}
		s.logger.Error("failed to update media", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update media")
		return
	}

	writeJSON(w, http.StatusOK, m)
}

// handleMediaDelete handles DELETE /api/v1/media/{id}
func (s *Server) handleMediaDelete(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid media ID")
		return
	}

	err = s.mediaService.Delete(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrMediaNotFound) {
			writeError(w, http.StatusNotFound, "media not found")
			return
		}
		s.logger.Error("failed to delete media", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete media")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "media deleted"})
}

// handleServeMedia handles GET /media/{filepath:*}
// Serves uploaded media files to authenticated or public requests.
func (s *Server) handleServeMedia(w http.ResponseWriter, r *http.Request) {
	filePath := r.PathValue("rest")
	if filePath == "" {
		writeError(w, http.StatusBadRequest, "missing file path")
		return
	}

	// Prevent directory traversal
	cleanPath := filepath.Clean(filePath)
	if strings.Contains(cleanPath, "..") {
		writeError(w, http.StatusForbidden, "invalid path")
		return
	}

	if s.mediaService.ObjectStorage() {
		s.serveStoredObject(w, r, cleanPath)
		return
	}

	// Resolved inside an os.Root: a symlink in the upload directory cannot
	// lead the server to a file outside it.
	file, err := s.mediaService.OpenFile(cleanPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "file not found")
			return
		}
		writeError(w, http.StatusForbidden, "invalid path")
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to access file")
		return
	}
	if stat.IsDir() {
		writeError(w, http.StatusForbidden, "cannot serve directory")
		return
	}

	// Detect content type
	buffer := make([]byte, media.SniffLen)
	n, _ := file.Read(buffer)
	contentType := media.SniffContentType(buffer[:n])
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to access file")
		return
	}

	setMediaHeaders(w, contentType, fmt.Sprintf(`"%x-%x"`, stat.Size(), stat.ModTime().UnixNano()), stat.Size())

	// Serve file
	http.ServeContent(w, r, stat.Name(), stat.ModTime(), file)
}

// setMediaHeaders sets the response headers every served media file gets.
// Only allowlisted media types are shown inline. Anything else already on
// disk (an .html planted by an old import, say) is a download the browser
// cannot run in the site's origin.
func setMediaHeaders(w http.ResponseWriter, contentType, etag string, size int64) {
	if _, ok := media.AllowedExtensionsForMIME(contentType); !ok {
		contentType = "application/octet-stream"
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("Content-Security-Policy", "sandbox")
	} else if contentType == "image/svg+xml" {
		// Stored svgs are sanitised on the way in; this is the second line:
		// shown only as an image, nothing in it can run or load.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		w.Header().Set("X-Content-Type-Options", "nosniff")
	} else if contentType == "application/pdf" {
		// Framed by the site's own pages (the pdf widget); other sites cannot
		// frame it. No sandbox: it breaks the browser's built-in viewer.
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}

	// One day, then revalidate: a replaced file keeps its address, so the
	// browser must be able to learn that the bytes changed (the ETag makes the
	// check cheap).
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400, must-revalidate")
	if etag != "" {
		w.Header().Set("ETag", etag)
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
}

// mediaStoreTimeout bounds the calls a /media request makes to object
// storage (the lookup, the start of a download, a whole spool).
var mediaStoreTimeout = 30 * time.Second

// mediaSpools bounds the objects spooled to local disk at once. Only a Range
// request spools; everything else streams or answers from the lookup.
var mediaSpools = make(chan struct{}, 4)

// serveStoredObject serves /media from a non-local store:
//   - HEAD answers from the lookup, nothing is downloaded (the type comes from
//     the file extension);
//   - a plain GET streams the object, sniffing its type from the first bytes;
//   - a Range GET spools the object to a temporary file so it can seek, behind
//     a small semaphore (503 with Retry-After when all slots are busy).
func (s *Server) serveStoredObject(w http.ResponseWriter, r *http.Request, rel string) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	timer := time.AfterFunc(mediaStoreTimeout, cancel)
	defer timer.Stop()

	// The validator comes from a cheap lookup, so a browser that already has
	// the bytes never makes the server fetch them.
	info, err := s.mediaService.StatObject(ctx, rel)
	if err != nil {
		mediaStoreFailure(w, err)
		return
	}
	if info.ETag != "" && ifNoneMatchHits(r, info.ETag) {
		w.Header().Set("Cache-Control", "public, max-age=86400, must-revalidate")
		w.Header().Set("ETag", info.ETag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if !info.ModTime.IsZero() {
		w.Header().Set("Last-Modified", info.ModTime.UTC().Format(http.TimeFormat))
	}
	name := filepath.Base(rel)

	switch {
	case r.Method == http.MethodHead:
		setMediaHeaders(w, media.MIMEForExtension(filepath.Ext(name)), info.ETag, info.Size)
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)

	case r.Header.Get("Range") != "":
		select {
		case mediaSpools <- struct{}{}:
			defer func() { <-mediaSpools }()
		default:
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusServiceUnavailable, "media storage is busy, retry shortly")
			return
		}
		rs, got, err := s.mediaService.OpenObject(ctx, rel)
		if err != nil {
			mediaStoreFailure(w, err)
			return
		}
		defer rs.Close()
		timer.Stop()
		head := make([]byte, media.SniffLen)
		n, _ := rs.Read(head)
		if _, err := rs.Seek(0, io.SeekStart); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to access file")
			return
		}
		setMediaHeaders(w, media.SniffContentType(head[:n]), info.ETag, got.Size)
		http.ServeContent(w, r, name, info.ModTime, rs)

	default:
		body, err := s.mediaService.GetObject(ctx, rel)
		if err != nil {
			mediaStoreFailure(w, err)
			return
		}
		defer body.Close()
		timer.Stop()
		br := bufio.NewReaderSize(body, media.SniffLen)
		head, _ := br.Peek(media.SniffLen)
		setMediaHeaders(w, media.SniffContentType(head), info.ETag, info.Size)
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, br)
	}
}

// mediaStoreFailure answers for a store error: a missing file or an invalid
// path as such, anything else as 502 (logged: a bucket outage is not a
// "forbidden").
func mediaStoreFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "file not found")
	case errors.Is(err, media.ErrInvalidKey):
		writeError(w, http.StatusForbidden, "invalid path")
	default:
		slog.Error("media: object storage", "err", err)
		writeError(w, http.StatusBadGateway, "media storage is unavailable")
	}
}

// ifNoneMatchHits reports whether the request's If-None-Match names etag (or
// is "*"). It applies to GET and HEAD only.
func ifNoneMatchHits(r *http.Request, etag string) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	for _, c := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		c = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(c), "W/"))
		if c == "*" || c == etag {
			return true
		}
	}
	return false
}
