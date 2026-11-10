package packages

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/tags"
)

// Entry is one package a registry offers.
type Entry struct {
	Kind        Kind   `json:"kind"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	// URL of the .hmp, absolute or relative to the registry index.
	URL string `json:"url"`
	// SHA256 is the hex hash of the .hmp (checked after the download).
	SHA256      string   `json:"sha256"`
	Size        int64    `json:"size,omitzero"`
	Permissions []string `json:"permissions,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	// Author, Source (repository URL), Commit (the pinned source revision) and
	// Tier describe where a package came from. All are optional: servers that
	// predate them ignore the keys.
	// Title is an optional display name for the directory page.
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"`
	Source string `json:"source,omitempty"`
	Commit string `json:"commit,omitempty"`
	Tier   string `json:"tier,omitempty"`
	// SignerKey is the base64 ed25519 public key of the package's author. A
	// developer index carries it for every entry; hosts verify the package
	// against it. Optional: servers that predate it ignore the key.
	SignerKey string `json:"signer_key,omitempty"`
	// Screenshot is a registry-relative path to a theme preview image.
	Screenshot string `json:"screenshot,omitempty"`
	// About (plain text, paragraphs split by a single newline) and Features
	// feed the public detail page. Both are optional: servers that predate
	// them ignore the keys.
	About    string   `json:"about,omitempty"`
	Features []string `json:"features,omitempty"`
	// Category (one of Categories) and Featured feed the admin Browse page.
	// Both are optional: servers that predate them ignore the keys.
	Category string `json:"category,omitempty"`
	Featured bool   `json:"featured,omitzero"`
	// SiteType (one of SiteTypes) and Flags (any of ThemeFlags) feed the admin
	// theme Browse page. Both are optional: unknown values are dropped.
	SiteType string   `json:"site_type,omitempty"`
	Flags    []string `json:"flags,omitempty"`
	// Tags summarises the tag declarations of a tag pack, read from the
	// package by the index builder, so the directory and the install prompt
	// can show hosts and consent category before any download. Optional:
	// servers that predate it ignore the key.
	Tags []TagSummary `json:"tags,omitempty"`
}

// TagSummary is one tag of a tag pack as a registry lists it: the display
// name, the consent category and the bare hosts it loads from.
type TagSummary struct {
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Hosts    []string `json:"hosts"`
}

// maxEntryTags is the most tags a registry entry lists (a package holds at most four).
const maxEntryTags = 4

// cleanTags keeps the well-formed tag summaries of an entry. The index is
// displayed before download, so nothing here is trusted: bad entries and bad
// hosts are dropped, never repaired.
func cleanTags(in []TagSummary) []TagSummary {
	var out []TagSummary
	for _, t := range in {
		if len(out) == maxEntryTags {
			break
		}
		if !tags.ValidName(t.Name) || !tags.ValidCategory(t.Category) {
			continue
		}
		var hosts []string
		for _, h := range t.Hosts {
			if len(hosts) < tags.MaxPackHosts && tags.ValidHost(h) && !slices.Contains(hosts, h) {
				hosts = append(hosts, h)
			}
		}
		if len(hosts) > 0 {
			out = append(out, TagSummary{Name: t.Name, Category: t.Category, Hosts: hosts})
		}
	}
	return out
}

// UnmarshalJSON decodes an entry, dropping a site_type or flags value of the
// wrong JSON type (and tags of one) instead of failing the whole index.
func (e *Entry) UnmarshalJSON(data []byte) error {
	type plain Entry
	aux := struct {
		*plain
		SiteType jsontext.Value `json:"site_type"`
		Flags    jsontext.Value `json:"flags"`
		Tags     jsontext.Value `json:"tags"`
	}{plain: (*plain)(e)}
	if err := jsonx.UnmarshalPinned(data, &aux); err != nil {
		return err
	}
	e.SiteType, e.Flags = "", nil
	_ = jsonx.UnmarshalPinned(aux.SiteType, &e.SiteType)
	if jsonx.UnmarshalPinned(aux.Flags, &e.Flags) != nil {
		e.Flags = nil
	}
	e.Tags = nil
	if jsonx.UnmarshalPinned(aux.Tags, &e.Tags) != nil {
		e.Tags = nil
	}
	return nil
}

