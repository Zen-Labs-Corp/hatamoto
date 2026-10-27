package agents

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/database"
)

const (
	defaultRangeDays = 30
	maxRangeDays     = 366
	defaultPerPage   = 50
	maxPerPage       = 200
	maxPage          = 10000
	topAgentsLimit   = 10
)

// AdminStore is everything the admin handlers read from the database.
type AdminStore interface {
	StatsStore
	EntityStore
	RobotsStore
}

type adminRepository struct {
	*StatsRepository
	*RobotsRepository
}

// NewAdminStore returns the Postgres AdminStore.
func NewAdminStore(db *database.DB) AdminStore {
	return adminRepository{NewStatsRepository(db), NewRobotsRepository(db)}
}

// AdminHandlers serves /api/v1/agent-analytics/*. The server mounts it in the
// authenticated admin group. No response type here carries an IP field; the
// spoof log exposes a masked network (ip_masked) only.
type AdminHandlers struct {
	store AdminStore
	reg   *Registry
	stats func() WriterStats
	now   func() time.Time
}

// NewAdminHandlers creates the admin handlers. stats may be nil (dropped_hits
// then reports 0).
func NewAdminHandlers(store AdminStore, reg *Registry, stats func() WriterStats) *AdminHandlers {
	return &AdminHandlers{store: store, reg: reg, stats: stats, now: time.Now}
}

func writeAdminJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAdminError(w http.ResponseWriter, status int, msg string) {
	writeAdminJSON(w, status, map[string]string{"error": msg})
}

// parseRange reads ?from&to (YYYY-MM-DD, inclusive, UTC). Defaults to the last
// 30 days ending today. A bad date, from > to, or a span over 366 days is 400.
func (h *AdminHandlers) parseRange(w http.ResponseWriter, r *http.Request) (Range, bool) {
	today := h.now().UTC().Truncate(24 * time.Hour)
	rg := Range{From: today.AddDate(0, 0, -(defaultRangeDays - 1)), To: today}
	q := r.URL.Query()
	parse := func(name string, dst *time.Time) bool {
		v := q.Get(name)
		if v == "" {
			return true
		}
		t, err := time.Parse(dayFmt, v)
		if err != nil {
			writeAdminError(w, http.StatusBadRequest, name+" must be YYYY-MM-DD")
			return false
		}
		*dst = t
		return true
	}
	hasFrom, hasTo := q.Get("from") != "", q.Get("to") != ""
	if !parse("from", &rg.From) || !parse("to", &rg.To) {
		return rg, false
	}
	switch {
	case hasTo && !hasFrom:
		rg.From = rg.To.AddDate(0, 0, -(defaultRangeDays - 1))
	case hasFrom && !hasTo && rg.From.After(rg.To):
		rg.To = rg.From
	}
	if rg.From.After(rg.To) {
		writeAdminError(w, http.StatusBadRequest, "from must not be after to")
		return rg, false
	}
	if rg.To.Sub(rg.From) > (maxRangeDays-1)*24*time.Hour {
		writeAdminError(w, http.StatusBadRequest, "range must not exceed 366 days")
		return rg, false
	}
	return rg, true
}

