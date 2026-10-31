package localeconv

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// ErrNoRegionTree is returned by Suggest when the data has no recognisable
// region tree (two or more top-level pages whose attribute names their slug).
var ErrNoRegionTree = errors.New("no region tree found")

// SnapshotLoader reads the data Suggest and the analysis work on.
type SnapshotLoader interface {
	LoadSnapshot(ctx context.Context) (*Snapshot, error)
}

// SuggestFrom loads the data and proposes a plan. It writes nothing.
func SuggestFrom(ctx context.Context, l SnapshotLoader) (*Plan, []Issue, error) {
	snap, err := l.LoadSnapshot(ctx)
	if err != nil {
		return nil, nil, err
	}
	return Suggest(snap)
}

// tagAttributes are tried first, in this order, to read a root's language tag.
var tagAttributes = []string{"lang", "locale", "language"}

type candidate struct {
	ent  *Ent
	tag  string
	desc int // descendants
}

// Suggest proposes a plan from the region tree it finds (see the plan's
// --suggest algorithm). The plan validates except for locales whose tag could
// not be read: those have an empty tag and a warning, and the operator fills
// them in. It is pure and deterministic.
func Suggest(snap *Snapshot) (*Plan, []Issue, error) {
	byID := make(map[string]*Ent, len(snap.Entities))
	hasChild := map[string]bool{}
	for i := range snap.Entities {
		e := &snap.Entities[i]
		byID[e.ID] = e
		if e.ParentID != "" {
			hasChild[e.ParentID] = true
		}
	}

	var roots []*Ent
	for i := range snap.Entities {
		e := &snap.Entities[i]
		if e.Schema == defaultRootSchema && e.ParentID == "" && e.published() && hasChild[e.ID] && validSlug(e.Slug) && len(e.Slug) <= maxSegmentLen {
			roots = append(roots, e)
		}
	}
	slices.SortFunc(roots, func(a, b *Ent) int { return cmp.Compare(a.ID, b.ID) })

	attr, matched := localeAttribute(roots)
	if len(matched) < minLocales {
		return nil, nil, ErrNoRegionTree
	}

	var warnings []Issue
	cands := make([]*candidate, len(matched))
	rootIdx := map[string]int{}
	for i, e := range matched {
		cands[i] = &candidate{ent: e, tag: rootTag(e, attr)}
		rootIdx[e.ID] = i
		if cands[i].tag == "" {
			warnings = append(warnings, Issue{Code: CodeNoTag, EntityID: e.ID,
				Message: fmt.Sprintf("no language tag found on %q, fill in its tag by hand", e.Slug)})
		}
	}

	// localeOf is the candidate a row belongs to: its nearest root ancestor, else
	// the root its attribute names.
	bySlug := map[string]int{}
	for i, c := range cands {
		bySlug[c.ent.Slug] = i
	}
	localeOf := func(e *Ent) int {
		cur := e
		for d := 0; cur != nil && d < maxAncestorDepth; d++ {
			if i, ok := rootIdx[cur.ID]; ok {
				return i
			}
			cur = byID[cur.ParentID]
		}
		if i, ok := bySlug[e.Flat()[attr]]; ok {
			return i
		}
		return -1
	}
	for i := range snap.Entities {
		e := &snap.Entities[i]
		if li := localeOf(e); li >= 0 && cands[li].ent != e {
			cands[li].desc++
		}
	}

	groupAttr, firstGroup := findGroupAttribute(snap, attr, localeOf)
	order := orderCandidates(cands, firstGroup)

	plan := &Plan{
		Version:    planVersion,
		RootSchema: defaultRootSchema,
		PrefixAll:  new(true),
		LocaleFrom: LocaleFrom{Attribute: attr, Map: map[string]string{}, Fallback: FallbackRoot},
		Shared:     sharedLocale,
	}
	if groupAttr != "" {
		plan.GroupFrom = &GroupFrom{Attribute: groupAttr}
	}
	for _, c := range order {
		plan.Locales = append(plan.Locales, LocaleSpec{Tag: c.tag, Segment: c.ent.Slug, RootSlug: c.ent.Slug})
		if c.tag != "" {
			plan.LocaleFrom.Map[c.ent.Slug] = c.tag
		}
	}
	plan.Default = pickDefault(order, snap.Site.Language)
	plan.XDefault = plan.Default
	plan.applyDefaults()
	return plan, warnings, nil
}

// localeAttribute finds the top-level scalar attribute whose value equals the
// slug of the most roots. Ties go to the alphabetically first key.
func localeAttribute(roots []*Ent) (string, []*Ent) {
	counts := map[string]int{}
	for _, r := range roots {
		for k, v := range r.Flat() {
			if v == r.Slug {
				counts[k]++
			}
		}
	}
	best := ""
	for _, k := range sortedKeysOf(counts) {
		if best == "" || counts[k] > counts[best] {
			best = k
		}
	}
	var matched []*Ent
	for _, r := range roots {
		if r.Flat()[best] == r.Slug {
			matched = append(matched, r)
		}
	}
	return best, matched
}

