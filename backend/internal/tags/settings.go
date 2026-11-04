package tags

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// VendorState is the owner's choice for one vendor.
type VendorState struct {
	Enabled bool   `json:"enabled"`
	ID      string `json:"id"`
	Aux     string `json:"aux,omitempty"`
}

// Settings is the owner's tracking configuration.
type Settings struct {
	ExcludeSignedIn bool                   `json:"exclude_signed_in"`
	Vendors         map[string]VendorState `json:"vendors"`
	// MovedFrom lists the plugins whose settings were folded into this page.
	MovedFrom       []string `json:"moved_from,omitempty"`
	NoticeDismissed bool     `json:"notice_dismissed,omitempty"`
}

// FieldError names a field ("<vendor>" or "<vendor>.aux") and what is wrong.
type FieldError struct {
	Field   string
	Message string
}

// EnabledVendor is an enabled vendor with its normalized ids.
type EnabledVendor struct {
	Vendor
	ValueID string
	AuxID   string
}

// Defaults is what a site gets before anyone saves the page: nothing on,
// tags skipped for signed-in users.
func Defaults() Settings {
	return Settings{ExcludeSignedIn: true, Vendors: map[string]VendorState{}}
}

// Decode parses a stored document. Missing keys keep their defaults.
func Decode(raw []byte) (Settings, error) {
	s := Defaults()
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return Defaults(), fmt.Errorf("decode tracking settings: %w", err)
	}
	if s.Vendors == nil {
		s.Vendors = map[string]VendorState{}
	}
	return s, nil
}

// Normalize returns a copy with every known vendor's ids trimmed and, where
// the vendor is case-insensitive, upper-cased. Unknown keys are kept. A pack
// key whose pack is not live (off, blocked, unreadable, not yet reconciled)
// keeps its state untouched; only uninstalling the pack removes it (see
// Store.DropPack).
func (s Settings) Normalize() Settings {
	out := s
	out.Vendors = make(map[string]VendorState, len(s.Vendors))
	for k, st := range s.Vendors {
		if v, ok := ByID(k); ok {
			st.ID = v.NormalizeID(st.ID)
			st.Aux = strings.TrimSpace(st.Aux)
		}
		out.Vendors[k] = st
	}
	return out
}

// Validate checks every vendor entry and returns one error per problem, in
// table order. An enabled vendor needs an id; any id present must match.
func (s Settings) Validate() []FieldError {
	n := s.Normalize()
	var errs []FieldError
	for _, v := range All() {
		st, ok := n.Vendors[v.ID]
		if !ok {
			continue
		}
		switch {
		case st.ID == "" && st.Enabled:
			errs = append(errs, FieldError{v.ID, "Enter the " + strings.ToLower(v.IDLabel) + " to turn this tag on."})
		case st.ID != "" && !v.ValidID(st.ID):
			errs = append(errs, FieldError{v.ID, "This is not a valid " + strings.ToLower(v.IDLabel) + ". " + v.Help})
		}
		if st.Aux != "" {
			if v.Aux == nil {
				errs = append(errs, FieldError{v.ID + ".aux", "This tag has no second field."})
			} else if !v.Aux.Pattern.MatchString(st.Aux) {
				errs = append(errs, FieldError{v.ID + ".aux", "This is not a valid " + strings.ToLower(v.Aux.Label) + "."})
			}
		}
	}
	var unknown []string
	for k := range n.Vendors {
		if _, ok := ByID(k); !ok && !IsPackKey(k) {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		errs = append(errs, FieldError{k, "Unknown tag."})
	}
	return errs
}

// Enabled lists the enabled vendors in table order with normalized ids.
// Entries without an id, or with one the vendor's pattern refuses (a stored id
// is never trusted over the live declaration), are skipped.
func (s Settings) Enabled() []EnabledVendor {
	n := s.Normalize()
	var out []EnabledVendor
	for _, v := range All() {
		st := n.Vendors[v.ID]
		if st.Enabled && st.ID != "" && v.ValidID(st.ID) {
			out = append(out, EnabledVendor{Vendor: v, ValueID: st.ID, AuxID: st.Aux})
		}
	}
	return out
}

// Hosts merges the CSP hosts of every enabled vendor, de-duplicated and sorted.
func (s Settings) Hosts() Hosts {
	var script, connect, style, font []string
	for _, e := range s.Enabled() {
		script = append(script, e.Script...)
		connect = append(connect, e.Connect...)
		style = append(style, e.Style...)
		font = append(font, e.Font...)
	}
	return Hosts{Script: uniq(script), Connect: uniq(connect), Style: uniq(style), Font: uniq(font)}
}

func uniq(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:1]
	for _, x := range in[1:] {
		if x != out[len(out)-1] {
			out = append(out, x)
		}
	}
	return out
}
