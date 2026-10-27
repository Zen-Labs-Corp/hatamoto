package builder

import (
	"fmt"
	"regexp"
	"sort"
	"sync/atomic"
)

// Source kinds of a plugin data source (sdk.BuilderSource).
const (
	ExtValue = "value" // one string for a prop binding
	ExtItems = "items" // a list for a loop with from:"source"
)

// ExtSource is one data source a plugin offers to the builder. Its id in docs
// is "ext.<Plugin>.<Name>". Class (text, url or image) is only set for value
// sources.
type ExtSource struct {
	Plugin, Name, Label, Kind, Class string
}

// ID is the source id used in node bindings and in a loop's source prop.
func (s ExtSource) ID() string { return "ext." + s.Plugin + "." + s.Name }

var extRe = regexp.MustCompile(`^ext\.([a-z][a-z0-9-]{1,39})\.([a-z][a-z0-9_-]{0,31})$`)

var extSources atomic.Pointer[[]ExtSource]

// SetExtSources replaces the registered plugin data sources (the packages /
// plugin layer calls it when the set of granted sources changes). Entries with
// a bad name, kind or class are dropped; the list is kept sorted by id.
func SetExtSources(ss []ExtSource) {
	out := make([]ExtSource, 0, len(ss))
	for _, s := range ss {
		if !extRe.MatchString(s.ID()) || (s.Kind != ExtValue && s.Kind != ExtItems) {
			continue
		}
		if s.Kind == ExtValue && s.Class == "" {
			s.Class = BindText // the manifest may omit it: plain text
		}
		if s.Kind == ExtValue && s.Class != BindText && s.Class != BindURL && s.Class != BindImage {
			continue
		}
		if s.Kind == ExtItems {
			s.Class = ""
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	extSources.Store(&out)
}

// ExtSources returns the registered plugin data sources (a fresh copy).
func ExtSources() []ExtSource {
	if p := extSources.Load(); p != nil {
		return append([]ExtSource(nil), *p...)
	}
	return nil
}

// ValidExtID reports whether id has the shape of an ext source id.
func ValidExtID(id string) bool { return extRe.MatchString(id) }

// extBindSources are the registered value sources as vocabulary entries,
// grouped by plugin name.
func extBindSources() []BindSource {
	var out []BindSource
	for _, s := range ExtSources() {
		if s.Kind == ExtValue {
			out = append(out, BindSource{Source: s.ID(), Label: s.Label, Group: s.Plugin, Class: s.Class})
		}
	}
	return out
}

// parseExtBind classifies an "ext.<plugin>.<name>" binding. A registered value
// source yields its class; a source that is not registered (plugin disabled or
// removed) is accepted as attribute-like so a saved doc stays valid and simply
// resolves empty. A registered items source cannot be bound to a prop.
func parseExtBind(src string) (class string, attr bool, err error) {
	if !extRe.MatchString(src) {
		return "", false, fmt.Errorf("binding %q: want ext.<plugin>.<name>", src)
	}
	for _, s := range ExtSources() {
		if s.ID() == src {
			if s.Kind != ExtValue {
				return "", false, fmt.Errorf("binding %q: an items source feeds a loop, not a prop", src)
			}
			return s.Class, false, nil
		}
	}
	return BindText, true, nil
}
