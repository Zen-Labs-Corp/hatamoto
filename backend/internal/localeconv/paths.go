package localeconv

import (
	"cmp"
	"log/slog"
	"slices"

	"hatamoto.dev/core/internal/entity"
)

// maxPathChangeItems caps the listed path changes; Count is always complete.
const maxPathChangeItems = 100

func entitySchemas(schemas []Schema) []*entity.EntitySchema {
	out := make([]*entity.EntitySchema, len(schemas))
	for i, s := range schemas {
		out[i] = &entity.EntitySchema{Slug: s.Slug, Name: s.Name, Settings: s.Settings}
	}
	return out
}

// pathOf is the public path of a row. It is entity.URLSnapshot.EntityPath
// with the attributes given explicitly, so stripped attributes count.
func pathOf(snap *entity.URLSnapshot, e *Ent, flat map[string]string, st State, ancestors []string) string {
	ent := &entity.Entity{Slug: st.Slug, Locale: st.Locale, CreatedAt: e.CreatedAt, PublishedAt: e.PublishedAt}
	loc, found, err := snap.Locate(e.Schema, flat, snap.VarsFor(ent, ancestors))
	if err != nil || !found {
		return "/" + st.Slug
	}
	return loc.Path
}

// checkPaths compares the public path of every published row before and after
// the conversion, both computed by the URL snapshot (no pattern code here).
// A region home is served at the bare locale path afterwards.
func (a *analysis) checkPaths(after []Schema) {
	quiet := slog.New(slog.DiscardHandler)
	beforeSnap, err := entity.BuildURLSnapshot(entitySchemas(a.snap.Schemas), quiet)
	if err != nil {
		a.errorf(CodePathChanges, "", "cannot compute the current URLs: %v", err)
		return
	}
	entries := make([]entity.LocaleEntry, len(a.plan.Locales))
	for i, l := range a.plan.Locales {
		entries[i] = entity.LocaleEntry{Tag: a.tags[i], Segment: l.Segment}
	}
	set, err := entity.NewLocaleSet(normalized(a.plan.Default), entries, a.plan.PrefixAllValue(), normalized(a.plan.XDefault))
	if err != nil {
		a.errorf(CodePathChanges, "", "cannot build the locale set: %v", err)
		return
	}
	afterBase, err := entity.BuildURLSnapshot(entitySchemas(after), quiet)
	if err != nil {
		a.errorf(CodePathChanges, "", "cannot compute the URLs after the conversion: %v", err)
		return
	}
	afterSnap := afterBase.WithLocaleSet(set)

	idx := make(map[string]*Planned, len(a.ch.Planned))
	for i := range a.ch.Planned {
		idx[a.ch.Planned[i].Ent.ID] = &a.ch.Planned[i]
	}
	beforeChain := func(e *Ent) []string {
		var chain []string
		for cur, d := a.byID[e.ParentID], 0; cur != nil && d < maxAncestorDepth; cur, d = a.byID[cur.ParentID], d+1 {
			chain = append(chain, cur.Slug)
		}
		slices.Reverse(chain)
		return chain
	}
	afterChain := func(p *Planned) []string {
		var chain []string
		for cur, d := idx[p.After.ParentID], 0; cur != nil && d < maxAncestorDepth; cur, d = idx[cur.After.ParentID], d+1 {
			chain = append(chain, cur.After.Slug)
		}
		slices.Reverse(chain)
		return chain
	}

	homes := map[string]*LocaleReport{}
	for i := range a.rep.Locales {
		homes[a.rep.Locales[i].Home.ID] = &a.rep.Locales[i]
	}
	var changes []PathChange
	for i := range a.ch.Planned {
		p := &a.ch.Planned[i]
		e := p.Ent
		flat := e.Flat()
		oldPath := pathOf(beforeSnap, e, flat, p.Before(), beforeChain(e))
		var newPath string
		if p.Home {
			newPath = afterSnap.WithLocale(p.After.Locale, "/")
		} else {
			afterFlat := flat
			if len(p.Strip) > 0 {
				afterFlat = make(map[string]string, len(flat))
				for k, v := range flat {
					if !slices.Contains(p.Strip, k) {
						afterFlat[k] = v
					}
				}
			}
			newPath = pathOf(afterSnap, e, afterFlat, p.After, afterChain(p))
		}
		if lr, ok := homes[e.ID]; ok && p.Home {
			lr.Home.OldPath, lr.Home.NewPath = oldPath, newPath
		}
		if !e.published() || beforeSnap.IsSectionPage(e.Schema, e.Slug) || oldPath == newPath {
			continue
		}
		changes = append(changes, PathChange{ID: e.ID, Old: oldPath, New: newPath})
	}
	slices.SortFunc(changes, func(x, y PathChange) int { return cmp.Compare(x.ID, y.ID) })
	a.rep.PathChanges.Count = len(changes)
	a.rep.PathChanges.Items = append([]PathChange{}, changes[:min(len(changes), maxPathChangeItems)]...)
	if len(changes) > 0 {
		first := changes[0]
		a.errorf(CodePathChanges, first.ID, "%d public URLs would change, first: %s becomes %s", len(changes), first.Old, first.New)
	}
}
