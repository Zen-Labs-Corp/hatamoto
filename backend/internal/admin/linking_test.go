package admin

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	linkingv2 "hatamoto.dev/core/internal/linking/v2"
)

type fakeLinking struct {
	rules    map[uuid.UUID]*linkingv2.LinkRule
	names    map[uuid.UUID]string
	inserted []InsertOutcome
	insertIn [][3]string
	sugg     []linkingv2.Suggestion
	deleted  []string
	liveIn   []string
}

func newFakeLinking() *fakeLinking {
	return &fakeLinking{rules: map[uuid.UUID]*linkingv2.LinkRule{}, names: map[uuid.UUID]string{}}
}

func (f *fakeLinking) add(phrase string, target uuid.UUID, source string) *linkingv2.LinkRule {
	r := &linkingv2.LinkRule{ID: uuid.New(), TargetEntityID: target, RuleType: linkingv2.RuleTypeKeyword, MatchValue: phrase, Priority: 50, MaxLinksPerSource: 1, IsActive: true, Source: source,
		TargetEntityName: f.names[target], CreatedAt: time.Now()}
	f.rules[r.ID] = r
	return r
}

func (f *fakeLinking) ListRules(_ context.Context, fl *linkingv2.RuleFilter) (*linkingv2.PaginatedRulesResult, error) {
	var out []*linkingv2.LinkRule
	for _, r := range f.rules {
		if fl.Query != "" && !strings.Contains(strings.ToLower(r.MatchValue+" "+r.TargetEntityName), strings.ToLower(fl.Query)) {
			continue
		}
		if fl.Source != "" && r.Source != fl.Source {
			continue
		}
		if fl.IsActive != nil && r.IsActive != *fl.IsActive {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MatchValue < out[j].MatchValue })
	return &linkingv2.PaginatedRulesResult{Rules: out, Total: len(out), Page: 1, PerPage: fl.PerPage}, nil
}
func (f *fakeLinking) GetRule(_ context.Context, id uuid.UUID) (*linkingv2.LinkRule, error) {
	if r, ok := f.rules[id]; ok {
		c := *r
		return &c, nil
	}
	return nil, linkingv2.ErrRuleNotFound
}
func (f *fakeLinking) CreateRule(_ context.Context, in *linkingv2.CreateRuleInput) (*linkingv2.LinkRule, error) {
	for _, r := range f.rules {
		if r.TargetEntityID == in.TargetEntityID && r.MatchValue == in.MatchValue {
			return nil, linkingv2.ErrDuplicateRule
		}
	}
	r := f.add(in.MatchValue, in.TargetEntityID, orDefault(in.Source, "manual"))
	r.Priority, r.MaxLinksPerSource, r.IsActive, r.AnchorText = in.Priority, in.MaxLinksPerSource, in.IsActive, in.AnchorText
	return r, nil
}
func (f *fakeLinking) UpdateRule(_ context.Context, id uuid.UUID, in *linkingv2.UpdateRuleInput) (*linkingv2.LinkRule, error) {
	r, ok := f.rules[id]
	if !ok {
		return nil, linkingv2.ErrRuleNotFound
	}
	if in.MatchValue != nil {
		r.MatchValue = *in.MatchValue
	}
	if in.TargetEntityID != nil {
		r.TargetEntityID = *in.TargetEntityID
	}
	if in.Priority != nil {
		r.Priority = *in.Priority
	}
	if in.MaxLinksPerSource != nil {
		r.MaxLinksPerSource = *in.MaxLinksPerSource
	}
	if in.IsActive != nil {
		r.IsActive = *in.IsActive
	}
	if in.AnchorText != nil {
		r.AnchorText = in.AnchorText
	}
	return r, nil
}
func (f *fakeLinking) DeleteRules(_ context.Context, ids []uuid.UUID) (int, error) {
	n := 0
	for _, id := range ids {
		if _, ok := f.rules[id]; ok {
			delete(f.rules, id)
			n++
		}
	}
	return n, nil
}
func (f *fakeLinking) DeleteRulesBySource(_ context.Context, src string) (int, error) {
	f.deleted = append(f.deleted, src)
	n := 0
	for id, r := range f.rules {
		if r.Source == src {
			delete(f.rules, id)
			n++
		}
	}
	return n, nil
}
func (f *fakeLinking) DeleteAllRules(context.Context) (int, error) {
	n := len(f.rules)
	f.rules = map[uuid.UUID]*linkingv2.LinkRule{}
	return n, nil
}
func (f *fakeLinking) PreviewRule(context.Context, string, uuid.UUID) (linkingv2.RulePreviewResult, error) {
	return linkingv2.RulePreviewResult{MatchCount: 3, InsertableCount: 1, LinkedCount: 2, Matches: []linkingv2.RulePreviewMatch{
		{EntityName: "Blog <post>", EntitySlug: "blog-post", SchemaSlug: "post", Status: "insertable"}, {EntityName: "About", EntitySlug: "about", SchemaSlug: "page", Status: "already_linked"},
		{EntityName: "Team", EntitySlug: "team", SchemaSlug: "page", Status: "skipped", SkipReason: "already_linked"}}}, nil
}
func (f *fakeLinking) Suggest(_ context.Context, id uuid.UUID) ([]linkingv2.Suggestion, error) {
	if _, ok := f.names[id]; !ok {
		return nil, linkingv2.ErrSourceNotFound
	}
	return f.sugg, nil
}
func (f *fakeLinking) Equity(context.Context) (*linkingv2.EquityReport, error) {
	return &linkingv2.EquityReport{LinksTotal: 9, UniqueSources: 4, Targets: []linkingv2.EquityTargetRow{
		{TargetEntityID: uuid.New(), TargetName: "Services", TargetSlug: "services", TargetSchema: "page", InboundTotal: 7, InboundBySourceSchema: map[string]int{"post": 5, "page": 2}, Tier: "primary"},
		{TargetEntityID: uuid.New(), TargetName: "Contact", TargetSlug: "contact", TargetSchema: "page", InboundTotal: 2},
	}}, nil
}
func (f *fakeLinking) Inbound(context.Context, uuid.UUID) ([]linkingv2.EquityDetailRow, error) {
	return []linkingv2.EquityDetailRow{{SourceName: "Post <1>", SourceSlug: "post-1", SourceSchema: "post", AnchorText: "our services", Href: "/services"}}, nil
}
func (f *fakeLinking) EntityName(_ context.Context, id uuid.UUID) string { return f.names[id] }
func (f *fakeLinking) SuggestIn(_ context.Context, id uuid.UUID, html string) ([]linkingv2.Suggestion, error) {
	if _, ok := f.names[id]; !ok {
		return nil, linkingv2.ErrSourceNotFound
	}
	f.liveIn = append(f.liveIn, html)
	return f.sugg, nil
}
func (f *fakeLinking) WrapIn(_ context.Context, t uuid.UUID, anchor, html string) (string, InsertOutcome, error) {
	f.insertIn = append(f.insertIn, [3]string{"live", t.String(), anchor})
	i := strings.Index(html, anchor)
	if i < 0 {
		return html, InsertOutcome{SkipReason: "no_match"}, nil
	}
	return html[:i] + `<a href="/t" data-entity-id="` + t.String() + `">` + anchor + "</a>" + html[i+len(anchor):], InsertOutcome{Inserted: true}, nil
}
func (f *fakeLinking) Insert(_ context.Context, s, t uuid.UUID, anchor string) (InsertOutcome, error) {
	f.insertIn = append(f.insertIn, [3]string{s.String(), t.String(), anchor})
	if len(f.inserted) == 0 {
		return InsertOutcome{Inserted: true}, nil
	}
	o := f.inserted[0]
	f.inserted = f.inserted[1:]
	return o, nil
}

