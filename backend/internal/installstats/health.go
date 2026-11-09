package installstats

import (
	"sync"
	"sync/atomic"
)

// Health collects the Detailed level's health counts in memory. The hot paths
// only do an atomic add; the Service drains them into the database every few
// minutes and before a send. It holds names of plugins and numbers, never
// error text. Every method is safe on a nil *Health.
type Health struct {
	panics, http5xx, renderErrors, migrationFailed atomic.Int64

	mu     sync.Mutex
	plugin map[string]int64
}

// NewHealth returns an empty collector.
func NewHealth() *Health { return &Health{plugin: map[string]int64{}} }

// Panic counts one recovered request panic.
func (h *Health) Panic() {
	if h != nil {
		h.panics.Add(1)
	}
}

// HTTP5xx counts one response with a 5xx status.
func (h *Health) HTTP5xx() {
	if h != nil {
		h.http5xx.Add(1)
	}
}

// RenderError counts one failed public page render.
func (h *Health) RenderError() {
	if h != nil {
		h.renderErrors.Add(1)
	}
}

// MigrationFailed adds n failed migrations (read from the boot marker).
func (h *Health) MigrationFailed(n int64) {
	if h != nil && n > 0 {
		h.migrationFailed.Add(n)
	}
}

// PluginError counts one failed call of the named plugin. Past the map cap
// new names are counted under "other".
func (h *Health) PluginError(name string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.addPlugin(name, 1)
}

// addPlugin adds n to name; the caller holds mu.
func (h *Health) addPlugin(name string, n int64) {
	if h.plugin == nil {
		h.plugin = map[string]int64{}
	}
	if _, known := h.plugin[name]; !known && len(h.plugin) >= maxPluginErrorKeys {
		name = otherKey
	}
	h.plugin[name] += n
}

// Drain returns the counts collected so far and resets them to zero.
func (h *Health) Drain() Counts {
	if h == nil {
		return Counts{}
	}
	c := Counts{
		Panics:          h.panics.Swap(0),
		HTTP5xx:         h.http5xx.Swap(0),
		RenderErrors:    h.renderErrors.Swap(0),
		MigrationFailed: h.migrationFailed.Swap(0),
	}
	h.mu.Lock()
	c.PluginErrors, h.plugin = h.plugin, map[string]int64{}
	h.mu.Unlock()
	return c
}

// Restore adds c back, after a flush to the database failed.
func (h *Health) Restore(c Counts) {
	if h == nil {
		return
	}
	h.panics.Add(c.Panics)
	h.http5xx.Add(c.HTTP5xx)
	h.renderErrors.Add(c.RenderErrors)
	h.migrationFailed.Add(c.MigrationFailed)
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, n := range c.PluginErrors {
		h.addPlugin(name, n)
	}
}