// Categories is the fixed list of package categories, exact case.
var Categories = []string{"SEO", "Analytics", "Marketing", "Forms", "Commerce", "Content", "Media", "Developer", "Security", "Integrations"}

// ValidCategory reports whether s is one of Categories.
func ValidCategory(s string) bool {
	for _, c := range Categories {
		if c == s {
			return true
		}
	}
	return false
}

// SiteTypes is the fixed list of theme site types.
var SiteTypes = []string{"blog", "business", "docs", "landing", "portfolio", "restaurant"}

// ThemeFlags is the fixed list of theme capability flags.
var ThemeFlags = []string{"dark", "nojs", "builder"}

// ValidSiteType reports whether s is one of SiteTypes.
func ValidSiteType(s string) bool { return slices.Contains(SiteTypes, s) }

// ValidThemeFlag reports whether s is one of ThemeFlags.
func ValidThemeFlag(s string) bool { return slices.Contains(ThemeFlags, s) }

// cleanFlags keeps the known flags of in once each, in order; nil when none.
func cleanFlags(in []string) []string {
	var out []string
	for _, f := range in {
		if ValidThemeFlag(f) && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// Caps of Entry.About and Entry.Features, shared by the index build
// (Meta.validate) and the client sanitiser (ParseIndex).
const (
	maxAbout      = 1500
	maxAboutParas = 10
	maxFeatures   = 8
	maxFeature    = 120
)

// hasControl reports whether s holds a control character other than keep.
func hasControl(s string, keep rune) bool {
	for _, r := range s {
		if unicode.IsControl(r) && r != keep {
			return true
		}
	}
	return false
}

// cleanAbout returns about, or "" when it is over the caps or holds a control
// character other than the paragraph newline.
func cleanAbout(about string) string {
	if utf8.RuneCountInString(about) > maxAbout || strings.Count(about, "\n")+1 > maxAboutParas || hasControl(about, '\n') {
		return ""
	}
	return about
}

// cleanFeatures keeps at most the first maxFeatures usable items.
func cleanFeatures(in []string) []string {
	var out []string
	for _, f := range in {
		if f == "" || utf8.RuneCountInString(f) > maxFeature || hasControl(f, -1) {
			continue
		}
		if out = append(out, f); len(out) == maxFeatures {
			break
		}
	}
	return out
}

// screenshotPathPattern is the only shape of Entry.Screenshot a client keeps,
// so a page built from the index can never link outside screenshots/.
var screenshotPathPattern = regexp.MustCompile(`^screenshots/[a-z][a-z0-9-]{1,39}\.(png|webp|jpg)$`)

// Package tiers an Entry may carry.
const (
	TierFirstParty = "first-party"
	TierCommunity  = "community"
	TierDeveloper  = "developer"
)

// Revocation withdraws trust from a signer key (SignerKey), or from a package
// by Name and Version, or, with no Version, by Name for every version. Reason
// is shown to the owner.
type Revocation struct {
	SignerKey string `json:"signer_key,omitempty"`
	Name      string `json:"name,omitempty"`
	Version   string `json:"version,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// maxRevocationReason caps the stored reason of a revocation.
const maxRevocationReason = 200

// cleanRevocations keeps the usable revocations: one needs a parsable key or a
// package name. The reason is trimmed, stripped of control characters and capped.
func cleanRevocations(in []Revocation) []Revocation {
	var out []Revocation
	for _, rv := range in {
		if _, err := ParsePublicKey(rv.SignerKey); err != nil {
			rv.SignerKey = ""
		}
		if !namePattern.MatchString(rv.Name) {
			rv.Name, rv.Version = "", ""
		}
		if rv.SignerKey == "" && rv.Name == "" {
			continue
		}
		rv.Version = strings.TrimSpace(rv.Version)
		reason := strings.Map(func(c rune) rune {
			if unicode.IsControl(c) {
				return -1
			}
			return c
		}, rv.Reason)
		reason = strings.TrimSpace(reason)
		if runes := []rune(reason); len(runes) > maxRevocationReason {
			reason = string(runes[:maxRevocationReason])
		}
		rv.Reason = reason
		out = append(out, rv)
	}
	return out
}

// Index is the payload of a registry.json envelope.
type Index struct {
	Version int `json:"version"`
	// IssuedAt and ExpiresAt (RFC 3339) bound how long a signed index is
	// believed: an old signed copy cannot be replayed once it has expired.
	IssuedAt  string  `json:"issued_at"`
	ExpiresAt string  `json:"expires_at"`
	Packages  []Entry `json:"packages"`
	// Revoked lists withdrawn signer keys and package versions. Only the
	// developer index is acted on.
	Revoked []Revocation `json:"revoked,omitempty"`
}

// Limits on a signed index's freshness.
const (
	// maxIndexLifetime caps expires_at - issued_at, so a signer cannot mint an
	// index that is valid for ever.
	maxIndexLifetime = 180 * 24 * time.Hour
	// indexClockSkew is how far in the future issued_at may be.
	indexClockSkew = 10 * time.Minute
)

// Registry index refusals.
var (
	ErrIndexExpired  = errors.New("registry index has expired")
	ErrIndexNotFresh = errors.New("registry index has no valid issued_at / expires_at")
	ErrInsecureURL   = errors.New("registry URLs must use https")
)

// ErrRegistryOff means no registry is configured.
var ErrRegistryOff = errors.New("no registry is configured")

// maxIndexBytes bounds a registry index download.
const maxIndexBytes = 4 << 20

// Registry reads a signed registry index and downloads the packages it lists.
type Registry struct {
	URL    string
	Client *http.Client
	Trust  TrustSet
	Limits Limits
	// AllowInsecure accepts http:// URLs (an explicit development or test
	// override).
	AllowInsecure bool
	// Now is the clock (tests); time.Now when nil.
	Now func() time.Time
}

func (r *Registry) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// redactURL drops credentials (user:pass@) so a URL can go into an error
// message; text that does not parse as a URL is reduced to a placeholder when it
// could hold credentials.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if (err != nil || u.Host == "") && strings.Contains(raw, "@") {
		return "(unparsable URL)"
	}
	if err != nil {
		return raw
	}
	u.User = nil
	return u.String()
}

func (r *Registry) checkScheme(u string) error {
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("registry: bad URL %q", redactURL(u))
	}
	if parsed.Scheme != "https" && !(r.AllowInsecure && parsed.Scheme == "http") {
		return fmt.Errorf("%w: %s", ErrInsecureURL, redactURL(u))
	}
	return nil
}

// checkFresh refuses an index that is expired, not yet valid, or has no
// validity window.
func (r *Registry) checkFresh(idx *Index) error {
	return CheckFresh("registry index", idx.IssuedAt, idx.ExpiresAt, r.now(), maxIndexLifetime)
}

// CheckFresh refuses a document whose RFC 3339 window is malformed, longer
// than maxLifetime, issued in the future (beyond a 10 minute skew) or already
// expired at now. what names the document in errors. Failures wrap
// ErrIndexNotFresh or ErrIndexExpired.
func CheckFresh(what, issuedAt, expiresAt string, now time.Time, maxLifetime time.Duration) error {
	issued, err1 := time.Parse(time.RFC3339, issuedAt)
	expires, err2 := time.Parse(time.RFC3339, expiresAt)
	if err1 != nil || err2 != nil || !expires.After(issued) || expires.Sub(issued) > maxLifetime {
		return fmt.Errorf("%s: %w (it must carry RFC 3339 times at most %d days apart)", what, ErrIndexNotFresh, int(maxLifetime.Hours()/24))
	}
	if issued.After(now.Add(indexClockSkew)) {
		return fmt.Errorf("%s: %w (issued %s, in the future)", what, ErrIndexNotFresh, issuedAt)
	}
	if !now.Before(expires) {
		return fmt.Errorf("%s: %w (at %s); the registry may be serving an old copy", what, ErrIndexExpired, expiresAt)
	}
	return nil
}

// Fetch downloads and verifies the index. An index that is unsigned, signed by
// an untrusted key or altered is refused; it is never shown.
func (r *Registry) Fetch(ctx context.Context) (*Index, error) {
	if r == nil || r.URL == "" || strings.EqualFold(r.URL, "off") {
		return nil, ErrRegistryOff
	}
	if err := r.checkScheme(r.URL); err != nil {
		return nil, err
	}
	body, err := r.get(ctx, r.URL, maxIndexBytes)
	if err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	return ParseIndex(body, r.Trust, r.now())
}

// ParseIndex verifies a signed registry.json document against trust, checks
// its version and freshness at now and drops entries that are malformed. An
// unsigned, tampered, expired or untrusted index is refused. An unknown tier is
// cleared, and so is an unknown category; the entry stays.
func ParseIndex(doc []byte, trust TrustSet, now time.Time) (*Index, error) {
	raw, err := trust.VerifyIndex(doc)
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := jsonx.UnmarshalPinned(raw, &idx); err != nil {
		return nil, fmt.Errorf("registry index: %w", err)
	}
	if idx.Version != 1 {
		return nil, fmt.Errorf("registry index: unsupported version %d", idx.Version)
	}
	if err := CheckFresh("registry index", idx.IssuedAt, idx.ExpiresAt, now, maxIndexLifetime); err != nil {
		return nil, err
	}
	valid := idx.Packages[:0]
	for _, e := range idx.Packages {
		if (e.Kind == KindPlugin || e.Kind == KindTheme) && namePattern.MatchString(e.Name) && len(e.SHA256) == 64 && e.URL != "" {
			if e.Tier != TierFirstParty && e.Tier != TierCommunity && e.Tier != TierDeveloper {
				e.Tier = ""
			}
			if _, err := ParsePublicKey(e.SignerKey); err != nil {
				e.SignerKey = ""
			}
			if e.Tier == TierDeveloper && e.SignerKey == "" {
				continue
			}
			if !screenshotPathPattern.MatchString(e.Screenshot) {
				e.Screenshot = ""
			}
			if e.Category != "" && !ValidCategory(e.Category) {
				e.Category = ""
			}
			if !ValidSiteType(e.SiteType) {
				e.SiteType = ""
			}
			e.Flags = cleanFlags(e.Flags)
			e.About, e.Features = cleanAbout(e.About), cleanFeatures(e.Features)
			if e.Kind == KindPlugin {
				e.Tags = cleanTags(e.Tags)
			} else {
				e.Tags = nil
			}
			valid = append(valid, e)
		}
	}
	idx.Packages = valid
	idx.Revoked = cleanRevocations(idx.Revoked)
	return &idx, nil
}

// FilterDeveloper returns the developer index dev reduced to what a host may
// offer: entries with a valid signer_key, forced to TierDeveloper, minus every
// kind and name that main lists (nil main: none) and every first-party plugin,
// so a developer can never shadow a main-registry or first-party package.
// Revocations are kept.
func FilterDeveloper(dev, main *Index) *Index {
	out := &Index{Version: dev.Version, IssuedAt: dev.IssuedAt, ExpiresAt: dev.ExpiresAt, Revoked: dev.Revoked}
	for _, e := range dev.Packages {
		if _, err := ParsePublicKey(e.SignerKey); err != nil {
			continue
		}
		if slices.Contains(FirstPartyPlugins, e.Name) {
			continue
		}
		if main != nil {
			if _, shadowed := main.Find(e.Kind, e.Name); shadowed {
				continue
			}
		}
		e.Tier = TierDeveloper
		out.Packages = append(out.Packages, e)
	}
	return out
}

// Find returns the entry for kind and name.
func (idx *Index) Find(kind Kind, name string) (Entry, bool) {
	for _, e := range idx.Packages {
		if e.Kind == kind && e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Download fetches the .hmp of e and checks its hash against the index. The
// package itself is verified (signature) by Manager.Install.
func (r *Registry) Download(ctx context.Context, e Entry) ([]byte, error) {
	base, err := url.Parse(r.URL)
	if err != nil {
		return nil, err
	}
	ref, err := url.Parse(e.URL)
	if err != nil {
		return nil, fmt.Errorf("registry: bad package URL %q", redactURL(e.URL))
	}
	target := base.ResolveReference(ref).String()
	if err := r.checkScheme(target); err != nil {
		return nil, err
	}
	data, err := r.get(ctx, target, r.Limits.Archive)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", e.Name, err)
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), e.SHA256) {
		return nil, fmt.Errorf("download %s: the file does not match the hash in the registry", e.Name)
	}
	return data, nil
}

// Fetch reads a package from a URL (`hatamoto package install <url>`), bounded.
func FetchURL(ctx context.Context, c *http.Client, u string, max int64) ([]byte, error) {
	r := &Registry{Client: c}
	return r.get(ctx, u, max)
}

func (r *Registry) get(ctx context.Context, u string, max int64) ([]byte, error) {
	c := r.Client
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", redactURL(u), resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", redactURL(u), max)
	}
	return b, nil
}

// semver is major.minor.patch with an optional pre-release (build metadata
// ignored). It exists to tell whether the registry has something newer.
type semver struct {
	major, minor, patch int
	pre                 string
}

func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(s, "v")
	s, build, hasBuild := strings.Cut(s, "+")
	if hasBuild && !validIdentifiers(build) {
		return semver{}, false
	}
	core, pre, hasPre := strings.Cut(s, "-")
	if hasPre && !validIdentifiers(pre) {
		return semver{}, false
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var n [3]int
	for i, p := range parts {
		if !allDigits(p) {
			return semver{}, false
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			return semver{}, false
		}
		n[i] = v
	}
	return semver{n[0], n[1], n[2], pre}, true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// validIdentifiers accepts dot-separated, non-empty [0-9A-Za-z-] identifiers.
func validIdentifiers(s string) bool {
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		for _, c := range id {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
				return false
			}
		}
	}
	return true
}

// comparePre orders two non-empty pre-release strings by semver section 11:
// identifiers left to right, numeric ones numerically and below alphanumeric
// ones, and a shorter list below a longer one that starts the same.
func comparePre(a, b string) int {
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(x) && i < len(y); i++ {
		if c := compareIdentifier(x[i], y[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(x), len(y))
}

func compareIdentifier(a, b string) int {
	an, bn := allDigits(a), allDigits(b)
	switch {
	case an && bn:
		a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
		if c := cmp.Compare(len(a), len(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	case an:
		return -1
	case bn:
		return 1
	}
	return strings.Compare(a, b)
}

// ValidSemver reports whether v parses as a version Newer understands
// ("v1.2.3", optional pre-release and build metadata).
func ValidSemver(v string) bool {
	_, ok := parseSemver(v)
	return ok
}

// CompareVersions orders two versions: -1, 0 or 1. Both must be ValidSemver;
// an unparsable version compares as equal to everything.
func CompareVersions(a, b string) int {
	x, ok1 := parseSemver(a)
	y, ok2 := parseSemver(b)
	if !ok1 || !ok2 {
		return 0
	}
	for _, c := range [][2]int{{x.major, y.major}, {x.minor, y.minor}, {x.patch, y.patch}} {
		if c[0] != c[1] {
			if c[0] > c[1] {
				return 1
			}
			return -1
		}
	}
	switch {
	case x.pre == y.pre:
		return 0
	case x.pre == "":
		return 1
	case y.pre == "":
		return -1
	}
	return comparePre(x.pre, y.pre)
}

// Newer reports whether version a is newer than b. Unparsable versions are
// never newer.
func Newer(a, b string) bool {
	return ValidSemver(a) && ValidSemver(b) && CompareVersions(a, b) > 0
}
