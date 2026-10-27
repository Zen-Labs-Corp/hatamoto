// Command fakesite is a static file server that logs every request as one JSON
// line. The update end-to-end test uses it as a stand-in for the release site
// and reads the log as evidence of what an install sends. Run from backend/:
//
//	go run ./tools/fakesite --dir DIR --addr 127.0.0.1:PORT --log FILE
//
// It serves exact file paths only: no redirects, no directory listings, no
// index files. It is a dev tool and is never linked into the hatamoto binary.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"
)

type logLine struct {
	Time    string              `json:"t"`
	Remote  string              `json:"remote"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   string              `json:"query"`
	Host    string              `json:"host"`
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// newHandler serves files below dir and writes one JSON line per request to out.
func newHandler(dir string, out io.Writer) http.Handler {
	var mu sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		serveExact(sw, r, dir)
		line, err := json.Marshal(logLine{
			Time:    time.Now().UTC().Format(time.RFC3339Nano),
			Remote:  r.RemoteAddr,
			Method:  r.Method,
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			Host:    r.Host,
			Status:  sw.status,
			Headers: r.Header,
		})
		if err != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_, _ = out.Write(append(line, '\n'))
	})
}

// serveExact answers 200 with the file at the request path, else 404.
func serveExact(w http.ResponseWriter, r *http.Request, dir string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clean := path.Clean("/" + r.URL.Path)
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(clean)))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	// ServeContent never redirects, unlike FileServer for names ending in index.html.
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

func main() {
	dir := flag.String("dir", "", "directory to serve")
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	logPath := flag.String("log", "", "request log file (JSON lines)")
	flag.Parse()
	if *dir == "" || *logPath == "" {
		fmt.Fprintln(os.Stderr, "usage: fakesite --dir DIR --addr HOST:PORT --log FILE")
		os.Exit(2)
	}
	out, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("open log: %v", err)
	}
	defer out.Close()
	srv := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(*dir, out), // os.File writes are unbuffered: one write per line
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
