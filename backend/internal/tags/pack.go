package tags

import (
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"unicode"

	"hatamoto.dev/core/internal/jsonx"
)

// Limits for a tag pack declaration. Every one is a refusal, not a clamp.
const (
	maxPackHosts   = 8
	maxPatternLen  = 120
	maxNameLen     = 60
	maxIDLabelLen  = 40
	maxURLLen      = 512
	genericBootKey = "generic"
)

// packCategories mirrors consent.Categories ("necessary" is not a choice);
// a test keeps the two in step so this leaf package needs no consent import.
var packCategories = []string{"analytics", "marketing", "functional"}

var (
	hostLabel    = `[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?`
	exactHostRE  = regexp.MustCompile(`^` + hostLabel + `(?:\.` + hostLabel + `)+$`)
	wildHostRE   = regexp.MustCompile(`^\*\.` + hostLabel + `(?:\.` + hostLabel + `)+$`)
	stemRE       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)
	pluginNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`) // sdk.NamePattern
)

// Decl is the JSON a package carries in tags/<stem>.json. It holds data
// only: core builds the loader call and the noscript markup itself.
type Decl struct {
	Name string `json:"name"`
	ID   struct {
		Label   string `json:"label"`
		Pattern string `json:"pattern"`
	} `json:"id"`
	// Script is an https URL template; {id} may appear once.
	Script string `json:"script"`
	// Hosts are bare hostnames, exact or "*.a.b", that go into the page CSP.
	Hosts []string `json:"hosts"`
	// Websockets also allows wss:// connections to Hosts.
	Websockets bool   `json:"websockets"`
	Category   string `json:"category"`
	// Noscript is an optional https image URL template, shown only when no
	// visitor can be asked for consent.
	Noscript string `json:"noscript"`
}

// ParseDecl validates raw (the contents of tags/<stem>.json from plugin) and
// returns the vendor it declares. Errors name the broken rule only; the
// caller prefixes the file.
func ParseDecl(plugin, stem string, raw []byte) (Vendor, error) {
	if !pluginNameRE.MatchString(plugin) {
		return Vendor{}, errors.New("plugin name is not usable as a tag key prefix")
	}
	if !stemRE.MatchString(stem) {
		return Vendor{}, fmt.Errorf("file name %q must be lower-case letters, digits and dashes", stem)
	}
	var d Decl
	if err := jsonx.Unmarshal(raw, &d, jsonx.StrictNoUnknown); err != nil {
		return Vendor{}, fmt.Errorf("invalid JSON: %v", err)
	}

	if err := shortText("name", d.Name, maxNameLen); err != nil {
		return Vendor{}, err
	}
	if err := shortText("id.label", d.ID.Label, maxIDLabelLen); err != nil {
		return Vendor{}, err
	}
	pattern, err := compilePattern(d.ID.Pattern)
	if err != nil {
		return Vendor{}, err
	}
	if !validCategory(d.Category) {
		return Vendor{}, fmt.Errorf("category must be one of %s", strings.Join(packCategories, ", "))
	}
	if err := checkHosts(d.Hosts); err != nil {
		return Vendor{}, err
	}
	if d.Script == "" {
		return Vendor{}, errors.New("script is required")
	}
	if err := checkTemplateURL("script", d.Script, d.Hosts); err != nil {
		return Vendor{}, err
	}
	v := Vendor{
		ID:       plugin + "." + stem,
		Label:    d.Name,
		IDLabel:  d.ID.Label,
		Pattern:  pattern,
		Category: d.Category,
		Src:      d.Script,
		Boot:     genericBootKey,
		Help:     "Copy it from your " + d.Name + " account.",
	}
	for _, h := range d.Hosts {
		v.Script = append(v.Script, "https://"+h)
		v.Connect = append(v.Connect, "https://"+h)
	}
	if d.Websockets {
		for _, h := range d.Hosts {
			v.Connect = append(v.Connect, "wss://"+h)
		}
	}
	if d.Noscript != "" {
		if err := checkTemplateURL("noscript", d.Noscript, d.Hosts); err != nil {
			return Vendor{}, err
		}
		v.Noscript = `<img height="1" width="1" hidden alt="" src="` + html.EscapeString(d.Noscript) + `">`
	}
	return v, nil
}

func shortText(field, s string, max int) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len([]rune(s)) > max {
		return fmt.Errorf("%s is longer than %d characters", field, max)
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == '<' || r == '>' {
			return fmt.Errorf("%s holds a character that is not allowed", field)
		}
	}
	return nil
}

// compilePattern accepts an anchored RE2 expression that cannot match the
// empty string. The stored regexp is wrapped so an alternation such as
// "^a$|b" cannot escape the anchors.
func compilePattern(p string) (*regexp.Regexp, error) {
	if p == "" {
		return nil, errors.New("id.pattern is required")
	}
	if len(p) > maxPatternLen {
		return nil, fmt.Errorf("id.pattern is longer than %d characters", maxPatternLen)
	}
	if !strings.HasPrefix(p, "^") || !endsWithAnchor(p) {
		return nil, errors.New("id.pattern must start with ^ and end with $")
	}
	re, err := regexp.Compile("^(?:" + p + ")$")
	if err != nil {
		return nil, fmt.Errorf("id.pattern is not a valid expression: %v", err)
	}
	if re.MatchString("") {
		return nil, errors.New("id.pattern must not match an empty id")
	}
	return re, nil
}

// endsWithAnchor reports whether p ends in an unescaped "$".
func endsWithAnchor(p string) bool {
	if !strings.HasSuffix(p, "$") {
		return false
	}
	slashes := 0
	for i := len(p) - 2; i >= 0 && p[i] == '\\'; i-- {
		slashes++
	}
	return slashes%2 == 0
}

func validCategory(c string) bool {
	for _, k := range packCategories {
		if c == k {
			return true
		}
	}
	return false
}

func checkHosts(hosts []string) error {
	if len(hosts) == 0 {
		return errors.New("hosts needs at least one host")
	}
	if len(hosts) > maxPackHosts {
		return fmt.Errorf("hosts holds more than %d entries", maxPackHosts)
	}
	seen := map[string]bool{}
	for _, h := range hosts {
		if !exactHostRE.MatchString(h) && !wildHostRE.MatchString(h) {
			return fmt.Errorf("host %q must be a lower-case hostname or *.name.tld", h)
		}
		if seen[h] {
			return fmt.Errorf("host %q is listed twice", h)
		}
		seen[h] = true
	}
	return nil
}

// checkTemplateURL requires an https URL on a declared host with at most one
// {id} placeholder, which may not sit in the host.
func checkTemplateURL(field, raw string, hosts []string) error {
	if len(raw) > maxURLLen {
		return fmt.Errorf("%s is longer than %d characters", field, maxURLLen)
	}
	if strings.Count(raw, "{id}") > 1 {
		return fmt.Errorf("%s may hold {id} only once", field)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL", field)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%s must be an https URL", field)
	}
	if u.User != nil || u.Fragment != "" || u.Port() != "" || u.Opaque != "" {
		return fmt.Errorf("%s must not hold credentials, a port or a fragment", field)
	}
	if !hostDeclared(u.Hostname(), hosts) {
		return fmt.Errorf("%s host %q is not in hosts", field, u.Hostname())
	}
	return nil
}

// hostDeclared reports whether host is covered by one of hosts, where
// "*.a.b" covers every subdomain of a.b but not a.b itself.
func hostDeclared(host string, hosts []string) bool {
	for _, h := range hosts {
		if h == host {
			return true
		}
		if strings.HasPrefix(h, "*.") && strings.HasSuffix(host, h[1:]) && len(host) > len(h)-1 {
			return true
		}
	}
	return false
}

// PackVendor is a vendor contributed by an installed tag pack.
type PackVendor struct {
	Vendor
	PluginVersion string
}

var packs atomic.Pointer[[]PackVendor]

// SetPacks replaces the pack vendors in one swap. The caller passes the
// vendors of every installed, enabled, unblocked tag pack.
func SetPacks(v []PackVendor) {
	cp := append([]PackVendor(nil), v...)
	packs.Store(&cp)
}

// Packs returns a copy of the current pack vendors.
func Packs() []PackVendor {
	p := packs.Load()
	if p == nil {
		return nil
	}
	return append([]PackVendor(nil), (*p)...)
}

// All lists the built-in vendors, then the pack vendors, as a fresh slice.
func All() []Vendor {
	out := append([]Vendor(nil), Vendors...)
	if p := packs.Load(); p != nil {
		for _, pv := range *p {
			out = append(out, pv.Vendor)
		}
	}
	return out
}

// IsPackKey reports whether a settings key can only belong to a pack vendor.
// Built-in ids never contain a dot.
func IsPackKey(k string) bool { return strings.Contains(k, ".") }

// ValidHost reports whether h is a host a declaration may list: a lower-case
// hostname or "*.name.tld".
func ValidHost(h string) bool { return exactHostRE.MatchString(h) || wildHostRE.MatchString(h) }

// ValidCategory reports whether c is a consent category a tag may use.
func ValidCategory(c string) bool { return validCategory(c) }

// ValidName reports whether s is acceptable as a vendor display name.
func ValidName(s string) bool { return shortText("name", s, maxNameLen) == nil }

// MaxPackHosts is the most hosts one declaration may list.
const MaxPackHosts = maxPackHosts
