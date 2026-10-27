package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/httpx"
)

// fakeAdminStore returns nil slices on purpose: handlers must still emit [].
type fakeAdminStore struct {
	totals    OverviewTotals
	detail    AgentDetailData
	activity  EntityActivityData
	denied    []Purpose
	setDenied []Purpose
	setCalled bool
	gotRange  Range
	gotGroup  string
	gotPage   Page
	err       error
}

func (f *fakeAdminStore) Overview(_ context.Context, rg Range) (OverviewTotals, error) {
	f.gotRange = rg
	return f.totals, f.err
}
func (f *fakeAdminStore) Series(_ context.Context, _ Range, g string) ([]SeriesPoint, error) {
	f.gotGroup = g
	return nil, f.err
}
func (f *fakeAdminStore) TopAgents(context.Context, Range, int) ([]AgentTotals, error) {
	return nil, f.err
}
func (f *fakeAdminStore) TopPages(_ context.Context, _ Range, p Page) ([]PageRow, int, error) {
	f.gotPage = p
	return nil, 0, f.err
}
func (f *fakeAdminStore) NeverFetched(context.Context, Page) ([]NeverFetchedRow, int, error) {
	return nil, 0, f.err
}
func (f *fakeAdminStore) Agents(context.Context, Range) ([]AgentTotals, error) { return nil, f.err }
func (f *fakeAdminStore) AgentDetail(context.Context, Range, string) (AgentDetailData, error) {
	return f.detail, f.err
}
func (f *fakeAdminStore) Errors(_ context.Context, _ Range, p Page) ([]ErrorRow, int, error) {
	f.gotPage = p
	return nil, 0, f.err
}
func (f *fakeAdminStore) Spoofed(_ context.Context, _ Range, p Page) ([]SpoofedRow, int, error) {
	f.gotPage = p
	return nil, 0, f.err
}

func (f *fakeAdminStore) EntityActivity(context.Context, uuid.UUID, []string, time.Time) (EntityActivityData, error) {
	return f.activity, f.err
}

func (f *fakeAdminStore) DeniedPurposes(context.Context) ([]Purpose, error) {
	return f.denied, f.err
}
func (f *fakeAdminStore) SetDenied(_ context.Context, d []Purpose) error {
	f.setDenied, f.setCalled = d, true
	return f.err
}

func adminRouter(h *AdminHandlers) http.Handler {
	mux := http.NewServeMux()
	r := httpx.NewGroup(mux, "")
	r.HandleFunc("GET /overview", h.HandleOverview)
	r.HandleFunc("GET /pages", h.HandlePages)
	r.HandleFunc("GET /agents", h.HandleAgents)
	r.HandleFunc("GET /agents/{id}", h.HandleAgentDetail)
	r.HandleFunc("GET /errors", h.HandleErrors)
	r.HandleFunc("GET /spoofed", h.HandleSpoofed)
	r.HandleFunc("GET /entities/{id}", h.HandleEntityActivity)
	r.HandleFunc("GET /robots-policy", h.HandleGetRobotsPolicy)
	r.HandleFunc("PUT /robots-policy", h.HandlePutRobotsPolicy)
	return mux
}

func newTestAdmin(f *fakeAdminStore) *AdminHandlers {
	h := NewAdminHandlers(f, Default(), func() WriterStats { return WriterStats{Dropped: 3} })
	h.now = func() time.Time { return time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC) }
	return h
}

func get(t *testing.T, h http.Handler, url string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, url, nil))
	return rr
}

func TestAdminArraysNeverNull(t *testing.T) {
	h := adminRouter(newTestAdmin(&fakeAdminStore{detail: AgentDetailData{Found: true}}))
	for _, u := range []string{"/overview", "/pages", "/agents", "/agents/gptbot", "/errors", "/spoofed"} {
		rr := get(t, h, u)
		if rr.Code != 200 {
			t.Fatalf("%s: %d %s", u, rr.Code, rr.Body)
		}
		if strings.Contains(rr.Body.String(), "null") && u != "/overview" && u != "/agents/gptbot" {
			t.Errorf("%s: contains null: %s", u, rr.Body)
		}
		var m map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		for k, v := range m {
			if arr, isArr := v.([]any); isArr && arr == nil {
				t.Errorf("%s: %s is null", u, k)
			}
		}
	}
	// overview/detail legitimately carry null scalars (verified_percent); arrays must be [].
	var ov map[string]any
	_ = json.Unmarshal(get(t, h, "/overview").Body.Bytes(), &ov)
	for _, k := range []string{"series", "top_agents"} {
		if _, ok := ov[k].([]any); !ok {
			t.Errorf("overview %s = %v, want []", k, ov[k])
		}
	}
	var det map[string]any
	_ = json.Unmarshal(get(t, h, "/agents/gptbot").Body.Bytes(), &det)
	for _, k := range []string{"series", "top_paths", "by_status"} {
		if _, ok := det[k].([]any); !ok {
			t.Errorf("detail %s = %v, want []", k, det[k])
		}
	}
}

