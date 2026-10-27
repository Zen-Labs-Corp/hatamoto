// Command stub is a stand-in for `hatamoto serve` used by the supervisor
// tests. It reads <exe dir>/behavior (key=value lines):
//
//	ready_delay_ms=N   delay before /ready answers 200
//	ready=never        /ready always 503
//	root=404|500            status of "GET /" in the smoke result (default 200)
//	smoke=fail|500|errors   /smoke answers pass:false, HTTP 500, or plugin_errors
//	crash_after_ms=N   exit 1 after N ms
//	ignore_term=1      ignore SIGTERM (only SIGKILL stops it)
//
// The same lines may instead be appended to the binary after a
// "\n#hmstub-behavior\n" marker, so a staged artifact carries its behavior.
//
// It serves /ready, /health and /id (the exe dir name) on API_PORT and /smoke
// (bearer token HATAMOTO_SMOKE_TOKEN) on HATAMOTO_SMOKE_ADDR.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const trailerMarker = "\n#hmstub-behavior\n"

func main() {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	b := map[string]string{}
	parse := func(raw string) {
		for _, line := range strings.Split(raw, "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
				b[k] = v
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "behavior")); err == nil {
		parse(string(raw))
	}
	if self, err := os.ReadFile(exe); err == nil {
		if i := bytes.LastIndex(self, []byte(trailerMarker)); i >= 0 {
			parse(string(self[i+len(trailerMarker):]))
		}
	}
	num := func(k string) time.Duration {
		n, _ := strconv.Atoi(b[k])
		return time.Duration(n) * time.Millisecond
	}

	start := time.Now()
	delay := num("ready_delay_ms")
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		if b["ready"] == "never" || time.Since(start) < delay {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	})
	mux.HandleFunc("/id", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(filepath.Base(dir))) })
	srv := &http.Server{Addr: ":" + os.Getenv("API_PORT"), Handler: mux}

	var smokeSrv *http.Server
	if addr := os.Getenv("HATAMOTO_SMOKE_ADDR"); addr != "" {
		token := os.Getenv("HATAMOTO_SMOKE_TOKEN")
		sm := http.NewServeMux()
		sm.HandleFunc("/smoke", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(401)
				return
			}
			if b["smoke"] == "500" {
				w.WriteHeader(500)
				return
			}
			root, _ := strconv.Atoi(b["root"])
			if root == 0 {
				root = 200
			}
			check := map[string]any{"name": "GET /", "ok": root < 500, "status": root, "detail": "stub"}
			if b["smoke"] == "fail" {
				check["ok"], check["status"] = false, 0
			}
			res := map[string]any{
				"pass":          b["smoke"] != "fail" && root < 500,
				"checks":        []map[string]any{check},
				"render_errors": 0,
			}
			if b["smoke"] == "errors" {
				res["plugin_errors"] = []string{"stubplugin"}
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(res)
		})
		smokeSrv = &http.Server{Addr: addr, Handler: sm}
		go smokeSrv.ListenAndServe()
	}

	if b["ignore_term"] == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	if d := num("crash_after_ms"); d > 0 {
		go func() { time.Sleep(d); os.Exit(1) }()
	}
	go srv.ListenAndServe()

	sig := make(chan os.Signal, 1)
	if b["ignore_term"] == "1" {
		signal.Notify(sig, syscall.SIGINT) // Notify on SIGTERM would undo Ignore
	} else {
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	}
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	if smokeSrv != nil {
		smokeSrv.Shutdown(ctx)
	}
}