func sortedKeysOf[V any](m map[string]V) []string { return sortedStrings(m) }

// rootTag is the first attribute of the root, in order lang, locale, language,
// then the others alphabetically, whose value is a language tag. The locale
// attribute itself is skipped: its value is the slug.
func rootTag(e *Ent, localeAttr string) string {
	flat := e.Flat()
	keys := slices.Clone(tagAttributes)
	for _, k := range sortedKeysOf(flat) {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		if k == localeAttr {
			continue
		}
		if tag, err := entity.NormalizeLocale(flat[k]); err == nil && flat[k] != "" {
			return tag
		}
	}
	return ""
}

// findGroupAttribute looks for the attribute whose value is a uuid shared by two
// or more rows in two or more candidate locales, covering the most rows. It also
// returns the members of the first such group (smallest value) for ordering.
func findGroupAttribute(snap *Snapshot, localeAttr string, localeOf func(*Ent) int) (string, []*Ent) {
	type group struct {
		rows    []*Ent
		locales map[int]bool
	}
	byAttr := map[string]map[string]*group{}
	for i := range snap.Entities {
		e := &snap.Entities[i]
		li := localeOf(e)
		if li < 0 {
			continue
		}
		for k, v := range e.Flat() {
			if k == localeAttr {
				continue
			}
			if _, err := uuid.Parse(v); err != nil {
				continue
			}
			if byAttr[k] == nil {
				byAttr[k] = map[string]*group{}
			}
			g := byAttr[k][v]
			if g == nil {
				g = &group{locales: map[int]bool{}}
				byAttr[k][v] = g
			}
			g.rows, g.locales[li] = append(g.rows, e), true
		}
	}
	best, bestCovered := "", 0
	var first []*Ent
	for _, k := range sortedKeysOf(byAttr) {
		covered := 0
		var firstRows []*Ent
		for _, v := range sortedKeysOf(byAttr[k]) {
			g := byAttr[k][v]
			if len(g.rows) >= 2 && len(g.locales) >= 2 {
				covered += len(g.rows)
				if firstRows == nil {
					firstRows = g.rows
				}
			}
		}
		if covered > bestCovered {
			best, bestCovered, first = k, covered, firstRows
		}
	}
	return best, first
}

// orderCandidates puts the locales in hreflang order: the order a stored list
// attribute of the first group gives, else root created_at then slug.
func orderCandidates(cands []*candidate, first []*Ent) []*candidate {
	if order := alternatesOrder(cands, first); order != nil {
		return order
	}
	out := slices.Clone(cands)
	slices.SortFunc(out, func(a, b *candidate) int {
		if c := a.ent.CreatedAt.Compare(b.ent.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ent.Slug, b.ent.Slug)
	})
	return out
}

// alternatesKeys are the object keys that may name a locale inside a list entry.
var alternatesKeys = []string{"lang", "locale", "tag", "hreflang", "segment", "slug"}

// alternatesOrder reads a top-level list attribute of the group's rows (strings, or
// objects naming a locale) and returns the candidates in its order when it names
// every one of them.
func alternatesOrder(cands []*candidate, members []*Ent) []*candidate {
	match := func(token string) int {
		token = strings.TrimSpace(token)
		for i, c := range cands {
			if token == c.ent.Slug || (c.tag != "" && strings.EqualFold(token, c.tag)) {
				return i
			}
		}
		return -1
	}
	for _, m := range members {
		var attrs map[string]json.RawMessage
		if json.Unmarshal(m.Attrs, &attrs) != nil {
			continue
		}
		for _, k := range sortedKeysOf(attrs) {
			var list []json.RawMessage
			if json.Unmarshal(attrs[k], &list) != nil {
				continue
			}
			var out []*candidate
			seen := map[int]bool{}
			for _, raw := range list {
				i := -1
				var s string
				var obj map[string]any
				switch {
				case json.Unmarshal(raw, &s) == nil:
					i = match(s)
				case json.Unmarshal(raw, &obj) == nil:
					for _, key := range alternatesKeys {
						if v, ok := obj[key].(string); ok {
							if i = match(v); i >= 0 {
								break
							}
						}
					}
				}
				if i >= 0 && !seen[i] {
					seen[i] = true
					out = append(out, cands[i])
				}
			}
			if len(out) == len(cands) {
				return out
			}
		}
	}
	return nil
}

// pickDefault is the candidate whose tag is the site language, else the one with
// the most descendants (the first in order on a tie).
func pickDefault(order []*candidate, language string) string {
	if lang, err := entity.NormalizeLocale(language); err == nil {
		for _, c := range order {
			if c.tag == lang {
				return c.tag
			}
		}
	}
	best := order[0]
	for _, c := range order[1:] {
		if c.desc > best.desc {
			best = c
		}
	}
	return best.tag
}
