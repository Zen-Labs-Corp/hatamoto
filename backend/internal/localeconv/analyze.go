package localeconv

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/entity"
)

// maxAncestorDepth bounds the parent walk, like maxAncestorDepthSQL does for
// the public URL lookup.
const maxAncestorDepth = 16

// maxListedWarnings caps how many per-entity warnings of one kind are listed.
const maxListedWarnings = 20

// State is the part of an entity row the conversion rewrites.
type State struct {
	Locale   string
	Slug     string
	ParentID string
	Group    string // translation_group; new groups are keyed by Planned.GroupKey until apply
}

// Planned is one entity with its planned after-state.
type Planned struct {
	Ent *Ent
	// After is the row after the conversion. After.Group is the current group
	// until apply replaces it with the uuid of GroupKey.
	After State
	// GroupKey is the group_from value when the row joins a new translation
	// group, "" when its group is left alone.
	GroupKey string
	// Home is true for a region home (its slug becomes home.slug).
	Home bool
	// Renamed is true when the slug was changed to free it for another row.
	Renamed bool
	// Strip lists the attribute keys strip_attributes removes from this row.
	Strip []string
}

// Before is the state of the row as stored.
func (p *Planned) Before() State {
	return State{Locale: p.Ent.Locale, Slug: p.Ent.Slug, ParentID: p.Ent.ParentID, Group: p.Ent.Group}
}

// Changed reports whether apply writes the row.
func (p *Planned) Changed() bool {
	b := p.Before()
	return p.After.Locale != b.Locale || p.After.Slug != b.Slug || p.After.ParentID != b.ParentID ||
		p.GroupKey != "" || len(p.Strip) > 0
}

// Changes is the planned conversion of every entity, ordered by id.
type Changes struct {
	Planned []Planned
	// Schemas are the schemas whose overrides the conversion edits.
	Schemas []SchemaChange
}

// Rows returns the planned rows that apply writes.
func (c *Changes) Rows() []*Planned {
	var out []*Planned
	for i := range c.Planned {
		if c.Planned[i].Changed() {
			out = append(out, &c.Planned[i])
		}
	}
	return out
}

// Analyze plans the conversion of snap under plan and reports what it finds.
// It is pure and deterministic: the same inputs give the same output. When the
// plan fails validation, or a root cannot be found, Changes is empty.
func Analyze(snap *Snapshot, plan *Plan) (*Changes, *Report) {
	a := &analysis{snap: snap, plan: plan, rep: newReport(), ch: &Changes{}}
	a.run()
	return a.finish()
}

type analysis struct {
	snap *Snapshot
	plan *Plan
	rep  *Report
	ch   *Changes

	tags     []string          // normalised plan tags, plan order
	tagByVal map[string]string // locale_from.map with normalised tags
	roots    map[string]string // root slug -> normalised tag
	byID     map[string]*Ent
	unmatch  map[string]int
	warned   map[string]int
}

func (a *analysis) errorf(code, entityID, format string, args ...any) {
	a.rep.Errors = append(a.rep.Errors, Issue{Code: code, Message: fmt.Sprintf(format, args...), EntityID: entityID})
}

// warnf lists the first maxListedWarnings warnings of a code, then counts the rest.
func (a *analysis) warnf(code, entityID, format string, args ...any) {
	a.warned[code]++
	if a.warned[code] > maxListedWarnings {
		return
	}
	a.rep.Warnings = append(a.rep.Warnings, Issue{Code: code, Message: fmt.Sprintf(format, args...), EntityID: entityID})
}

