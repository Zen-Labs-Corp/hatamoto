package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"

	"hatamoto.dev/core/internal/render"
)

// The smoke listener is a second, loopback-only HTTP server the supervisor
// polls after a release switch. It is not part of the public route table: it
// has its own mux with one route, and it only starts when the supervisor sets
// HATAMOTO_SMOKE_ADDR and HATAMOTO_SMOKE_TOKEN for the child.

type smokeCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"` // not a 5xx
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

type smokeResult struct {
	Pass         bool         `json:"pass"`
	Checks       []smokeCheck `json:"checks"`
	RenderErrors int64        `json:"render_errors"`
	BootFaults   []string     `json:"boot_faults"`
	PluginErrors []string     `json:"plugin_errors"`
}

var firstLoc = regexp.MustCompile(`<loc>\s*([^<\s]+)\s*</loc>`)

// startSmoke listens on addr (loopback IP literal only) and serves GET /smoke
// guarded by the bearer token. The returned server is stopped by Shutdown.
func (s *Server) startSmoke(addr, token string) error {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil || !ap.Addr().IsLoopback() {
		return fmt.Errorf("smoke address %q refused: want a loopback ip:port", addr)
	}
	if token == "" {
		return errors.New("smoke token is empty")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("smoke listener: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /smoke", s.smokeHandler(token))
	s.smokeServer = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 30 * time.Second}
	go func() {
		if err := s.smokeServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Warn("smoke listener stopped", slog.String("error", err.Error()))
		}
	}()
	s.logger.Info("smoke listener started", slog.String("addr", addr))
	return nil
}

// startSmokeFromEnv starts the listener when the supervisor asked for it. A
// failure is logged, never fatal: the supervisor's gate then fails fast.
func (s *Server) startSmokeFromEnv(addr, token string) {
	if addr == "" || token == "" {
		return
	}
	if err := s.startSmoke(addr, token); err != nil {
		s.logger.Error("smoke listener not started", slog.String("error", err.Error()))
	}
}

func (s *Server) stopSmoke(ctx context.Context) {
	if s.smokeServer != nil {
		_ = s.smokeServer.Shutdown(ctx)
	}
}

func (s *Server) smokeHandler(token string) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(s.runSmoke())
	})
}

// runSmoke makes the checks in process, through the real router, with no
// network and no proxies in between.
//
// RenderErrors is what the pass's own requests added to the render error
// count. The count only takes records logged with a smoke request's context,
// so visitor errors never reach it, not even while a pass runs. Passes are
// serialised so two of them never see each other's errors. BootFaults are the
// release-level faults logged outside any request (theme fallback at boot),
// read after the requests.
func (s *Server) runSmoke() smokeResult {
	s.smokeMu.Lock()
	defer s.smokeMu.Unlock()
	errsBefore := RenderErrorCount()
	res := smokeResult{Pass: true, PluginErrors: []string{}}
	// The server only reports what each request returned; whether that is
	// acceptable is the supervisor's call, relative to the last good run. A
	// 5xx is never acceptable.
	add := func(name string, status int, detail string) {
		ok := status < 500
		res.Checks = append(res.Checks, smokeCheck{Name: name, OK: ok, Status: status, Detail: detail})
		if !ok {
			res.Pass = false
		}
	}
	get := func(name, path string) *httptest.ResponseRecorder {
		rec := s.smokeGet(path)
		add(name, rec.Code, fmt.Sprint(rec.Code))
		return rec
	}

	get("GET /", "/")
	sm := get("GET /sitemap.xml", "/sitemap.xml")
	if sm.Code == http.StatusOK {
		if m := firstLoc.FindStringSubmatch(sm.Body.String()); m != nil {
			switch page := locPath(m[1]); page {
			case "":
				add("published page", 0, "no pages")
			case "/":
				add("published page", 0, "home only")
			default:
				get("published page", page)
			}
		} else {
			add("published page", 0, "no pages")
		}
	}
	// The name is the baseline key the update gate compares: keep it stable, not the address.
	get("GET /admin/login", s.adminPathValue()+"/login")

	for _, st := range s.pluginStatuses() {
		if st.Error != "" {
			res.PluginErrors = append(res.PluginErrors, st.Name)
		}
	}
	sort.Strings(res.PluginErrors)
	res.RenderErrors = max(0, RenderErrorCount()-errsBefore)
	// Read last: the pass's own first render can be what notes a fault (plugin
	// templates are vetted lazily on the first page build).
	res.BootFaults = render.BootFaults()
	return res
}

func (s *Server) smokeGet(path string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(smokeContext(context.Background()), http.MethodGet, path, nil)
	req.Header.Set("User-Agent", "hatamoto-smoke")
	req.Header.Set("X-Hatamoto-Smoke", "1")
	req.Header.Set("Cache-Control", "no-cache")
	// Fetch metadata of a typed-in address: the admin guard serves documents to
	// top-level navigations only.
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

// locPath is the path (and query) of a sitemap <loc> URL.
func locPath(loc string) string {
	i := strings.Index(loc, "://")
	if i < 0 {
		return ""
	}
	rest := loc[i+3:]
	j := strings.Index(rest, "/")
	if j < 0 {
		return "/"
	}
	return rest[j:]
}
