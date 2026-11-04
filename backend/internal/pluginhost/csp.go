package pluginhost

import (
	"slices"

	"hatamoto.dev/sdk"
)

// CSPSet is the union of the page-security hosts of the enabled plugins, per
// directive, sorted and without duplicates.
type CSPSet struct {
	Script, Connect, Img, Frame []string
}

// Empty reports whether no plugin adds a host.
func (s CSPSet) Empty() bool {
	return len(s.Script)+len(s.Connect)+len(s.Img)+len(s.Frame) == 0
}

// setCSP records (nil removes) the csp block of a plugin that is now live and
// publishes a fresh snapshot, so the render path reads without locking.
func (r *Runtime) setCSP(plugin string, c *sdk.CSP) {
	r.cspMu.Lock()
	defer r.cspMu.Unlock()
	next := map[string]sdk.CSP{}
	if cur := r.csp.Load(); cur != nil {
		for k, v := range *cur {
			next[k] = v
		}
	}
	if c.Empty() {
		delete(next, plugin)
	} else {
		next[plugin] = *c
	}
	r.csp.Store(&next)
}

// CSPHosts returns the hosts the loaded plugins add to the public pages'
// policy. A plugin for which imgOnly reports true (a community plugin)
// contributes its image hosts only, whatever its manifest says. Plugins that
// are off or uninstalled are not loaded and so contribute nothing.
func (r *Runtime) CSPHosts(imgOnly func(plugin string) bool) CSPSet {
	cur := r.csp.Load()
	var out CSPSet
	if cur == nil {
		return out
	}
	for name, c := range *cur {
		out.Img = append(out.Img, c.ImgSrc...)
		if imgOnly != nil && imgOnly(name) {
			continue
		}
		out.Script = append(out.Script, c.ScriptSrc...)
		out.Connect = append(out.Connect, c.ConnectSrc...)
		out.Frame = append(out.Frame, c.FrameSrc...)
	}
	for _, l := range []*[]string{&out.Script, &out.Connect, &out.Img, &out.Frame} {
		slices.Sort(*l)
		*l = slices.Compact(*l)
	}
	return out
}