// parsePage reads page/per_page. A page beyond maxPage is answered with 400
// (ok=false): deep OFFSETs only force a full scan, then discard it.
func parsePage(w http.ResponseWriter, r *http.Request) (page, perPage int, p Page, ok bool) {
	page, perPage = 1, defaultPerPage
	if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v > 0 {
		if v > maxPage {
			writeAdminError(w, http.StatusBadRequest, "page must not exceed 10000")
			return 0, 0, Page{}, false
		}
		page = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && v > 0 {
		perPage = min(v, maxPerPage)
	}
	return page, perPage, Page{Limit: perPage, Offset: (page - 1) * perPage}, true
}

// agentName resolves a display name; ids from migrated legacy rows are not in
// the registry and fall back to the id.
func (h *AdminHandlers) agentName(id string) string {
	if a := h.reg.ByID(id); a != nil {
		return a.Name
	}
	return id
}

type agentRow struct {
	AgentTotals
	Name string `json:"name"`
}

func (h *AdminHandlers) agentRows(in []AgentTotals) []agentRow {
	out := make([]agentRow, 0, len(in))
	for _, a := range in {
		out = append(out, agentRow{AgentTotals: a, Name: h.agentName(a.AgentID)})
	}
	return out
}

// verifiedPct is verified / decided hits (pending excluded), nil when nothing
// was decided yet.
func verifiedPct(verified, spoofed, unverifiable int64) *float64 {
	decided := verified + spoofed + unverifiable
	if decided == 0 {
		return nil
	}
	v := float64(verified) / float64(decided) * 100
	return &v
}

type overviewResponse struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Group  string `json:"group"`
	Totals struct {
		AIFetches        int64    `json:"ai_fetches"`
		Verified         int64    `json:"verified"`
		Spoofed          int64    `json:"spoofed"`
		Unverifiable     int64    `json:"unverifiable"`
		VerifiedPercent  *float64 `json:"verified_percent"`
		AIReferredVisits int64    `json:"ai_referred_visits"`
		Pageviews        int64    `json:"pageviews"`
	} `json:"totals"`
	Series      []SeriesPoint `json:"series"`
	TopAgents   []agentRow    `json:"top_agents"`
	DroppedHits uint64        `json:"dropped_hits"`
}

// HandleOverview serves GET /overview?from&to&group=operator|purpose. Counts
// cover AI purposes only (training, search_index, user_fetch, other_ai).
func (h *AdminHandlers) HandleOverview(w http.ResponseWriter, r *http.Request) {
	rg, ok := h.parseRange(w, r)
	if !ok {
		return
	}
	group := r.URL.Query().Get("group")
	switch group {
	case "":
		group = "operator"
	case "operator", "purpose":
	default:
		writeAdminError(w, http.StatusBadRequest, "group must be operator or purpose")
		return
	}
	ctx := r.Context()
	totals, err := h.store.Overview(ctx, rg)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "failed to load overview")
		return
	}
	series, err := h.store.Series(ctx, rg, group)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "failed to load overview")
		return
	}
	top, err := h.store.TopAgents(ctx, rg, topAgentsLimit)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "failed to load overview")
		return
	}
	var resp overviewResponse
	resp.From, resp.To, resp.Group = rg.From.Format(dayFmt), rg.To.Format(dayFmt), group
	resp.Totals.AIFetches = totals.AIFetches
	resp.Totals.Verified, resp.Totals.Spoofed, resp.Totals.Unverifiable = totals.Verified, totals.Spoofed, totals.Unverifiable
	resp.Totals.VerifiedPercent = verifiedPct(totals.Verified, totals.Spoofed, totals.Unverifiable)
	resp.Totals.AIReferredVisits, resp.Totals.Pageviews = totals.AIVisits, totals.Pageviews
	resp.Series = series
	if resp.Series == nil {
		resp.Series = []SeriesPoint{}
	}
	resp.TopAgents = h.agentRows(top)
	if h.stats != nil {
		resp.DroppedHits = h.stats().Dropped
	}
	writeAdminJSON(w, http.StatusOK, resp)
}

// HandlePages serves GET /pages?from&to&page&per_page: top fetched pages in
// the range and published entities never fetched by any AI agent (all time).
// page/per_page apply to both lists.
func (h *AdminHandlers) HandlePages(w http.ResponseWriter, r *http.Request) {
	rg, ok := h.parseRange(w, r)
	if !ok {
		return
	}
	page, perPage, p, ok := parsePage(w, r)
	if !ok {
		return
	}
	top, topTotal, err := h.store.TopPages(r.Context(), rg, p)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "failed to load pages")
		return
	}
	never, neverTotal, err := h.store.NeverFetched(r.Context(), p)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "failed to load pages")
		return
	}
	if top == nil {
		top = []PageRow{}
	}
	if never == nil {
		never = []NeverFetchedRow{}
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"from": rg.From.Format(dayFmt), "to": rg.To.Format(dayFmt),
		"page": page, "per_page": perPage,
		"top": top, "top_total": topTotal,
		"never_fetched": never, "never_fetched_total": neverTotal,
	})
}