func newLinkRig(t *testing.T) (*rig, *fakeLinking, uuid.UUID, uuid.UUID) {
	t.Helper()
	fl := newFakeLinking()
	target, source := uuid.New(), uuid.New()
	fl.names[target], fl.names[source] = "Services", "Home page"
	g := newRig(t, func(d *Deps) {
		d.Growth.Linking = fl
		d.Growth.Pickers = fakePickers{entities: []PickOption{{Value: target.String(), Label: "Services", Slug: "services", Hint: "/services"}, {Value: source.String(), Label: "Home page", Slug: "home", Hint: "/home"}}}
	})
	return g, fl, target, source
}

func TestLinkingRulesListAndFilters(t *testing.T) {
	t.Parallel()

	g, fl, target, _ := newLinkRig(t)
	fl.add("web design", target, "manual")
	fl.add("seo", target, "bulk_import")
	body := g.do("GET", "/admin/settings/linking", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"web design", "Bulk import", "Services", "Clean up rules", "Internal linking sections"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if !strings.Contains(body, "hm-viz--ink") {
		t.Error("priority bar is not ink")
	}
	if strings.Count(body, "Delete all rules") != 1 || !strings.Contains(body, `role="menuitem" class="hm-menu__item hm-menu__item--danger" href="/admin/settings/linking/rules/delete-all"`) {
		t.Error("Delete all rules belongs once, in the header overflow menu")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if b := g.do("GET", "/admin/settings/linking?source=bulk_import", "owner-token", nil, nil).Body.String(); strings.Contains(b, "web design") || !strings.Contains(b, "seo") {
		t.Error("source filter")
	}
	if b := g.do("GET", "/admin/settings/linking?q=nope", "owner-token", nil, nil).Body.String(); !strings.Contains(b, "No linking rules") {
		t.Error("search")
	}
}

func TestLinkingRuleCreateEditPreviewToggleDelete(t *testing.T) {
	t.Parallel()

	g, fl, target, _ := newLinkRig(t)
	if issues := a11y.Check(g.do("GET", "/admin/settings/linking/rules/new", "owner-token", nil, nil).Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("new a11y: %v", issues)
	}
	good := url.Values{"phrase": {"web design"}, "target": {target.String()}, "target_prev": {"Services"}, "priority": {"80"}, "maxlinks": {"2"}, "active": {"1"}, "anchor": {"our web design"}}
	for name, bad := range map[string]url.Values{
		"no phrase":     {"phrase": {""}, "target": {target.String()}, "priority": {"50"}, "maxlinks": {"1"}},
		"no target":     {"phrase": {"x"}, "priority": {"50"}, "maxlinks": {"1"}},
		"bad priority":  {"phrase": {"x"}, "target": {target.String()}, "priority": {"500"}, "maxlinks": {"1"}},
		"bad max links": {"phrase": {"x"}, "target": {target.String()}, "priority": {"5"}, "maxlinks": {"0"}},
	} {
		rec := g.do("POST", "/admin/settings/linking/rules", "owner-token", bad, nil)
		if rec.Code != 422 || len(fl.rules) != 0 {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
			t.Errorf("%s a11y: %v", name, issues)
		}
	}
	// preview shows matches (escaped) without saving
	pv := url.Values{}
	for k, v := range good {
		pv[k] = v
	}
	pv.Set("_op", "preview")
	rec := g.do("POST", "/admin/settings/linking/rules", "owner-token", pv, nil)
	if rec.Code != 200 || len(fl.rules) != 0 || !strings.Contains(rec.Body.String(), "Blog &lt;post&gt;") || !strings.Contains(rec.Body.String(), "Already link to the target") || !strings.Contains(rec.Body.String(), `formaction="/admin/settings/linking/rules#preview"`) || !strings.Contains(rec.Body.String(), `max="20"`) {
		t.Fatalf("preview: %d %v", rec.Code, len(fl.rules))
	}
	if b := rec.Body.String(); !strings.Contains(b, `id="preview"`) || strings.Index(b, `id="preview"`) > strings.Index(b, "hm-statgrid") {
		t.Errorf("the #preview anchor must come before the preview stats")
	}
	if reason := skipReason("already_linked"); strings.Contains(rec.Body.String(), "</span> <span class=\"hm-muted\">"+reason) {
		t.Errorf("preview repeats %q after the already-linked badge", reason)
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("preview a11y: %v", issues)
	}
	if rec := g.do("POST", "/admin/settings/linking/rules", "owner-token", good, nil); rec.Code != 303 || len(fl.rules) != 1 {
		t.Fatalf("create: %d", rec.Code)
	}
	if rec := g.do("POST", "/admin/settings/linking/rules", "owner-token", good, nil); rec.Code != 422 || !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("duplicate: %d", rec.Code)
	}
	var id uuid.UUID
	for k := range fl.rules {
		id = k
	}
	u := "/admin/settings/linking/rules/" + id.String()
	edit := g.do("GET", u, "owner-token", nil, nil)
	if edit.Code != 200 || !strings.Contains(edit.Body.String(), `value="web design"`) || !strings.Contains(edit.Body.String(), `value="Services"`) {
		t.Fatalf("edit: %d", edit.Code)
	}
	good.Set("phrase", "website design")
	good.Del("active")
	if rec := g.do("POST", u, "owner-token", good, nil); rec.Code != 303 || fl.rules[id].MatchValue != "website design" || fl.rules[id].IsActive {
		t.Fatalf("update: %d %+v", rec.Code, fl.rules[id])
	}
	g.do("POST", u+"/toggle", "owner-token", url.Values{}, nil)
	if !fl.rules[id].IsActive {
		t.Error("toggle")
	}
	if g.do("GET", u+"/delete", "owner-token", nil, nil).Code != 200 || len(fl.rules) != 1 {
		t.Fatal("confirm page must not delete")
	}
	if rec := g.do("POST", u+"/delete", "owner-token", url.Values{}, nil); rec.Code != 303 || len(fl.rules) != 0 {
		t.Fatalf("delete: %d", rec.Code)
	}
}

func TestLinkingRuleTargetByTypedSlugWithoutJS(t *testing.T) {
	t.Parallel()

	g, fl, _, _ := newLinkRig(t)
	rec := g.do("POST", "/admin/settings/linking/rules", "owner-token", url.Values{"phrase": {"consulting"}, "target_q": {"services"}, "priority": {"50"}, "maxlinks": {"1"}, "active": {"1"}}, nil)
	if rec.Code != 303 || len(fl.rules) != 1 {
		t.Fatalf("typed target should resolve: %d", rec.Code)
	}
}

func TestLinkingBulkAndCleanups(t *testing.T) {
	t.Parallel()

	g, fl, target, _ := newLinkRig(t)
	a, b := fl.add("a", target, "manual"), fl.add("b", target, "manual")
	fl.add("c", target, "auto_tag")
	rec := g.do("POST", "/admin/settings/linking/rules/bulk", "owner-token", url.Values{"action": {"delete"}, "id": {a.ID.String(), b.ID.String()}}, nil)
	if rec.Code != 200 || len(fl.rules) != 3 || !strings.Contains(rec.Body.String(), "Delete 2 rules?") {
		t.Fatalf("must ask first: %d", rec.Code)
	}
	rec = g.do("POST", "/admin/settings/linking/rules/bulk", "owner-token", url.Values{"action": {"delete"}, "confirm": {"1"}, "id": {a.ID.String(), b.ID.String()}}, nil)
	if rec.Code != 303 || len(fl.rules) != 1 {
		t.Fatalf("bulk delete: %d", rec.Code)
	}
	rec = g.do("POST", "/admin/settings/linking/rules/delete-source", "owner-token", url.Values{"source": {"auto_tag"}}, nil)
	if rec.Code != 200 || len(fl.rules) != 1 || !strings.Contains(rec.Body.String(), `name="confirm" value="1"`) || !strings.Contains(rec.Body.String(), `name="source" value="auto_tag"`) {
		t.Fatalf("by-source must ask first: %d", rec.Code)
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("source confirm a11y: %v", issues)
	}
	g.do("POST", "/admin/settings/linking/rules/delete-source", "owner-token", url.Values{"source": {"auto_tag"}, "confirm": {"1"}}, nil)
	if len(fl.rules) != 0 || len(fl.deleted) != 1 {
		t.Error("by-source delete")
	}
	if rec := g.do("POST", "/admin/settings/linking/rules/delete-source", "owner-token", url.Values{"source": {"'; drop table"}, "confirm": {"1"}}, nil); rec.Code != 303 {
		t.Errorf("unknown source: %d", rec.Code)
	}
	fl.add("z", target, "manual")
	if g.do("GET", "/admin/settings/linking/rules/delete-all", "owner-token", nil, nil).Code != 200 || len(fl.rules) != 1 {
		t.Fatal("delete-all confirmation page must not delete")
	}
	g.do("POST", "/admin/settings/linking/rules/delete-all", "owner-token", url.Values{}, nil)
	if len(fl.rules) != 0 {
		t.Error("delete all")
	}
}

func TestLinkingBulkImport(t *testing.T) {
	t.Parallel()

	g, fl, target, _ := newLinkRig(t)
	fl.add("seo", target, "manual")
	text := "# phrase, slug, priority\nweb design, services, 70\nseo, services\nmissing, nothing-here\nbroken line\n\n"
	rec := g.do("POST", "/admin/settings/linking/import", "owner-token", url.Values{"rules": {text}}, nil)
	body := rec.Body.String()
	if rec.Code != 200 || len(fl.rules) != 2 {
		t.Fatalf("import: %d rules=%d", rec.Code, len(fl.rules))
	}
	for _, want := range []string{"1 rule created, 1 already existed", "no page with the slug nothing-here", "expected phrase, page slug"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// The skipped lines are an error result of the POST: announced (role=alert), not a note.
	if !strings.Contains(body, `class="hm-flash hm-flash--error" role="alert">2 lines were skipped:`) {
		t.Error("skipped lines are not an announced error flash")
	}
	var imported *linkingv2.LinkRule
	for _, r := range fl.rules {
		if r.MatchValue == "web design" {
			imported = r
		}
	}
	if imported == nil || imported.Priority != 70 || imported.Source != "bulk_import" {
		t.Errorf("imported rule: %+v", imported)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if rec := g.do("POST", "/admin/settings/linking/import", "owner-token", url.Values{"rules": {""}}, nil); rec.Code != 422 {
		t.Errorf("empty import: %d", rec.Code)
	}
	if issues := a11y.Check(g.do("GET", "/admin/settings/linking/import", "owner-token", nil, nil).Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("form a11y: %v", issues)
	}
}

func TestLinkingSuggestionsAndInsert(t *testing.T) {
	t.Parallel()

	g, fl, target, source := newLinkRig(t)
	fl.sugg = []linkingv2.Suggestion{
		{TargetEntityID: target, TargetName: "Services", AnchorPhrase: "our <services>", URL: "/services", Status: "insertable"},
		{TargetEntityID: uuid.New(), TargetName: "About", AnchorPhrase: "about", URL: "/about", Status: "skipped", SkipReason: "in_heading"},
		{TargetEntityID: uuid.New(), TargetName: "Contact", AnchorPhrase: "contact", URL: "/contact", Status: "skipped", SkipReason: "already_linked"},
	}
	empty := g.do("GET", "/admin/settings/linking/suggestions", "owner-token", nil, nil).Body.String()
	if issues := a11y.Check(empty, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("empty a11y: %v", issues)
	}
	rec := g.do("GET", "/admin/settings/linking/suggestions?entity="+source.String()+"&entity_prev=Home+page", "owner-token", nil, nil)
	body := rec.Body.String()
	for _, want := range []string{"1 can be linked, 1 already linked, 1 skipped", "our &lt;services&gt;", "Insert link", "The phrase only appears in headings"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "already links to the page") {
		t.Error("an already linked row says it once, in its badge")
	}
	if !strings.Contains(body, "Already linked") || strings.Count(body, "Already links to the target") != 1 {
		t.Error("the bar counts already linked rows under their own segment, matching the badge")
	}
	if strings.Count(body, "Insert link") != 1 {
		t.Error("only the insertable row has an Insert button")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if rec := g.do("GET", "/admin/settings/linking/suggestions?entity=nonsense", "owner-token", nil, nil); rec.Code != 422 {
		t.Errorf("bad entity: %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/settings/linking/suggestions?entity="+uuid.NewString(), "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown entity: %d", rec.Code)
	}
	ins := url.Values{"source": {source.String()}, "target": {target.String()}, "anchor": {"our services"}, "label": {"Home page"}}
	flushed := 0
	g.a.deps.Invalidate = func(context.Context) { flushed++ }
	if rec := g.do("POST", "/admin/settings/linking/suggestions/insert", "owner-token", ins, nil); rec.Code != 303 || len(fl.insertIn) != 1 || fl.insertIn[0][2] != "our services" {
		t.Fatalf("insert: %d %v", rec.Code, fl.insertIn)
	}
	if flushed != 1 {
		t.Errorf("an inserted link changes saved content: the rendered pages must be flushed (flushes = %d)", flushed)
	}
	fl.inserted = []InsertOutcome{{SkipReason: "already_linked"}}
	rec = g.do("POST", "/admin/settings/linking/suggestions/insert", "owner-token", ins, nil)
	if rec.Code != 303 || flushed != 1 {
		t.Fatalf("skip insert: %d (flushes %d)", rec.Code, flushed)
	}
	// opened with only the id, the page's name fills the picker and the heading
	named := g.do("GET", "/admin/settings/linking/suggestions?entity="+source.String(), "owner-token", nil, nil).Body.String()
	if !strings.Contains(named, "Suggestions for Home page") {
		t.Error("the heading must name the page when only ?entity=<id> is given")
	}
	if rec := g.do("POST", "/admin/settings/linking/suggestions/insert", "owner-token", url.Values{"source": {"x"}}, nil); rec.Code != 400 {
		t.Errorf("bad insert: %d", rec.Code)
	}
}

func TestLinkingEquityAndInbound(t *testing.T) {
	t.Parallel()

	g, fl, target, _ := newLinkRig(t)
	body := g.do("GET", "/admin/settings/linking/equity", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Services", "Primary", "page 2, post 5", `hm-statcard__value">9</dd>`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, want := range []string{"hm-barlist", "hm-catbar", "hm-cellbar", "Most linked pages"} {
		if !strings.Contains(body, want) {
			t.Errorf("equity page missing %q", want)
		}
	}
	if strings.Contains(body, `class="hm-stats"`) {
		t.Error("equity page hand-writes hm-stats")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if b := g.do("GET", "/admin/settings/linking/equity?tier=primary", "owner-token", nil, nil).Body.String(); strings.Contains(b, "Contact (/") || !strings.Contains(b, "Primary (1)") {
		t.Error("tier filter, with counts from the unfiltered report")
	}
	if b := g.do("GET", "/admin/settings/linking/equity?q=contact", "owner-token", nil, nil).Body.String(); strings.Contains(b, "Services (/") || !strings.Contains(b, "Contact") {
		t.Error("search filter")
	}
	if b := g.do("GET", "/admin/settings/linking/equity?schema=post", "owner-token", nil, nil).Body.String(); !strings.Contains(b, "No internal links yet") {
		t.Error("page type filter")
	}
	rec := g.do("GET", "/admin/settings/linking/equity/"+target.String(), "owner-token", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Post &lt;1&gt;") || !strings.Contains(rec.Body.String(), "our services") {
		t.Fatalf("inbound: %d", rec.Code)
	}
	if issues := a11y.Check(rec.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("inbound a11y: %v", issues)
	}
	_ = fl
	inb := g.do("GET", "/admin/settings/linking/equity/"+target.String(), "owner-token", nil, nil).Body.String()
	if !strings.Contains(inb, "Back to link equity") || strings.Contains(inb, `hm-card__title" id="inb-title`) {
		t.Error("inbound page: back action in the header, no card title")
	}
	if rec := g.do("GET", "/admin/settings/linking/equity/not-a-uuid", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("bad id: %d", rec.Code)
	}
}

func TestLinkStatusBadge(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ status, reason, label, kind string }{
		{"insertable", "", "Can be linked", "success"},
		{"already_linked", "", "Already links to the target", "info"},
		{"skipped", "already_linked", "Already links to the target", "info"},
		{"skipped", "in_heading", "Skipped", "neutral"},
	} {
		if b := linkStatusBadge(c.status, c.reason); b.Label != c.label || b.Kind != c.kind {
			t.Errorf("%s/%s: %+v", c.status, c.reason, b)
		}
	}
}
