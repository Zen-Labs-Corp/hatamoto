package render

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A plugin template that keeps running out of the range budget is paused for a
// while, so a hot bad template cannot cost every visitor the full budget.
const (
	rangeBreakLimit      = 3                // breaches within the window that trip the breaker
	rangeBreakWindow     = 10 * time.Minute // breaches older than this are forgotten
	rangeBreakPause      = 10 * time.Minute // how long a tripped template renders nothing
	rangeBreakProbeLease = time.Minute      // how long one probe render may take before another is granted
	rangeBreakKeys       = 256              // bound on the tracked templates
	rangeBreakStale      = 10 * time.Minute // an open key nobody probed this long after its pause is dropped
	rangeBreakSweep      = time.Minute      // at most one sweep for stale keys per this interval
)

// breakVerdict is what admit tells a template execution about to start.
type breakVerdict uint8

const (
	verdictRun   breakVerdict = iota // render normally
	verdictSkip                      // paused: render nothing
	verdictProbe                     // pause is over: this render is the one trial
)

// breakState is one template. It is closed while tripped is false (breaches
// holds the recent ones), open while tripped and probeAt is zero, and half
// open once a probe render was granted.
type breakState struct {
	plugin, tmpl string
	breaches     []time.Time
	tripped      bool
	until        time.Time
	probeAt      time.Time
}

// rangeBreaker pauses plugin templates by range budget breaches. The state is
// per process and in memory: a restart clears it.
type rangeBreaker struct {
	now  func() time.Time
	open atomic.Int64 // keys that are open or half open; 0 lets admit skip the lock
	mu   sync.Mutex
	keys map[string]*breakState

	nextSweep time.Time // earliest time of the next sweepLocked
}

// rangeBreaks is the process-wide breaker, like rangeWarns tests swap it.
var rangeBreaks = newRangeBreaker(time.Now)

func newRangeBreaker(now func() time.Time) *rangeBreaker {
	return &rangeBreaker{now: now, keys: map[string]*breakState{}}
}

func breakKey(plugin, tmpl string) string { return plugin + "|" + tmpl }