// HandleAgents serves GET /agents?from&to: every agent seen in the range.
func (h *AdminHandlers) HandleAgents(w http.ResponseWriter, r *http.Request) {
	rg, ok := h.parseRange(w, r)
	if !ok {
		return
	}
	list, err := h.store.Agents(r.Context(), rg)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "failed to load agents")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"from": rg.From.Format(dayFmt), "to": rg.To.Format(dayFmt),
		"agents": h.agentRows(list),
	})
}

type agentDetailResponse struct {
	AgentID         string        `json:"agent_id"`
	Name            string        `json:"name"`
	Operator        string        `json:"operator"`
	Purpose         string        `json:"purpose"`
	RobotsToken     string        `json:"robots_token"`
	IgnoresRobots   bool          `json:"ignores_robots"`
	From            string        `json:"from"`
	To              string        `json:"to"`
	Hits            int64         `json:"hits"`
	Verified        int64         `json:"verified_hits"`
	Spoofed         int64         `json:"spoofed_hits"`
	Unverifiable    int64         `json:"unverifiable_hits"`
	VerifiedPercent *float64      `json:"verified_percent"`
	LastSeenAt      *time.Time    `json:"last_seen_at"`
	Series          []DayCount    `json:"series"`
	TopPaths        []PathCount   `json:"top_paths"`
	ByStatus        []StatusCount `json:"by_status"`
}

// HandleAgentDetail serves GET /agents/{id}?from&to. 404 for an id that is
// neither in the registry nor in the rollup.
func (h *AdminHandlers) HandleAgentDetail(w http.ResponseWriter, r *http.Request) {
	rg, ok := h.parseRange(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	d, err := h.store.AgentDetail(r.Context(), rg, id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}
	reg := h.reg.ByID(id)
	if !d.Found && reg == nil {
		writeAdminError(w, http.StatusNotFound, "agent not found")
		return
	}
	resp := agentDetailResponse{
		AgentID: id, Name: h.agentName(id), Operator: d.Totals.Operator, Purpose: d.Totals.Purpose,
		From: rg.From.Format(dayFmt), To: rg.To.Format(dayFmt),
		Hits: d.Totals.Hits, Verified: d.Totals.Verified, Spoofed: d.Totals.Spoofed, Unverifiable: d.Totals.Unverifiable,
		VerifiedPercent: verifiedPct(d.Totals.Verified, d.Totals.Spoofed, d.Totals.Unverifiable),
		LastSeenAt:      d.LastSeenAt,
		Series:          d.Series, TopPaths: d.TopPaths, ByStatus: d.ByStatus,
	}
	if reg != nil {
		resp.Operator, resp.Purpose = reg.Operator, string(reg.Purpose)
		resp.RobotsToken, resp.IgnoresRobots = reg.RobotsToken, reg.IgnoresRobots
	}
	if resp.Series == nil {
		resp.Series = []DayCount{}
	}
	if resp.TopPaths == nil {
		resp.TopPaths = []PathCount{}
	}
	if resp.ByStatus == nil {
		resp.ByStatus = []StatusCount{}
	}
	writeAdminJSON(w, http.StatusOK, resp)
}

type errorResponseRow struct {
	ErrorRow
	Agents []string `json:"agents"`
}

// HandleErrors serves GET /errors?from&to&page&per_page: agent fetches that
// got 4xx/5xx, grouped by path and status.
func (h *AdminHandlers) HandleErrors(w http.ResponseWriter, r *http.Request) {
	rg, ok := h.parseRange(w, r)
	if !ok {
		return
	}
	page, perPage, p, ok := parsePage(w, r)
	if !ok {
		return
	}
	rows, total, err := h.store.Errors(r.Context(), rg, p)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "failed to load errors")
		return
	}
	out := make([]errorResponseRow, 0, len(rows))
	for _, e := range rows {
		names := make([]string, 0, len(e.AgentIDs))
		for _, id := range e.AgentIDs {
			names = append(names, h.agentName(id))
		}
		out = append(out, errorResponseRow{ErrorRow: e, Agents: names})
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"from": rg.From.Format(dayFmt), "to": rg.To.Format(dayFmt),
		"page": page, "per_page": perPage, "total": total, "errors": out,
	})
}

type spoofedResponseRow struct {
	SpoofedRow
	Name string `json:"agent_name"`
}