func (a *analysis) run() {
	if a.snap.Site.TriggerGuardMissing {
		a.errorf(CodeTriggerGuardMissing, "", "the site predates the multi-locale migration: entity_locale_retag() has no two-locale guard, run migrate up first")
	}
	a.rep.Errors = append(a.rep.Errors, a.plan.Validate()...)
	if len(a.rep.Errors) > 0 {
		return
	}
	a.warned = map[string]int{}
	a.unmatch = map[string]int{}
	a.normalisePlan()
	if !a.findRoots() {
		return
	}

	ents := make([]*Ent, len(a.snap.Entities))
	a.byID = make(map[string]*Ent, len(ents))
	for i := range a.snap.Entities {
		ents[i] = &a.snap.Entities[i]
		a.byID[ents[i].ID] = ents[i]
	}
	slices.SortFunc(ents, func(x, y *Ent) int { return cmp.Compare(x.ID, y.ID) })

	homeIDs := map[string]string{} // root entity id -> tag
	for _, r := range a.rootEntities() {
		homeIDs[r.ID] = a.roots[r.Slug]
	}

	a.ch.Planned = make([]Planned, len(ents))
	for i, e := range ents {
		a.ch.Planned[i] = a.planEntity(e, homeIDs)
	}
	for _, line := range a.unmatchedValues() {
		a.rep.UnmatchedValues = append(a.rep.UnmatchedValues, line)
		a.warnf(CodeUnmatchedValue, "", "%d rows have %s = %q, which locale_from.map does not list", line.Count, line.Attribute, line.Value)
	}
	a.resolveCollisions()
	a.planGroups()
	a.warnShadows()
	a.countLocales()
	a.assemble()
	for code, n := range a.warned {
		if n > maxListedWarnings {
			a.rep.Warnings = append(a.rep.Warnings, Issue{Code: code, Message: fmt.Sprintf("%d more %s warnings not listed", n-maxListedWarnings, code)})
		}
	}
}

func (a *analysis) normalisePlan() {
	a.roots = map[string]string{}
	for _, l := range a.plan.Locales {
		tag := normalized(l.Tag)
		a.tags = append(a.tags, tag)
		a.roots[l.RootSlug] = tag
	}
	a.tagByVal = map[string]string{}
	for v, t := range a.plan.LocaleFrom.Map {
		a.tagByVal[v] = normalized(t)
	}
}