func TestAdminOverviewShape(t *testing.T) {
	f := &fakeAdminStore{totals: OverviewTotals{AIFetches: 100, Verified: 9, Spoofed: 1, AIVisits: 5, Pageviews: 7}}
	rr := get(t, adminRouter(newTestAdmin(f)), "/overview?group=purpose")
	var got struct {
		Group  string `json:"group"`
		From   string `json:"from"`
		To     string `json:"to"`
		Totals struct {
			AIFetches int64    `json:"ai_fetches"`
			Pct       *float64 `json:"verified_percent"`
			Visits    int64    `json:"ai_referred_visits"`
			Views     int64    `json:"pageviews"`
		} `json:"totals"`
		Dropped uint64 `json:"dropped_hits"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Group != "purpose" || f.gotGroup != "purpose" {
		t.Errorf("group = %q / %q", got.Group, f.gotGroup)
	}
	if got.From != "2026-09-21" || got.To != "2026-10-20" {
		t.Errorf("default range = %s..%s", got.From, got.To)
	}
	if got.Totals.AIFetches != 100 || got.Totals.Visits != 5 || got.Totals.Views != 7 {
		t.Errorf("totals = %+v", got.Totals)
	}
	if got.Totals.Pct == nil || *got.Totals.Pct != 90 {
		t.Errorf("verified_percent = %v, want 90", got.Totals.Pct)
	}
	if got.Dropped != 3 {
		t.Errorf("dropped=%d", got.Dropped)
	}

	// Nothing decided yet: null, not 0.
	rr = get(t, adminRouter(newTestAdmin(&fakeAdminStore{})), "/overview")
	if !strings.Contains(rr.Body.String(), `"verified_percent":null`) {
		t.Errorf("want null verified_percent: %s", rr.Body)
	}
}

func TestAdminValidation(t *testing.T) {
	h := adminRouter(newTestAdmin(&fakeAdminStore{}))
	for _, u := range []string{
		"/overview?from=nope",
		"/overview?to=2026-13-40",
		"/overview?from=2026-10-10&to=2026-10-01",
		"/overview?from=2024-01-01&to=2026-10-01",
		"/overview?group=agent",
		"/errors?from=x",
	} {
		if rr := get(t, h, u); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", u, rr.Code)
		}
	}
	if rr := get(t, h, "/overview?from=2026-10-01&to=2026-10-31"); rr.Code != 200 {
		t.Errorf("valid range: %d", rr.Code)
	}
}

func TestAdminPerPageCap(t *testing.T) {
	f := &fakeAdminStore{}
	h := adminRouter(newTestAdmin(f))
	get(t, h, "/errors?per_page=9999&page=3")
	if f.gotPage.Limit != 200 || f.gotPage.Offset != 400 {
		t.Errorf("page = %+v, want limit 200 offset 400", f.gotPage)
	}
	get(t, h, "/spoofed?per_page=abc&page=0")
	if f.gotPage.Limit != 50 || f.gotPage.Offset != 0 {
		t.Errorf("defaults = %+v", f.gotPage)
	}
}

func TestAdminAgentDetail404(t *testing.T) {
	h := adminRouter(newTestAdmin(&fakeAdminStore{}))
	if rr := get(t, h, "/agents/not-a-real-agent"); rr.Code != 404 {
		t.Errorf("unknown agent: %d, want 404", rr.Code)
	}
	if rr := get(t, h, "/agents/gptbot"); rr.Code != 200 {
		t.Errorf("registry agent without data: %d, want 200", rr.Code)
	}
}

func TestAdminStoreErrorIs500(t *testing.T) {
	h := adminRouter(newTestAdmin(&fakeAdminStore{err: context.DeadlineExceeded}))
	if rr := get(t, h, "/overview"); rr.Code != 500 {
		t.Errorf("code = %d", rr.Code)
	}
}

// ISC-A-1: no admin response type may serialise an IP.
func TestAdminResponseTypesHaveNoIP(t *testing.T) {
	types := []any{
		overviewResponse{}, AgentTotals{}, PageRow{}, NeverFetchedRow{}, ErrorRow{}, SpoofedRow{},
		agentDetailResponse{}, entityActivityResponse{}, entityAgentRow{}, errorResponseRow{}, spoofedResponseRow{},
	}
	for _, v := range types {
		walkJSONNames(t, reflect.TypeOf(v), func(name string) {
			if n := strings.ToLower(name); n == "ip" || n == "ip_address" || n == "ips" {
				t.Errorf("%T exposes %q", v, name)
			}
		})
	}
}

func walkJSONNames(t *testing.T, ty reflect.Type, fn func(string)) {
	for ty.Kind() == reflect.Pointer || ty.Kind() == reflect.Slice {
		ty = ty.Elem()
	}
	if ty.Kind() != reflect.Struct || ty.PkgPath() == "time" {
		return
	}
	for f := range ty.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous && name == "" {
			walkJSONNames(t, f.Type, fn)
			continue
		}
		if name == "" {
			name = f.Name
		}
		fn(name)
		walkJSONNames(t, f.Type, fn)
	}
}

func TestVerifiedPct(t *testing.T) {
	if verifiedPct(0, 0, 0) != nil {
		t.Error("want nil")
	}
	if p := verifiedPct(1, 1, 2); p == nil || *p != 25 {
		t.Errorf("pct = %v", p)
	}
}

func TestMaskedIPSQLIsNetworkOnly(t *testing.T) {
	if !strings.Contains(maskedIPSQL, "network(") || !strings.Contains(maskedIPSQL, "24") || !strings.Contains(maskedIPSQL, "48") {
		t.Fatalf("masked ip SQL: %s", maskedIPSQL)
	}
}

func TestAdminEntityActivity(t *testing.T) {
	published := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	first := published.Add(3 * time.Hour)
	seen := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	st := 200
	tracking := published.Add(-24 * time.Hour)
	f := &fakeAdminStore{activity: EntityActivityData{
		Exists: true, FirstAt: &first, PublishedAt: &published, TrackingFrom: &tracking, UserFetch7d: 12, UserFetch30d: 41,
		Last: map[string]LastFetch{"gptbot": {At: seen, Status: &st}},
	}}
	h := adminRouter(newTestAdmin(f))
	id := uuid.NewString()
	rr := get(t, h, "/entities/"+id)
	if rr.Code != 200 {
		t.Fatalf("code %d %s", rr.Code, rr.Body)
	}
	var got struct {
		Latency *int64 `json:"first_crawled_after_publish_seconds"`
		D7      int64  `json:"user_fetch_7d"`
		D30     int64  `json:"user_fetch_30d"`
		Agents  []struct {
			ID     string     `json:"agent_id"`
			Last   *time.Time `json:"last_fetch_at"`
			Status *int       `json:"last_status"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Latency == nil || *got.Latency != 3*3600 || got.D7 != 12 || got.D30 != 41 {
		t.Errorf("latency/counts = %v %d %d", got.Latency, got.D7, got.D30)
	}
	var gpt, never int
	for _, a := range got.Agents {
		switch {
		case a.ID == "gptbot" && a.Last != nil && a.Status != nil && *a.Status == 200:
			gpt++
		case a.Last == nil:
			never++
		}
		if a.ID == "googlebot" || a.ID == "ahrefsbot" {
			t.Errorf("non-AI agent %s listed", a.ID)
		}
	}
	if gpt != 1 || never != len(got.Agents)-1 || len(got.Agents) < 5 {
		t.Errorf("agents = %+v", got.Agents)
	}

	// Published before tracking began: neither latency nor first_crawl_at (the
	// recorded row is only the first crawl since deploy).
	legacyTracking := published.Add(time.Hour)
	f.activity.TrackingFrom = &legacyTracking
	b := get(t, h, "/entities/"+id).Body.String()
	if !strings.Contains(b, `"first_crawled_after_publish_seconds":null`) || !strings.Contains(b, `"first_crawl_at":null`) {
		t.Errorf("pre-tracking entity: %s", b)
	}
	f.activity.TrackingFrom = &tracking

	// Crawl before publish (re-publish) or no crawl: latency null.
	early := published.Add(-time.Hour)
	f.activity.FirstAt = &early
	if b := get(t, h, "/entities/"+id).Body.String(); !strings.Contains(b, `"first_crawled_after_publish_seconds":null`) {
		t.Errorf("want null latency: %s", b)
	}
	f.activity = EntityActivityData{Exists: true}
	if b := get(t, h, "/entities/"+id).Body.String(); !strings.Contains(b, `"first_crawled_after_publish_seconds":null`) || strings.Contains(b, `"agents":null`) {
		t.Errorf("no crawl: %s", b)
	}

	if rr := get(t, h, "/entities/not-a-uuid"); rr.Code != 400 {
		t.Errorf("bad id: %d", rr.Code)
	}
	f.activity = EntityActivityData{}
	if rr := get(t, h, "/entities/"+id); rr.Code != 404 {
		t.Errorf("missing entity: %d", rr.Code)
	}
}

func TestTopPagesGroupsByPathOnly(t *testing.T) {
	if topPagesGroupBy != "GROUP BY d.path" || strings.Contains(topPagesGroupBy, "entity_id") {
		t.Errorf("top pages must group by path only: %s", topPagesGroupBy)
	}
	if !strings.Contains(topPagesSelect, "FILTER (WHERE d.entity_id IS NOT NULL)") {
		t.Errorf("top pages must pick the non-null entity: %s", topPagesSelect)
	}
}

func TestNeverFetchedExcludesVariants(t *testing.T) {
	if !strings.Contains(neverFetchedFrom, "e.variant_key IS NULL") {
		t.Errorf("never-fetched must skip variant rows: %s", neverFetchedFrom)
	}
}

func TestAdminPageBeyondMaxIs400(t *testing.T) {
	h := adminRouter(newTestAdmin(&fakeAdminStore{}))
	for _, u := range []string{"/pages", "/errors", "/spoofed"} {
		if rr := get(t, h, u+"?page=10001"); rr.Code != 400 {
			t.Errorf("%s page=10001: %d, want 400", u, rr.Code)
		}
		if rr := get(t, h, u+"?page=10000"); rr.Code != 200 {
			t.Errorf("%s page=10000: %d, want 200", u, rr.Code)
		}
	}
}
