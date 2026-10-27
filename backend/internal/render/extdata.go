package render

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/builder"
)

// ExtRequest asks a plugin data source for one value or item list. ID is
// unique within a batch; Source is "ext.<plugin>.<name>"; Limit bounds the
// items of an items source.
type ExtRequest struct {
	ID, Source string
	Limit      int
}

// ExtBatch is every external-source request of one page render, sent in a
// single call so a render never costs more than one plugin round trip.
type ExtBatch struct {
	Path, Language, SchemaSlug, EntityID string
	Requests                             []ExtRequest
}

// ExtResult answers one ExtRequest: Value for a value source, Items for an
// items source. OK is false for a request nobody answered or that failed.
type ExtResult struct {
	Value string
	Items []RelatedItem
	OK    bool
}

// ExtSourceFunc resolves a batch and returns the results by request ID. A
// request that is absent or has OK false renders empty. Nil-safe: a nil func
// means no external data.
type ExtSourceFunc func(ctx context.Context, b ExtBatch) map[string]ExtResult

const (
	// extBudget bounds the whole external-data round trip of one batch.
	extBudget = 500 * time.Millisecond
	// maxExtCalls is the most batches one render may send: the page itself,
	// then once more for sources only its theme parts name.
	maxExtCalls = 2
	// maxExtRequests bounds the distinct requests of one render.
	maxExtRequests = 16
)

// extState is the external data of one render: every request already sent
// (answered or not) and the results by request ID.
type extState struct {
	asked map[string]bool
	res   map[string]ExtResult
	calls int
}

// askedCount is the number of requests already sent (nil-safe).
func (s *extState) askedCount() int {
	if s == nil {
		return 0
	}
	return len(s.asked)
}

func extValueKey(src string) string            { return "v:" + src }
func extItemsKey(src string, limit int) string { return "i:" + src + ":" + strconv.Itoa(limit) }

// extValue is the resolved text of a value source ("" when unknown or failed).
func (v *PageView) extValue(src string) string {
	if v.ext == nil {
		return ""
	}
	if r := v.ext.res[extValueKey(src)]; r.OK {
		return r.Value
	}
	return ""
}

// extItems is the item list of an items source (nil when unknown or failed).
func (v *PageView) extItems(src string, limit int) []RelatedItem {
	if v.ext == nil {
		return nil
	}
	if r := v.ext.res[extItemsKey(src, limit)]; r.OK {
		return r.Items
	}
	return nil
}

// extRegistered maps the registered source ids to their kind. Sources that
// are not registered (plugin disabled or not granted) are never requested and
// resolve empty without degrading the page.
func extRegistered() map[string]string {
	m := map[string]string{}
	for _, s := range builder.ExtSources() {
		m[s.ID()] = s.Kind
	}
	return m
}

// collectExt calls add for every external binding and every source loop in
// the trees, hidden subtrees skipped.
func collectExt(ns []*builder.Node, add func(kind, src string, limit int)) {
	for _, n := range ns {
		if n == nil || nodeHidden(n) {
			continue
		}
		for _, src := range n.B {
			if strings.HasPrefix(src, "ext.") {
				add(builder.ExtValue, src, 0)
			}
		}
		if n.T == "loop" && str(n.P, "from") == "source" {
			add(builder.ExtItems, str(n.P, "source"), intProp(n.P, "limit", loopDefaultLimit, 1, loopMaxLimit))
		}
		collectExt(n.C, add)
	}
}

// prefetchExt resolves, in one batched call, every external source the trees
// use that this render has not asked for yet. It sends at most
// caps: maxExtCalls batches per render and maxExtRequests distinct requests in
// total; a request past a cap is left unanswered and degrades the render, as
// does any request that comes back failed (so the page is not cached). A
// render without ExtSources, or without ext sources in the trees, calls nothing.
func (e *Engine) prefetchExt(v *PageView, ns ...*builder.Node) {
	if e.opts.ExtSources == nil || len(ns) == 0 {
		return
	}
	var reg map[string]string
	var batch ExtBatch
	var pending []string
	over := false
	collectExt(ns, func(kind, src string, limit int) {
		if reg == nil {
			reg = extRegistered()
		}
		if reg[src] != kind {
			return
		}
		key := extValueKey(src)
		if kind == builder.ExtItems {
			key = extItemsKey(src, limit)
		}
		if v.ext != nil && v.ext.asked[key] || slices.Contains(pending, key) {
			return
		}
		if len(pending)+v.ext.askedCount() >= maxExtRequests {
			over = true
			return
		}
		pending = append(pending, key)
		batch.Requests = append(batch.Requests, ExtRequest{ID: key, Source: src, Limit: limit})
	})
	ctx := requestCtx(v)
	if over {
		NoteDegradedCtx(ctx)
	}
	if len(pending) == 0 {
		return
	}
	if v.ext == nil {
		v.ext = &extState{asked: map[string]bool{}, res: map[string]ExtResult{}}
	}
	for _, k := range pending {
		v.ext.asked[k] = true
	}
	if v.ext.calls >= maxExtCalls {
		NoteDegradedCtx(ctx)
		return
	}
	v.ext.calls++
	batch.Path, batch.Language, batch.SchemaSlug, batch.EntityID = v.Path, v.Site.Language, v.SchemaSlug, v.EntityID
	cctx, cancel := context.WithTimeout(ctx, extBudget)
	defer cancel()
	got := e.opts.ExtSources(cctx, batch)
	for _, k := range pending {
		r, ok := got[k]
		if !ok || !r.OK {
			NoteDegradedCtx(ctx)
			continue
		}
		v.ext.res[k] = r
	}
}
