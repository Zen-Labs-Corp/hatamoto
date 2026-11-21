package render

import (
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// pluginRangeBudget is the number of plugin-template range iterations one
// page render may spend. Plugin templates are trusted for markup but not for
// CPU: without the budget a range over a huge number or a func result that
// returns a huge slice would pin the render goroutine. Theme templates are
// not counted.
const pluginRangeBudget = 100000

// rangeBudgetFunc is the name of the compile-time template func that hands a
// plugin tree its budget handle. It is registered for the compiled set only,
// never in the plugin snapshot, so plugin source cannot call it.
const rangeBudgetFunc = "hmRangeBudget"

// rangeBudget is what one page render has left to spend across all plugin
// ranges. It is only touched by the render goroutine, so it needs no lock.
type rangeBudget struct {
	left  int64
	noted bool // NoteDegraded already called for this budget
	// marks is the breaker's bookkeeping of this render, by plugin|template.
	// It stays nil unless the breaker was involved, so a page without plugin
	// ranges, or on a healthy site, never allocates it.
	marks map[string]uint8
}

// Per-render breaker marks, see rangeBudget.marks.
const (
	markSkip     uint8 = 1 << iota // the breaker paused the template for this render
	markProbe                      // this render is the one trial after a pause
	markBreached                   // the template ran the budget dry: counted once per render
	markTripped                    // that breach tripped the breaker, and logged it
)

// rangeFunnels counts renders currently inside a funnel, process-wide. While
// it is zero a plugin range gets a standalone budget and nothing is stored.
var rangeFunnels atomic.Int64

// rangeBudgets holds the budget of each goroutine that is inside a funnel and
// has run a plugin range. Template funcs get no per-render context, so the
// binding is by goroutine id, like the degraded scope. A plugin cannot reach
// it: the funcs of the plugin snapshot never see this machinery.
var rangeBudgets struct {
	active atomic.Int64 // number of entries in byG
	byG    sync.Map     // goroutine id -> *rangeBudget
}

// rangeFunnel marks one render entry point (executeWith, RenderNode,
// RenderTop). It is returned by value so that entering and leaving a render
// costs only atomics, and a page without plugin ranges allocates nothing.
type rangeFunnel struct{ owner bool }

// enterRangeFunnel marks the start of a render. The funnel owns the budget
// binding unless the calling goroutine already has one (a nested render),
// in which case the nested render reuses it and leaves its release to the
// outer funnel. Callers must call exit, usually deferred.
func enterRangeFunnel() rangeFunnel {
	rangeFunnels.Add(1)
	if rangeBudgets.active.Load() == 0 {
		return rangeFunnel{owner: true}
	}
	_, bound := rangeBudgets.byG.Load(goroutineID())
	return rangeFunnel{owner: !bound}
}

// reset drops the budget bound to this render so that the next plugin range
// starts a fresh one. The trial render of plugin sections calls it so that it
// cannot use up the budget of the real page render.
//
// The breaker marks survive the reset, so the page render of a section sees the
// verdict of its trial render and a breach of both is counted once.
func (f rangeFunnel) reset() {
	if !f.owner {
		return
	}
	if b := dropRangeBudget(); b != nil && b.marks != nil {
		rangeBudgets.byG.Store(goroutineID(), &rangeBudget{left: pluginRangeBudget, marks: b.marks})
		rangeBudgets.active.Add(1)
	}
}

// exit leaves the render and releases the budget binding if this funnel owns
// it. A probe render that never ran the budget dry closes its breaker here.
func (f rangeFunnel) exit() {
	if f.owner {
		if b := dropRangeBudget(); b != nil && b.marks != nil {
			for k, m := range b.marks {
				if m&markProbe != 0 && m&markBreached == 0 {
					plugin, tmpl, _ := strings.Cut(k, "|")
					rangeBreaks.settle(plugin, tmpl)
				}
			}
		}
	}
	rangeFunnels.Add(-1)
}

// dropRangeBudget unbinds and returns the budget of the calling goroutine, nil
// if it has none.
func dropRangeBudget() *rangeBudget {
	if rangeBudgets.active.Load() == 0 {
		return nil
	}
	b, ok := rangeBudgets.byG.LoadAndDelete(goroutineID())
	if !ok {
		return nil
	}
	rangeBudgets.active.Add(-1)
	return b.(*rangeBudget)
}

// boundRangeBudget returns the budget of the calling goroutine's render,
// binding a new one on first use. Invariant: every execution of the compiled
// template set runs inside a funnel, so a stored budget is always dropped by
// its owner. Outside any funnel the budget is standalone and stored nowhere.
func boundRangeBudget() *rangeBudget {
	if rangeFunnels.Load() == 0 {
		return &rangeBudget{left: pluginRangeBudget}
	}
	id := goroutineID()
	if b, ok := rangeBudgets.byG.Load(id); ok {
		return b.(*rangeBudget)
	}
	b := &rangeBudget{left: pluginRangeBudget}
	rangeBudgets.byG.Store(id, b)
	rangeBudgets.active.Add(1)
	return b
}

// rangeHandle is what a plugin template holds in its budget variable: one per
// template execution, so the goroutine lookup runs once and not per iteration.
type rangeHandle struct {
	b      *rangeBudget
	log    *slog.Logger // nil: silent (sample render)
	plugin string
	tmpl   string
	warned bool
	skip   bool // the breaker paused this template for the render
	probe  bool // this render is the one trial after a pause
	fed    bool // the breach already reached the breaker
}

// newRangeHandle starts a plugin template execution. When the breaker has
// something open (or this render already involved it) it asks whether the
// template may run; a healthy site pays one atomic load.
func newRangeHandle(log *slog.Logger, plugin, tmpl string) *rangeHandle {
	h := &rangeHandle{b: boundRangeBudget(), log: log, plugin: plugin, tmpl: tmpl}
	if plugin != "" && (h.b.marks != nil || rangeBreaks.open.Load() != 0) {
		h.admit()
	}
	return h
}

// admit takes the verdict of this render when it has one (the trial render of a
// section and its page render must agree), else asks the breaker.
func (h *rangeHandle) admit() {
	k := breakKey(h.plugin, h.tmpl)
	m := h.b.marks[k]
	if m&(markSkip|markProbe) == 0 {
		if m&markBreached != 0 {
			return // it already ran in this render
		}
		switch rangeBreaks.admit(h.plugin, h.tmpl) {
		case verdictSkip:
			m |= markSkip
		case verdictProbe:
			m |= markProbe
		default:
			return
		}
		h.b.mark(k, m)
	}
	h.skip, h.probe = m&markSkip != 0, m&markProbe != 0
}

// mark sets the breaker marks of key, allocating the map on first use.
func (b *rangeBudget) mark(key string, m uint8) {
	if b.marks == nil {
		b.marks = map[string]uint8{}
	}
	b.marks[key] = m
}

// Run reports whether the template may run: the rewritten tree wraps its whole
// body in {{if $hm-budget.Run}}, so a paused template renders nothing. The
// first skip of a render marks it degraded, like the breach it replaces.
func (h *rangeHandle) Run() bool {
	if !h.skip {
		return true
	}
	if !h.b.noted {
		h.b.noted = true
		NoteDegraded()
	}
	return false
}

// standaloneRangeHandle is a silent handle with its own budget. A test helper:
// the sample render must not use it, it shares one budget across its handles.
func standaloneRangeHandle(plugin, tmpl string) *rangeHandle {
	return &rangeHandle{b: &rangeBudget{left: pluginRangeBudget}, plugin: plugin, tmpl: tmpl}
}

// Spend takes one iteration from the budget and reports whether it is spent:
// true means the range body must stop. It runs once per plugin iteration.
func (h *rangeHandle) Spend() bool {
	if h.b.left <= 0 {
		h.exhausted()
		return true
	}
	h.b.left--
	return false
}

func (h *rangeHandle) exhausted() {
	if h.log == nil {
		return
	}
	if !h.b.noted {
		h.b.noted = true
		NoteDegraded()
	}
	h.feedBreaker()
	if !h.warned {
		h.warned = true
		if rangeWarns.allow(h.plugin+"|"+h.tmpl, time.Now()) {
			h.log.Warn("plugin template range budget exceeded",
				"plugin", h.plugin, "template", h.tmpl, "budget", pluginRangeBudget)
		}
	}
}

// feedBreaker counts the breach once per render and template. The breach that
// trips the breaker logs the pause and replaces the per-minute warning.
func (h *rangeHandle) feedBreaker() {
	if h.fed || h.plugin == "" {
		return
	}
	h.fed = true
	k := breakKey(h.plugin, h.tmpl)
	m := h.b.marks[k]
	if m&markBreached != 0 {
		h.warned = h.warned || m&markTripped != 0 // the page render of a section tripped in its trial
		return
	}
	m |= markBreached
	if tripped, until := rangeBreaks.breach(h.plugin, h.tmpl, h.probe); tripped {
		m |= markTripped
		h.warned = true
		h.log.Warn("plugin template paused after repeated range budget breaches",
			"plugin", h.plugin, "template", h.tmpl, "until", until.UTC(),
			"breaches", rangeBreakLimit, "pause", rangeBreakPause)
	}
	h.b.mark(k, m)
}

const (
	rangeWarnWindow = time.Minute
	rangeWarnKeys   = 256
)

// warnLimiter lets one warning per key through per window, so a hot bad
// template cannot flood the log. The map is cleared when full: the cost is
// at worst a repeated warning, never unbounded memory.
type warnLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

var rangeWarns = &warnLimiter{last: map[string]time.Time{}}

func (l *warnLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.last[key]; ok && now.Sub(t) < rangeWarnWindow {
		return false
	}
	if _, ok := l.last[key]; !ok && len(l.last) >= rangeWarnKeys {
		clear(l.last)
	}
	l.last[key] = now
	return true
}