// admit decides whether a template execution runs. On a healthy site no key is
// open and it returns without taking the lock.
func (b *rangeBreaker) admit(plugin, tmpl string) breakVerdict {
	if b.open.Load() == 0 {
		return verdictRun
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.sweepLocked(now)
	st := b.keys[breakKey(plugin, tmpl)]
	if st == nil || !st.tripped {
		return verdictRun
	}
	if st.probeAt.IsZero() {
		if now.Before(st.until) {
			return verdictSkip
		}
	} else if now.Sub(st.probeAt) < rangeBreakProbeLease {
		return verdictSkip
	}
	st.probeAt = now
	return verdictProbe
}

// breach records a range budget breach and reports whether it tripped the
// breaker (until is then the end of the pause). probe says the breaching
// render was the granted probe: it re-opens the breaker at once. A breach of
// an ordinary render while the breaker is open or half open is ignored, it
// started before the trip.
func (b *rangeBreaker) breach(plugin, tmpl string, probe bool) (tripped bool, until time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	k := breakKey(plugin, tmpl)
	st := b.keys[k]
	if st != nil && st.tripped {
		if !probe || st.probeAt.IsZero() {
			return false, time.Time{}
		}
		st.until, st.probeAt = now.Add(rangeBreakPause), time.Time{}
		return true, st.until
	}
	if st == nil {
		if len(b.keys) >= rangeBreakKeys {
			b.pruneLocked(now)
		}
		if len(b.keys) >= rangeBreakKeys {
			return false, time.Time{} // full: fail open rather than grow
		}
		st = &breakState{plugin: plugin, tmpl: tmpl}
		b.keys[k] = st
	}
	st.breaches = append(slices.DeleteFunc(st.breaches, func(t time.Time) bool {
		return t.Before(now.Add(-rangeBreakWindow))
	}), now)
	if len(st.breaches) < rangeBreakLimit {
		return false, time.Time{}
	}
	st.breaches, st.tripped, st.until, st.probeAt = nil, true, now.Add(rangeBreakPause), time.Time{}
	b.open.Add(1)
	return true, st.until
}

// sweepLocked drops tripped keys that nobody tried again long after their pause
// (the template is no longer rendered, or the plugin was removed during a
// render), so that open falls back to zero and admit to its lock-free path. A
// dropped template simply runs again, like after a clean probe. It runs at most
// once per rangeBreakSweep and walks at most rangeBreakKeys keys.
func (b *rangeBreaker) sweepLocked(now time.Time) {
	if now.Before(b.nextSweep) {
		return
	}
	b.nextSweep = now.Add(rangeBreakSweep)
	for k, st := range b.keys {
		if !st.tripped {
			continue
		}
		last := st.until
		if !st.probeAt.IsZero() {
			last = st.probeAt.Add(rangeBreakProbeLease)
		}
		if now.Sub(last) > rangeBreakStale {
			delete(b.keys, k)
			b.open.Add(-1)
		}
	}
}

// pruneLocked drops closed keys whose breaches are all out of the window.
func (b *rangeBreaker) pruneLocked(now time.Time) {
	for k, st := range b.keys {
		if !st.tripped && !slices.ContainsFunc(st.breaches, func(t time.Time) bool {
			return !t.Before(now.Add(-rangeBreakWindow))
		}) {
			delete(b.keys, k)
		}
	}
}

// settle closes the breaker of a template whose probe render ran clean.
func (b *rangeBreaker) settle(plugin, tmpl string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := breakKey(plugin, tmpl)
	if st := b.keys[k]; st != nil && st.tripped && !st.probeAt.IsZero() {
		delete(b.keys, k)
		b.open.Add(-1)
	}
}

// forget drops everything known about a plugin (it was removed).
func (b *rangeBreaker) forget(plugin string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, st := range b.keys {
		if st.plugin != plugin {
			continue
		}
		if st.tripped {
			b.open.Add(-1)
		}
		delete(b.keys, k)
	}
}

// PausedTemplate is a plugin template the range breaker skips for now.
type PausedTemplate struct {
	Kind  string    // "section", "partial" or "template"
	Name  string    // section type, partial name or template name
	Until time.Time // when it is tried again, UTC
}

// PluginTemplatesPaused lists the templates of the plugin the range breaker
// currently skips, sorted by kind and name. Nil when there are none. It is
// read-only and meant for the admin and the API.
func PluginTemplatesPaused(plugin string) []PausedTemplate {
	return rangeBreaks.paused(plugin)
}

func (b *rangeBreaker) paused(plugin string) []PausedTemplate {
	if b.open.Load() == 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.sweepLocked(now)
	var out []PausedTemplate
	for _, st := range b.keys {
		if st.plugin != plugin || !st.tripped || !st.probeAt.IsZero() || !st.until.After(now) {
			continue
		}
		kind, name := pausedLabel(st.tmpl)
		out = append(out, PausedTemplate{Kind: kind, Name: name, Until: st.until.UTC()})
	}
	slices.SortFunc(out, func(a, c PausedTemplate) int {
		if d := strings.Compare(a.Kind, c.Kind); d != 0 {
			return d
		}
		return strings.Compare(a.Name, c.Name)
	})
	return out
}

// pausedLabel names a template tree for people: section-<typ> is a section,
// plugin/<ident>/<name> a partial, anything else a template defined by the
// plugin source.
func pausedLabel(tree string) (kind, name string) {
	if typ, ok := strings.CutPrefix(tree, "section-"); ok {
		return "section", typ
	}
	if rest, ok := strings.CutPrefix(tree, "plugin/"); ok {
		if _, n, ok := strings.Cut(rest, "/"); ok {
			return "partial", n
		}
	}
	return "template", tree
}
