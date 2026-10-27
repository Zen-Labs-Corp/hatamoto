package builder

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The site design system: a closed set of design tokens (values the owner
// edits, names fixed) and named style presets (reusable StyleSpec maps that
// nodes reference by p.preset). It lives under the "builder" key of the site
// theme settings JSON; builder only parses, validates and compiles it.

// The layout slots a theme part can fill.
const (
	SlotHeader  = "header"
	SlotFooter  = "footer"
	SlotSingle  = "single"
	SlotArchive = "archive"
)

// PartSlots lists the layout slots in display order.
func PartSlots() []string {
	return []string{SlotHeader, SlotFooter, SlotSingle, SlotArchive}
}

// ValidPartSlot reports whether s names a layout slot.
func ValidPartSlot(s string) bool {
	for _, p := range PartSlots() {
		if p == s {
			return true
		}
	}
	return false
}

const (
	maxPresets     = 48
	maxPresetLabel = 60
)

var (
	colorTokenNames  = []string{"surface", "muted", "accent"}
	shadowTokenNames = []string{"sm", "md", "lg"}
	fsTokenNames     = []string{"xs", "sm", "md", "lg", "xl", "2xl", "3xl", "4xl"}

	shadowLen   = `(0|-?[0-9]{1,3}px)`
	shadowColor = `(#([0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})|rgba\([0-9]{1,3},[0-9]{1,3},[0-9]{1,3},(0|1|0?\.[0-9]{1,3})\))`
	shadowLayer = shadowLen + ` ` + shadowLen + ` ` + shadowLen + `( ` + shadowLen + `)? ` + shadowColor
	shadowRe    = regexp.MustCompile(`^` + shadowLayer + `(, ?` + shadowLayer + `)?$`)
)

// Tokens are the editable values of the design tokens. A missing entry means
// the built-in default.
type Tokens struct {
	Color  map[string]string `json:"color,omitempty"`
	Space  map[string]string `json:"space,omitempty"`
	FS     map[string]string `json:"fs,omitempty"`
	Shadow map[string]string `json:"shadow,omitempty"`
}

// Preset is a named, reusable StyleSpec style map (breakpoint -> key -> value).
type Preset struct {
	Label string                       `json:"label"`
	S     map[string]map[string]string `json:"s"`
}

// Design is the site design system.
type Design struct {
	Tokens  Tokens            `json:"tokens"`
	Presets map[string]Preset `json:"presets"`
	// Parts assigns a theme_part entity id to a layout slot (see PartSlots).
	Parts map[string]string `json:"parts"`
}

// tokenClass describes one token class: its CSS variable prefix, the closed
// names, the built-in defaults and the value check.
type tokenClass struct {
	name     string
	prefix   string // CSS custom property prefix, "--hm-" + prefix + name
	names    []string
	defaults map[string]string
	valid    func(string) bool
	field    func(*Tokens) *map[string]string
}

func spaceNames() []string {
	n := make([]string, len(spaceSteps))
	for i := range n {
		n[i] = strconv.Itoa(i)
	}
	return n
}

// tokenLenRe is lenRe with up to three decimals, which the built-in type scale needs (0.875rem).
var tokenLenRe = regexp.MustCompile(`^(0|[0-9]{1,4}(\.[0-9]{1,3})?(px|rem|em|%|vh|vw))$`)

func validLen(v string) bool { return tokenLenRe.MatchString(v) }

var tokenClasses = []tokenClass{
	{"color", "", colorTokenNames, map[string]string{"surface": "#fff", "muted": "#eef0f4", "accent": "#c8102e"},
		hexRe.MatchString, func(t *Tokens) *map[string]string { return &t.Color }},
	{"space", "space-", spaceNames(), spaceDefaults(), validLen, func(t *Tokens) *map[string]string { return &t.Space }},
	{"fs", "fs-", fsTokenNames, copyMap(styleTokens["fs"]), validLen, func(t *Tokens) *map[string]string { return &t.FS }},
	{"shadow", "shadow-", shadowTokenNames, copyMap(shadowDefaults), shadowRe.MatchString, func(t *Tokens) *map[string]string { return &t.Shadow }},
}

func spaceDefaults() map[string]string {
	m := make(map[string]string, len(spaceSteps))
	for i, v := range spaceSteps {
		m[strconv.Itoa(i)] = v
	}
	return m
}

// DefaultTokens returns the built-in token values (what the editor prefills).
func DefaultTokens() Tokens {
	var t Tokens
	for _, c := range tokenClasses {
		*c.field(&t) = copyMap(c.defaults)
	}
	return t
}

func (c tokenClass) knows(name string) bool {
	for _, n := range c.names {
		if n == name {
			return true
		}
	}
	return false
}

// Validate checks the design strictly (used on save): closed token names,
// valid values, valid presets.
func (d Design) Validate() error {
	var errs []error
	for _, c := range tokenClasses {
		for _, name := range sortedKeys(*c.field(&d.Tokens)) {
			v := (*c.field(&d.Tokens))[name]
			if !c.knows(name) {
				errs = append(errs, fmt.Errorf("unknown %s token %q", c.name, name))
			} else if !c.valid(v) {
				errs = append(errs, fmt.Errorf("bad value %q for %s token %s", v, c.name, name))
			}
		}
	}
	if len(d.Presets) > maxPresets {
		errs = append(errs, fmt.Errorf("at most %d presets", maxPresets))
	}
	for _, name := range sortedKeys(d.Presets) {
		if err := ValidatePreset(name, d.Presets[name]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ValidatePreset checks one preset: a legal name, a short label and at least
// one style entry, every one of them valid for the StyleSpec.
func ValidatePreset(name string, p Preset) error {
	if !ValidPresetName(name) {
		return fmt.Errorf("preset name %q: lowercase letters, digits and hyphens, starting with a letter, max 32", name)
	}
	if utf8.RuneCountInString(p.Label) > maxPresetLabel {
		return fmt.Errorf("preset %s: label longer than %d characters", name, maxPresetLabel)
	}
	n := 0
	for _, bp := range sKeys(p.S) {
		if _, _, ok := ParseSKey(bp); !ok {
			return fmt.Errorf("preset %s: unknown breakpoint %q", name, bp)
		}
		for _, k := range sortedKeys(p.S[bp]) {
			// A preset has no parent context, so parent-hover is refused.
			if err := checkStyle(bp, k, p.S[bp][k], false); err != nil {
				return fmt.Errorf("preset %s: %w", name, err)
			}
			n++
		}
	}
	if n == 0 {
		return fmt.Errorf("preset %s: no style set", name)
	}
	return nil
}

// ParseDesign reads the "builder" key of the theme settings JSON. It never
// fails closed on bad data: invalid tokens, presets and values are dropped and
// reported in the returned (joined) error, the valid rest is kept. Absent data
// is an empty design with a nil error.
func ParseDesign(themeExtra []byte) (Design, error) {
	var d Design
	var top map[string]json.RawMessage
	if len(themeExtra) == 0 || json.Unmarshal(themeExtra, &top) != nil || len(top["builder"]) == 0 {
		return d, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(top["builder"], &raw); err != nil {
		return d, fmt.Errorf("builder settings: %w", err)
	}
	var errs []error
	if tok, ok := raw["tokens"].(map[string]any); ok {
		for _, c := range tokenClasses {
			m, _ := tok[c.name].(map[string]any)
			for _, name := range sortedKeys(m) {
				v, _ := m[name].(string)
				if !c.knows(name) || !c.valid(v) {
					errs = append(errs, fmt.Errorf("dropped %s token %q", c.name, name))
					continue
				}
				f := c.field(&d.Tokens)
				if *f == nil {
					*f = map[string]string{}
				}
				(*f)[name] = v
			}
		}
	}
	pt, _ := raw["parts"].(map[string]any)
	for _, slot := range sortedKeys(pt) {
		id, _ := pt[slot].(string)
		if !ValidPartSlot(slot) || !uuidRe.MatchString(id) {
			errs = append(errs, fmt.Errorf("dropped part %q", slot))
			continue
		}
		if d.Parts == nil {
			d.Parts = map[string]string{}
		}
		d.Parts[slot] = id
	}
	pm, _ := raw["presets"].(map[string]any)
	for _, name := range sortedKeys(pm) {
		if len(d.Presets) >= maxPresets {
			errs = append(errs, fmt.Errorf("dropped preset %q: more than %d", name, maxPresets))
			continue
		}
		p, perrs := parsePreset(pm[name])
		errs = append(errs, perrs...)
		if err := ValidatePreset(name, p); err != nil {
			errs = append(errs, fmt.Errorf("dropped %w", err))
			continue
		}
		if d.Presets == nil {
			d.Presets = map[string]Preset{}
		}
		d.Presets[name] = p
	}
	return d, errors.Join(errs...)
}

// parsePreset keeps the valid string entries of a raw preset.
func parsePreset(v any) (Preset, []error) {
	var p Preset
	var errs []error
	m, _ := v.(map[string]any)
	p.Label, _ = m["label"].(string)
	if utf8.RuneCountInString(p.Label) > maxPresetLabel {
		p.Label = ""
	}
	s, _ := m["s"].(map[string]any)
	for _, bp := range sortedKeys(s) {
		vals, _ := s[bp].(map[string]any)
		for _, k := range sortedKeys(vals) {
			val, _ := vals[k].(string)
			if err := checkStyle(bp, k, val, false); err != nil {
				errs = append(errs, fmt.Errorf("dropped style %s.%s: %w", bp, k, err))
				continue
			}
			if p.S == nil {
				p.S = map[string]map[string]string{}
			}
			if p.S[bp] == nil {
				p.S[bp] = map[string]string{}
			}
			p.S[bp][k] = val
		}
	}
	return p, errs
}

// TokenCSS returns the ":root{--hm-...}" block of the tokens that differ from
// their defaults, sorted by property, or "" when none does. Invalid values and
// unknown names are skipped, so the output is safe in a <style> element.
func (d Design) TokenCSS() string {
	var decls []string
	for _, c := range tokenClasses {
		vals := *c.field(&d.Tokens)
		for _, name := range c.names {
			if v, ok := vals[name]; ok && v != c.defaults[name] && c.valid(v) {
				decls = append(decls, "--hm-"+c.prefix+name+":"+v)
			}
		}
	}
	if len(decls) == 0 {
		return ""
	}
	sort.Strings(decls) // plain string order: deterministic, which is all that matters
	return ":root{" + strings.Join(decls, ";") + ";}"
}

// PresetCSS compiles one preset into rules for .hm-p-<name>, per breakpoint
// with the same media queries as a node. An invalid name yields "".
func PresetCSS(name string, p Preset, o CSSOpts) string {
	if !ValidPresetName(name) {
		return ""
	}
	sel := ".hm-p-" + name
	// No parent context: parent-hover is refused in presets.
	return stylesCSSAt(sel, sel+sel, "", p.S, o)
}

// MergeInto sets the tokens and presets of d under the "builder" key of the
// theme settings JSON, keeping every other key (and any other key inside
// "builder"). Empty parts are removed.
func (d Design) MergeInto(themeExtra []byte) ([]byte, error) {
	return mergeBuilder(themeExtra, func(b map[string]json.RawMessage) error {
		if err := setPart(b, "tokens", d.Tokens, d.Tokens.empty()); err != nil {
			return err
		}
		return setPart(b, "presets", d.Presets, len(d.Presets) == 0)
	})
}

// MergeTokens replaces only builder.tokens, leaving presets (even ones that
// would not parse) and every other key as stored. Empty tokens remove the key.
func MergeTokens(themeExtra []byte, t Tokens) ([]byte, error) {
	return mergeBuilder(themeExtra, func(b map[string]json.RawMessage) error {
		return setPart(b, "tokens", t, t.empty())
	})
}

// MergeParts applies a slot -> part id patch to builder.parts, leaving the
// other slots, tokens, presets and every other key as stored. An empty id
// removes the slot. An unknown slot or a non-uuid id is refused.
func MergeParts(themeExtra []byte, parts map[string]string) ([]byte, error) {
	for slot, id := range parts {
		if !ValidPartSlot(slot) {
			return nil, fmt.Errorf("unknown part slot %q", slot)
		}
		if id != "" && !uuidRe.MatchString(id) {
			return nil, fmt.Errorf("bad part id for slot %s", slot)
		}
	}
	return mergeBuilder(themeExtra, func(b map[string]json.RawMessage) error {
		cur := map[string]string{}
		if len(b["parts"]) > 0 {
			if err := json.Unmarshal(b["parts"], &cur); err != nil {
				cur = map[string]string{} // unreadable assignment: start over
			}
		}
		for slot, id := range parts {
			if id == "" {
				delete(cur, slot)
			} else {
				cur[slot] = id
			}
		}
		return setPart(b, "parts", cur, len(cur) == 0)
	})
}

// ValidateToken checks one token value: class is color, space, fs or shadow.
func ValidateToken(class, name, value string) error {
	for _, c := range tokenClasses {
		if c.name != class {
			continue
		}
		if !c.knows(name) {
			return fmt.Errorf("unknown %s token %q", class, name)
		}
		if !c.valid(value) {
			return fmt.Errorf("bad value %q for %s token %s", value, class, name)
		}
		return nil
	}
	return fmt.Errorf("unknown token class %q", class)
}

// ErrPresetExists and ErrTooManyPresets are the refusals of AddPreset.
var (
	ErrPresetExists   = errors.New("a preset with this name exists")
	ErrTooManyPresets = fmt.Errorf("at most %d presets", maxPresets)
)

// ValidateStyleValue checks one style value of a preset against the StyleSpec.
func ValidateStyleValue(key, value string) error {
	_, err := styleDecl(key, value)
	return err
}

// AddPreset creates one preset under builder.presets and leaves everything
// else as stored. It validates p and refuses an existing name (ErrPresetExists,
// also for a stored one that no longer parses) and more than 48 presets.
func AddPreset(themeExtra []byte, name string, p Preset) ([]byte, error) {
	if err := ValidatePreset(name, p); err != nil {
		return nil, err
	}
	return mergeBuilder(themeExtra, func(b map[string]json.RawMessage) error {
		presets, err := rawPresets(b)
		if err != nil {
			return err
		}
		if _, ok := presets[name]; ok {
			return ErrPresetExists
		}
		if len(presets) >= maxPresets {
			return ErrTooManyPresets
		}
		return putPresets(b, presets, map[string]*Preset{name: &p})
	})
}

// MergePresets applies updates to builder.presets: a non-nil preset replaces or
// adds, a nil one deletes. Presets not named stay exactly as stored. Each one is
// validated and the total stays within 48.
func MergePresets(themeExtra []byte, updates map[string]*Preset) ([]byte, error) {
	for _, name := range sortedKeys(updates) {
		if p := updates[name]; p != nil {
			if err := ValidatePreset(name, *p); err != nil {
				return nil, err
			}
		}
	}
	return mergeBuilder(themeExtra, func(b map[string]json.RawMessage) error {
		presets, err := rawPresets(b)
		if err != nil {
			return err
		}
		return putPresets(b, presets, updates)
	})
}

func rawPresets(b map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	presets := map[string]json.RawMessage{}
	if len(b["presets"]) > 0 {
		if err := json.Unmarshal(b["presets"], &presets); err != nil {
			return nil, fmt.Errorf("builder presets: %w", err)
		}
	}
	return presets, nil
}

func putPresets(b, presets map[string]json.RawMessage, updates map[string]*Preset) error {
	for name, p := range updates {
		if p == nil {
			delete(presets, name)
			continue
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		presets[name] = raw
	}
	if len(presets) > maxPresets {
		return ErrTooManyPresets
	}
	return setPart(b, "presets", presets, len(presets) == 0)
}

func setPart(b map[string]json.RawMessage, key string, v any, empty bool) error {
	if empty {
		delete(b, key)
		return nil
	}
	raw, err := json.Marshal(v)
	b[key] = raw
	return err
}

// mergeBuilder applies edit to the "builder" object of the theme settings JSON
// and returns the whole document; the builder key is dropped when it ends empty.
func mergeBuilder(themeExtra []byte, edit func(b map[string]json.RawMessage) error) ([]byte, error) {
	top := map[string]json.RawMessage{}
	if len(themeExtra) > 0 && string(themeExtra) != "null" {
		if err := json.Unmarshal(themeExtra, &top); err != nil {
			return nil, fmt.Errorf("theme settings: %w", err)
		}
	}
	b := map[string]json.RawMessage{}
	if len(top["builder"]) > 0 {
		if err := json.Unmarshal(top["builder"], &b); err != nil {
			return nil, fmt.Errorf("builder settings: %w", err)
		}
	}
	if err := edit(b); err != nil {
		return nil, err
	}
	if len(b) == 0 {
		delete(top, "builder")
	} else {
		raw, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		top["builder"] = raw
	}
	return json.Marshal(top)
}

func (t Tokens) empty() bool {
	return len(t.Color)+len(t.Space)+len(t.FS)+len(t.Shadow) == 0
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
