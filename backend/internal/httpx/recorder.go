package httpx

import "net/http"

// Recorder wraps a ResponseWriter to record the status code and body size.
// Unwrap exposes the underlying writer to http.ResponseController, so Flush,
// Hijack and deadlines keep working through it.
type Recorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

// NewRecorder wraps w.
func NewRecorder(w http.ResponseWriter) *Recorder { return &Recorder{ResponseWriter: w} }

// Status is the written status code, or 0 when the handler never wrote one.
func (r *Recorder) Status() int { return r.status }

// BytesWritten is the number of body bytes written.
func (r *Recorder) BytesWritten() int { return r.bytes }

func (r *Recorder) WriteHeader(code int) {
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		// Informational (e.g. 103 Early Hints): not the final status.
		r.ResponseWriter.WriteHeader(code)
		return
	}
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *Recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Unwrap returns the wrapped writer for http.ResponseController.
func (r *Recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