// rootEntities returns the entity of each plan locale's root, in plan order.
func (a *analysis) rootEntities() []*Ent {
	var out []*Ent
	for _, l := range a.plan.Locales {
		for i := range a.snap.Entities {
			e := &a.snap.Entities[i]
			if a.isRootCandidate(e, l.RootSlug) {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

func (a *analysis) isRootCandidate(e *Ent, slug string) bool {
	return e.Schema == a.plan.RootSchema && e.ParentID == "" && e.Slug == slug && e.published()
}

// findRoots checks the schema and that every root_slug names exactly one
// published top-level row. It reports false when the analysis cannot go on.
func (a *analysis) findRoots() bool {
	if !slices.ContainsFunc(a.snap.Schemas, func(s Schema) bool { return s.Slug == a.plan.RootSchema }) {
		a.errorf(CodeUnknownSchema, "", "root_schema %q is not a schema of this site", a.plan.RootSchema)
		return false
	}
	ok := true
	for _, l := range a.plan.Locales {
		var ids []string
		for i := range a.snap.Entities {
			if a.isRootCandidate(&a.snap.Entities[i], l.RootSlug) {
				ids = append(ids, a.snap.Entities[i].ID)
			}
		}
		slices.Sort(ids)
		switch {
		case len(ids) == 0:
			a.errorf(CodeRootNotFound, "", "no published top-level %s with slug %q", a.plan.RootSchema, l.RootSlug)
			ok = false
		case len(ids) > 1:
			a.errorf(CodeRootAmbiguous, ids[0], "%d published top-level %s rows have slug %q", len(ids), a.plan.RootSchema, l.RootSlug)
			ok = false
		}
	}
	return ok
}

// homeOf walks up from e (e included) to the nearest region root.
func (a *analysis) homeOf(e *Ent, homeIDs map[string]string) (rootID, tag string) {
	cur := e
	for depth := 0; cur != nil && depth < maxAncestorDepth; depth++ {
		if t, ok := homeIDs[cur.ID]; ok {
			return cur.ID, t
		}
		cur = a.byID[cur.ParentID]
	}
	return "", ""
}

func (a *analysis) planEntity(e *Ent, homeIDs map[string]string) Planned {
	p := Planned{Ent: e, After: State{Locale: e.Locale, Slug: e.Slug, ParentID: e.ParentID, Group: e.Group}}
	lf := a.plan.LocaleFrom
	val := e.Flat()[lf.Attribute]
	_, homeTag := a.homeOf(e, homeIDs)

	tag := ""
	if val != "" {
		if t, ok := a.tagByVal[val]; ok {
			tag = t
		} else {
			a.unmatch[val]++
		}
		if homeTag == "" {
			a.warnf(CodeAttributeWithoutHome, e.ID, "%s %q has %s = %q but no region home above it", e.Schema, e.Slug, lf.Attribute, val)
		}
	}
	if tag == "" && lf.Fallback == FallbackRoot {
		tag = homeTag
	}
	if tag == "" {
		tag = entity.LocaleShared
	}
	p.After.Locale = tag

	if _, isHome := homeIDs[e.ID]; isHome {
		p.Home = true
		p.After.Slug = a.plan.Home.Slug
	} else if parentTag, ok := homeIDs[e.ParentID]; ok && parentTag != "" {
		p.After.ParentID = ""
	}

	if len(a.plan.StripAttributes) > 0 {
		keys := topLevelKeys(e)
		for _, k := range a.plan.StripAttributes {
			if keys[k] {
				p.Strip = append(p.Strip, k)
			}
		}
		slices.Sort(p.Strip)
	}
	return p
}

func (a *analysis) unmatchedValues() []UnmatchedValue {
	var out []UnmatchedValue
	for v, n := range a.unmatch {
		out = append(out, UnmatchedValue{Attribute: a.plan.LocaleFrom.Attribute, Value: v, Count: n})
	}
	slices.SortFunc(out, func(x, y UnmatchedValue) int { return cmp.Compare(x.Value, y.Value) })
	return out
}

// uniqueKey is the unique index a row falls under: (schema, locale, slug), plus
// the parent for a nested schema.
func uniqueKey(e *Ent, st State) string {
	parent := "-"
	if e.Nested {
		parent = "n:" + st.ParentID
	}
	return strings.Join([]string{e.Schema, st.Locale, parent, st.Slug}, "\x00")
}

// resolveCollisions finds rows that would share a unique key after the
// conversion. The region home wins, then a published row. Non-published losers
// are renamed <slug>-old-<n> (or abort with home.on_conflict "abort"); a
// published loser blocks.
func (a *analysis) resolveCollisions() {
	ps := a.ch.Planned
	buckets := map[string][]int{}
	for i := range ps {
		k := uniqueKey(ps[i].Ent, ps[i].After)
		buckets[k] = append(buckets[k], i)
	}
	used := make(map[string]bool, len(buckets))
	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		used[k] = true
		if len(buckets[k]) > 1 {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)

	for _, k := range keys {
		idxs := buckets[k] // ascending, hence by id
		win := a.pickWinner(idxs)
		for _, i := range idxs {
			if i == win {
				continue
			}
			p := &ps[i]
			winner := &ps[win]
			if p.Ent.published() {
				a.errorf(CodePublishedCollision, p.Ent.ID, "published %s %q collides with %q (%s) in locale %s",
					p.Ent.Schema, p.Ent.Slug, winner.After.Slug, winner.Ent.ID, p.After.Locale)
				continue
			}
			if a.plan.Home.OnConflict == OnConflictAbort {
				a.errorf(CodeSlugCollision, p.Ent.ID, "%s %s %q collides with %q (%s) in locale %s and home.on_conflict is %q",
					p.Ent.Status, p.Ent.Schema, p.Ent.Slug, winner.After.Slug, winner.Ent.ID, p.After.Locale, OnConflictAbort)
				continue
			}
			old := p.After.Slug
			for n := 1; ; n++ {
				cand := fmt.Sprintf("%s-old-%d", old, n)
				st := p.After
				st.Slug = cand
				if ck := uniqueKey(p.Ent, st); !used[ck] {
					used[ck] = true
					p.After.Slug, p.Renamed = cand, true
					break
				}
			}
			a.rep.RenamedCollisions = append(a.rep.RenamedCollisions, RenamedRow{
				ID: p.Ent.ID, Schema: p.Ent.Schema, OldSlug: old, NewSlug: p.After.Slug,
				Reason: fmt.Sprintf("%s row collides under locale %s", p.Ent.Status, p.After.Locale),
			})
		}
	}
}

// pickWinner chooses which of the rows sharing a key keeps the slug.
func (a *analysis) pickWinner(idxs []int) int {
	ps := a.ch.Planned
	best := idxs[0]
	rank := func(i int) [3]int {
		p := &ps[i]
		r := [3]int{2, 2, 2}
		if p.Home {
			r[0] = 0
		}
		if p.Ent.published() {
			r[1] = 0
		}
		if !p.Changed() {
			r[2] = 0
		}
		return r
	}
	for _, i := range idxs[1:] {
		ri, rb := rank(i), rank(best)
		if c := slices.Compare(ri[:], rb[:]); c < 0 ||
			(c == 0 && ps[i].Ent.CreatedAt.Before(ps[best].Ent.CreatedAt)) {
			best = i
		}
	}
	return best
}

// planGroups links rows sharing a group_from value into one translation group.
// Shared rows (locale und) have no translations and are left out.
func (a *analysis) planGroups() {
	gf := a.plan.GroupFrom
	if gf == nil {
		return
	}
	byValue := map[string][]int{}
	for i := range a.ch.Planned {
		p := &a.ch.Planned[i]
		if v := p.Ent.Flat()[gf.Attribute]; v != "" && p.After.Locale != entity.LocaleShared {
			byValue[v] = append(byValue[v], i)
		}
	}
	values := make([]string, 0, len(byValue))
	for v := range byValue {
		values = append(values, v)
	}
	slices.Sort(values)
	for _, v := range values {
		idxs := byValue[v]
		if len(idxs) == 1 {
			a.rep.Groups.Singletons++
			continue
		}
		byLocale := map[string][]string{}
		for _, i := range idxs {
			p := &a.ch.Planned[i]
			byLocale[p.After.Locale] = append(byLocale[p.After.Locale], p.Ent.ID)
		}
		conflict := false
		for _, tag := range sortedStrings(byLocale) {
			if ids := byLocale[tag]; len(ids) > 1 {
				conflict = true
				a.rep.Groups.Conflicts = append(a.rep.Groups.Conflicts, GroupConflict{Value: v, Locale: tag, IDs: ids})
				a.errorf(CodeGroupTwoInLocale, ids[0], "%d rows share %s = %q in locale %s", len(ids), gf.Attribute, v, tag)
			}
		}
		if conflict {
			continue
		}
		a.rep.Groups.Formed++
		for _, i := range idxs {
			a.ch.Planned[i].GroupKey = v
		}
	}
}

func sortedStrings[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// warnShadows flags published rows, other than the new homes, that keep the
// home slug in a locale: the public home lookup finds a slug at any depth.
func (a *analysis) warnShadows() {
	for i := range a.ch.Planned {
		p := &a.ch.Planned[i]
		if p.Home || !p.Ent.published() || p.After.Slug != a.plan.Home.Slug || p.After.Locale == entity.LocaleShared {
			continue
		}
		a.warnf(CodeHomeChildrenShadow, p.Ent.ID, "published %s %q in locale %s has the home slug and may shadow the new home",
			p.Ent.Schema, p.Ent.Slug, p.After.Locale)
	}
}

// countLocales fills the per-locale and shared counts and the home ids.
func (a *analysis) countLocales() {
	byTag := map[string]*LocaleReport{}
	for i, l := range a.plan.Locales {
		a.rep.Locales = append(a.rep.Locales, LocaleReport{Tag: a.tags[i], Segment: l.Segment, CountsBySchema: map[string]int{}})
	}
	for i := range a.rep.Locales {
		byTag[a.rep.Locales[i].Tag] = &a.rep.Locales[i]
	}
	for i := range a.ch.Planned {
		p := &a.ch.Planned[i]
		if lr, ok := byTag[p.After.Locale]; ok {
			lr.CountsBySchema[p.Ent.Schema]++
			lr.Total++
			if p.Home {
				lr.Home.ID = p.Ent.ID
			}
			continue
		}
		a.rep.Shared.CountsBySchema[p.Ent.Schema]++
		a.rep.Shared.Total++
	}
}

func topLevelKeys(e *Ent) map[string]bool {
	var m map[string]json.RawMessage
	out := map[string]bool{}
	if len(e.Attrs) == 0 || json.Unmarshal(e.Attrs, &m) != nil {
		return out
	}
	for k := range m {
		out[k] = true
	}
	return out
}
