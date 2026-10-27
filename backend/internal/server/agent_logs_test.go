package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/httpx"

	"hatamoto.dev/core/internal/agents"
	"hatamoto.dev/core/internal/config"
)

type logRecorder struct{ hits []agents.RawHit }

func (l *logRecorder) Record(h agents.RawHit) bool {
	l.hits = append(l.hits, h)
	return true
}

func callAgentLogs(t *testing.T, cfg config.AgentAnalyticsConfig, secret string) (int, *logRecorder) {
	t.Helper()
	rec := &logRecorder{}
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	mountAgentLogRoute(r, newAgentLogHandlers(cfg, rec, slog.New(slog.NewTextHandler(io.Discard, nil))))
	body := `{"ClientRequestURI":"/media/a.png","ClientRequestUserAgent":"GPTBot/1.2","EdgeResponseStatus":200}`
	req := httptest.NewRequest(http.MethodPost, "/agent-logs", strings.NewReader(body))
	if secret != "" {
		req.Header.Set("X-Agent-Log-Secret", secret)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr.Code, rec
}

func TestAgentLogRouteAbsentByDefault(t *testing.T) {
	for name, cfg := range map[string]config.AgentAnalyticsConfig{
		"default":             {},
		"secret without flag": {LogIngestSecret: "s"},
		"flag without secret": {LogIngestEnabled: true},
	} {
		if code, rec := callAgentLogs(t, cfg, "s"); code != http.StatusNotFound || len(rec.hits) != 0 {
			t.Errorf("%s: want 404 and no hits, got %d / %d", name, code, len(rec.hits))
		}
	}
}

func TestAgentLogRouteEnabled(t *testing.T) {
	cfg := config.AgentAnalyticsConfig{LogIngestEnabled: true, LogIngestSecret: "s"}
	if code, _ := callAgentLogs(t, cfg, "bad"); code != http.StatusUnauthorized {
		t.Errorf("wrong secret: %d", code)
	}
	code, rec := callAgentLogs(t, cfg, "s")
	if code != http.StatusAccepted || len(rec.hits) != 1 || rec.hits[0].Source != agents.SourceLog {
		t.Errorf("want 202 and one log hit, got %d %+v", code, rec.hits)
	}
}
