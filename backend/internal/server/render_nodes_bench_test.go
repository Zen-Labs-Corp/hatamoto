package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/builder"
)

// twelveSectionBody is a converted 12-section page and a request that moves the
// last section to the top and asks for its fragment: the endpoint's common job.
func twelveSectionBody(tb testing.TB) string {
	tb.Helper()
	secs := make([]builder.Section, 12)
	for i := range secs {
		secs[i] = builder.Section{ID: fmt.Sprintf("s%d", i), Type: "html", SortOrder: i + 1,
			Content: map[string]any{"body": fmt.Sprintf("<h2>Section %d</h2><p>Some body text for section %d.</p>", i, i)}}
	}
	return nodesBody(tb, builder.FromSections(secs), map[string]any{
		"ops":    []json.RawMessage{json.RawMessage(`{"op":"move","id":"c-s11","index":0}`)},
		"render": []string{"s11"},
	})
}

func postNodesOnce(tb testing.TB, h interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, body string) {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/edit/render", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		tb.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

// BenchmarkRenderNodesEndpoint: real engine, stub chrome, one move op, one fragment.
func BenchmarkRenderNodesEndpoint(b *testing.B) {
	h := http.HandlerFunc(newPreviewHandlers(b).handleRenderNodes)
	body := twelveSectionBody(b)
	postNodesOnce(b, h, body)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		postNodesOnce(b, h, body)
	}
}

// TestRenderNodesBudget: a wall-clock backstop outside make budgets (the
// deterministic gate is TestBudgetAllocsRenderNodes): the median of 200 requests
// stays under the ceiling, widened to >= 10x the dev-machine median.
func TestRenderNodesBudget(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing")
	}
	h := http.HandlerFunc(newPreviewHandlers(t).handleRenderNodes)
	body := twelveSectionBody(t)
	postNodesOnce(t, h, body) // warm the engine
	times := make([]time.Duration, 200)
	for i := range times {
		start := time.Now()
		postNodesOnce(t, h, body)
		times[i] = time.Since(start)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	if med := times[len(times)/2]; med > 50*time.Millisecond {
		t.Errorf("render nodes median %v, budget 50ms", med)
	}
}