// HandleSpoofed serves GET /spoofed?from&to&page&per_page: the spoof log with
// the network (/24 or /48) of each offending address, never the address.
func (h *AdminHandlers) HandleSpoofed(w http.ResponseWriter, r *http.Request) {
	rg, ok := h.parseRange(w, r)
	if !ok {
		return
	}
	page, perPage, p, ok := parsePage(w, r)
	if !ok {
		return
	}
	rows, total, err := h.store.Spoofed(r.Context(), rg, p)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "failed to load spoofed hits")
		return
	}
	out := make([]spoofedResponseRow, 0, len(rows))
	for _, s := range rows {
		out = append(out, spoofedResponseRow{SpoofedRow: s, Name: h.agentName(s.AgentID)})
	}
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"from": rg.From.Format(dayFmt), "to": rg.To.Format(dayFmt),
		"page": page, "per_page": perPage, "total": total, "hits": out,
	})
}

type entityAgentRow struct {
	AgentID     string     `json:"agent_id"`
	Name        string     `json:"name"`
	Operator    string     `json:"operator"`
	Purpose     string     `json:"purpose"`
	LastFetchAt *time.Time `json:"last_fetch_at"`
	LastStatus  *int       `json:"last_status"`
}

type entityActivityResponse struct {
	FirstCrawledAfterPublishSeconds *int64           `json:"first_crawled_after_publish_seconds"`
	FirstCrawlAt                    *time.Time       `json:"first_crawl_at"`
	UserFetch7d                     int64            `json:"user_fetch_7d"`
	UserFetch30d                    int64            `json:"user_fetch_30d"`
	Agents                          []entityAgentRow `json:"agents"`
}

// aiAgentsWithUA are the registry agents that can actually show up in hits:
// AI purposes with a UA pattern (token-only entries never match).
func (h *AdminHandlers) aiAgentsWithUA() []Agent {
	out := []Agent{}
	for _, a := range h.reg.Agents {
		if a.Purpose.IsAI() && len(a.UA) > 0 {
			out = append(out, a)
		}
	}
	return out
}

// HandleEntityActivity serves GET /entities/{id}: the editor's AI Agents card.
// Every AI agent in the registry appears, with last_fetch_at null for "never".
// first_crawled_after_publish_seconds is null when there is no crawl yet, no
// publish time, the crawl predates the (re)publish, or the entity was published
// before tracking began (its first crawl may long predate the tracker).
// first_crawl_at is likewise null for entities published before tracking
// began: the recorded row is only the first crawl since deploy, not the real
// first crawl, so the card falls back to the newest per-agent fetch.
func (h *AdminHandlers) HandleEntityActivity(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid entity id")
		return
	}
	agents := h.aiAgentsWithUA()
	ids := make([]string, len(agents))
	for i, a := range agents {
		ids[i] = a.ID
	}
	d, err := h.store.EntityActivity(r.Context(), id, ids, h.now().UTC().Truncate(24*time.Hour))
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "failed to load entity activity")
		return
	}
	if !d.Exists {
		writeAdminError(w, http.StatusNotFound, "entity not found")
		return
	}
	resp := entityActivityResponse{UserFetch7d: d.UserFetch7d, UserFetch30d: d.UserFetch30d, Agents: make([]entityAgentRow, 0, len(agents))}
	trackedSincePublish := d.PublishedAt != nil && d.TrackingFrom != nil && !d.PublishedAt.Before(*d.TrackingFrom)
	if trackedSincePublish {
		resp.FirstCrawlAt = d.FirstAt
	}
	if d.FirstAt != nil && trackedSincePublish && !d.FirstAt.Before(*d.PublishedAt) {
		secs := int64(d.FirstAt.Sub(*d.PublishedAt).Seconds())
		resp.FirstCrawledAfterPublishSeconds = &secs
	}
	for _, a := range agents {
		row := entityAgentRow{AgentID: a.ID, Name: a.Name, Operator: a.Operator, Purpose: string(a.Purpose)}
		if lf, ok := d.Last[a.ID]; ok {
			at := lf.At
			row.LastFetchAt, row.LastStatus = &at, lf.Status
		}
		resp.Agents = append(resp.Agents, row)
	}
	writeAdminJSON(w, http.StatusOK, resp)
}
