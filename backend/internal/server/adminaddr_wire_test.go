package server

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/hooks"
)

// wiredHandler is buildHandler itself (intercept included) over the themed
// route shell, with the admin address set and the demo middlewares on or off.
func wiredHandler(t *testing.T, demo bool, prefix string) (http.Handler, *Server) {
	t.Helper()
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.EnableCookieAuth = true
	s.cfg.Demo.Enabled = demo
	s.cfg.Admin.Resolved = prefix
	s.validateSession = fakeSession
	s.validateAPICookie = fakeAPISession
	s.hooks = hooks.New(s.logger)
	s.setupRoutes()
	h := s.buildHandler()
	t.Cleanup(func() { s.stopProbes() })
	publicThemeFixture(t, s)
	s.registerNotFound()
	return h, s
}

// stripDemoBanner removes what the demo middlewares add to a public page: the
// banner strip and the Content-Length it changes. The intercept sits outside
// them (decision: it must run before the mux and answer before any page
// middleware), so a refused probe is the same page without the strip. The
// demo's admin address is /admin, which the demo shows to everyone, so there
// is nothing for that difference to give away; every other byte must match.
func stripDemoBanner(dump string) string {
	dump = strings.ReplaceAll(dump, demoBannerHTML, "")
	var out []string
	for _, l := range strings.Split(dump, "\n") {
		if !strings.HasPrefix(l, "Content-Length:") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// The scanner table against the handler server.New serves: every probe, signed
// out, is answered as the same shape at a wrong address, with the demo
// middlewares in the chain and without them.
func TestScannerTableWiredHandler(t *testing.T) {
	for _, demo := range []bool{false, true} {
		name := map[bool]string{false: "demo off", true: "demo on"}[demo]
		t.Run(name, func(t *testing.T) {
			prefix := "/admin"
			if !demo {
				prefix = tablePrefix
			}
			h, _ := wiredHandler(t, demo, prefix)
			norm := func(d string) string {
				if demo {
					return stripDemoBanner(d)
				}
				return d
			}
			// The demo's write budget answers a busy client's POSTs with 429 on
			// public paths (the intercept's refusals do not spend it); that is
			// a demo-only difference with nothing to hide, so demo compares reads.
			methods := []string{"GET", "HEAD", "POST"}
			if demo {
				methods = methods[:2]
			}
			compared := 0
			for _, p := range scannerProbes {
				under := underPrefix(strings.SplitN(p, "?", 2)[0], prefix)
				if under && prefix == defaultAdminPrefix {
					continue // the default address passes everything through to the admin
				}
				if under && !strings.Contains(p, "%") && isCleanPath(p) {
					continue // the real admin address (login, setup) is not a wrong guess
				}
				for _, m := range methods {
					got := tableReq{m, p, false, nil}.dump(h)
					random := randomLike(p, len(scannerProbes))
					want := tableReq{m, random, false, nil}.dump(h)
					swapped := strings.ReplaceAll(got, echoed(p), echoed(random))
					if c := cleanedTarget(p); c != echoed(p) {
						swapped = strings.ReplaceAll(swapped, c, cleanedTarget(random))
					}
					if strings.Contains(p, "%") {
						for _, f := range []string{"Location: %s\n", `<a href="%s">`} {
							swapped = strings.ReplaceAll(swapped, fmt.Sprintf(f, decodedClean(p)), fmt.Sprintf(f, decodedClean(random)))
						}
					}
					if norm(swapped) != norm(want) {
						t.Fatalf("%s %s differs from the answer for an unknown path\n--- want (%s) ---\n%s\n--- got ---\n%s", m, p, random, want, got)
					}
					compared++
				}
			}
			mounted := mountedProbes
			if prefix == defaultAdminPrefix {
				mounted = nil // the default address is not hidden
			}
			for _, p := range mounted {
				for _, m := range []string{"GET", "HEAD"} {
					got := tableReq{m, prefix + p, false, nil}.dump(h)
					want := tableReq{m, "/zz-guess" + p, false, nil}.dump(h)
					if norm(got) != norm(strings.ReplaceAll(want, "zz-guess", strings.TrimPrefix(prefix, "/"))) {
						t.Errorf("%s %s%s differs from the wrong guess\n--- guess ---\n%s\n--- got ---\n%s", m, prefix, p, want, got)
					}
					compared++
				}
			}
			if compared < 400 {
				t.Fatalf("only %d comparisons", compared)
			}
		})
	}
}

func TestSetupURL(t *testing.T) {
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Admin.Resolved = "/k7qm-x2rf-9tdc"
	if got := s.setupURL(); got != "/k7qm-x2rf-9tdc/setup" {
		t.Errorf("no base url: %q", got)
	}
	s.cfg.PublicSite.BaseURL = "https://site.example.test/"
	if got := s.setupURL(); got != "https://site.example.test/k7qm-x2rf-9tdc/setup" {
		t.Errorf("with base url: %q", got)
	}
}

func TestWarnIfAdminAddressServed(t *testing.T) {
	s, err := routeShell()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, nil))
	s.cfg.Admin.Resolved = "/k7qm-x2rf-9tdc"
	s.warnIfAdminAddressServed(context.Background())
	if logs.Len() != 0 {
		t.Errorf("a free address warned: %s", &logs)
	}
	s.cfg.Admin.Resolved = "/api"
	s.warnIfAdminAddressServed(context.Background())
	if !strings.Contains(logs.String(), "admin address /api is also used by the site") {
		t.Errorf("no warning for a reserved address: %s", &logs)
	}
}
