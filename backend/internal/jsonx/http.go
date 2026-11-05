package jsonx

import (
	jsonv2 "encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"
)

// DefaultMaxBody is the request body limit most JSON endpoints use (1 MiB).
const DefaultMaxBody int64 = 1 << 20

// WriteJSON sends v as a JSON response with the given status. The body is
// HTMLSafe encoded and ends in a newline, like the encoders it replaces. A nil
// v writes the status and headers only. Callers that need more headers (cache
// control, nosniff) set them before the call.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	var body []byte
	if v != nil {
		var err error
		body, err = Marshal(v, HTMLSafe)
		if err != nil {
			slog.Error("jsonx: failed to encode response", slog.String("error", err.Error()))
			w.WriteHeader(status)
			return
		}
		body = append(body, '\n')
	}
	w.WriteHeader(status)
	if body != nil {
		_, _ = w.Write(body)
	}
}

// WriteError sends {"error": msg} with the given status.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

// DecodeRequest decodes the JSON request body into v, reading at most max
// bytes (DefaultMaxBody when max <= 0). Without options it uses Strict, so
// trailing data is rejected. It returns the status to answer with on failure
// and 0 on success: 400 for a missing or malformed body, 413 when the body is
// over the limit.
func DecodeRequest(w http.ResponseWriter, r *http.Request, v any, max int64, o ...jsonv2.Options) (status int, err error) {
	if r.Body == nil {
		return http.StatusBadRequest, errors.New("missing request body")
	}
	if max <= 0 {
		max = DefaultMaxBody
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	if err := UnmarshalRead(r.Body, v, o...); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return http.StatusRequestEntityTooLarge, err
		}
		return http.StatusBadRequest, err
	}
	return 0, nil
}
