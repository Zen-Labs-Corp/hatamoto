package entity

import (
	"math"
	"net/url"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/jsonx"
)

// MaxArchiveLimit caps settings.archive_limit. It mirrors the render package's
// MaxQueryLimit (entity cannot import render).
const MaxArchiveLimit = 100

// ArchiveLimitSetting is settings.archive_limit as stored: how many entries the
// archive page lists, with no pagination. It decodes leniently: anything but a
// whole number reads as 0 (off), so a stale or hand-edited value can never
// make URLSettings fail and reset the schema's URL routing.
type ArchiveLimitSetting int

// UnmarshalJSON implements json.Unmarshaler.
func (l *ArchiveLimitSetting) UnmarshalJSON(b []byte) error {
	var n int
	if jsonx.Unmarshal(b, &n, jsonx.StoredLoose) != nil || n < 0 {
		n = 0
	}
	*l = ArchiveLimitSetting(n)
	return nil
}

// ArchiveGroupsSettings is settings.archive_groups: the archive splits into one
// page per numeric bucket floor(attribute/Divisor). Group pages live at
// ArchivePath + "/" + Path (with {n} replaced by the bucket number). It is
// off unless Enabled reports true.
//
// Label, Title and Description accept the placeholders {n}, {label} (Title and
// Description only) and {archive_title} (Title and Description only).
type ArchiveGroupsSettings struct {
	Attribute       string `json:"attribute,omitempty"`
	Divisor         int64  `json:"divisor,omitempty"`
	Path            string `json:"path,omitempty"`
	Label           string `json:"label,omitempty"`
	Title           string `json:"title,omitempty"`
	Description     string `json:"description,omitempty"`
	AnchorAttribute string `json:"anchor_attribute,omitempty"`
	RedirectEntries bool   `json:"redirect_entries,omitempty"`
}

// archiveGroupsRaw has the same fields without the lenient decoder; the write
// check decodes into it to see type errors.
type archiveGroupsRaw ArchiveGroupsSettings

// UnmarshalJSON implements json.Unmarshaler. It never fails: a malformed value
// (wrong types, not an object) reads as the zero, disabled, settings.
func (g *ArchiveGroupsSettings) UnmarshalJSON(b []byte) error {
	var raw archiveGroupsRaw
	if jsonx.Unmarshal(b, &raw, jsonx.StoredLoose) != nil {
		raw = archiveGroupsRaw{}
	}
	*g = ArchiveGroupsSettings(raw)
	return nil
}

const groupNumberToken = "{n}"

// Enabled reports whether the settings describe a usable grouping: a valid
// attribute key, a divisor of at least 1 and a path with exactly one {n} and
// no characters outside letters, digits, '_', '.' and '-'. The write check
// uses the same path rule.
func (g ArchiveGroupsSettings) Enabled() bool {
	return ValidAttrKey(g.Attribute) && g.Divisor >= 1 && validGroupPath(g.Path) &&
		(g.AnchorAttribute == "" || ValidAttrKey(g.AnchorAttribute))
}

func validGroupPath(path string) bool {
	if strings.Count(path, groupNumberToken) != 1 {
		return false
	}
	for _, r := range strings.Replace(path, groupNumberToken, "", 1) {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

// Bucket returns the group number of an entry with the given attributes:
// floor(value/Divisor). It is false when the attribute is missing, not a
// number (numeric strings count) or negative.
func (g ArchiveGroupsSettings) Bucket(attrs map[string]any) (int64, bool) {
	if !g.Enabled() {
		return 0, false
	}
	f, ok := attrNumber(attrs[g.Attribute])
	if !ok || f < 0 {
		return 0, false
	}
	return int64(math.Floor(f / float64(g.Divisor))), true
}

func attrNumber(v any) (float64, bool) {
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case float32:
		f = float64(x)
	case int:
		f = float64(x)
	case int64:
		f = float64(x)
	case interface{ Float64() (float64, error) }: // json.Number
		var err error
		if f, err = x.Float64(); err != nil {
			return 0, false
		}
	case string:
		var err error
		if f, err = strconv.ParseFloat(strings.TrimSpace(x), 64); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// URL returns the group page path under archivePath.
func (g ArchiveGroupsSettings) URL(archivePath string, n int64) string {
	return strings.TrimRight(archivePath, "/") + "/" + strings.ReplaceAll(g.Path, groupNumberToken, strconv.FormatInt(n, 10))
}

// Parse is the inverse of URL: it returns the group number when p is a group
// page path under archivePath. Only canonical numbers (digits, no leading
// zeros) match.
func (g ArchiveGroupsSettings) Parse(archivePath, p string) (int64, bool) {
	if !g.Enabled() {
		return 0, false
	}
	rest, ok := strings.CutPrefix(p, strings.TrimRight(archivePath, "/")+"/")
	if !ok {
		return 0, false
	}
	prefix, suffix, _ := strings.Cut(g.Path, groupNumberToken)
	rest, ok = strings.CutPrefix(rest, prefix)
	if !ok {
		return 0, false
	}
	digits, ok := strings.CutSuffix(rest, suffix)
	if !ok || digits == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != digits {
		return 0, false
	}
	return n, true
}

// Min and Max are the attribute range of group n: Min inclusive, Max exclusive.
func (g ArchiveGroupsSettings) Min(n int64) float64 { return float64(n) * float64(g.Divisor) }

// Max is the exclusive upper bound of group n (see Min).
func (g ArchiveGroupsSettings) Max(n int64) float64 { return float64(n+1) * float64(g.Divisor) }

// GroupLabel returns the short name of group n (default "{n}").
func (g ArchiveGroupsSettings) GroupLabel(n int64) string {
	l := g.Label
	if l == "" {
		l = groupNumberToken
	}
	return strings.ReplaceAll(l, groupNumberToken, strconv.FormatInt(n, 10))
}

// Heading returns the page title and description of group n; archiveTitle is
// the archive's own heading. The title defaults to "{archive_title}: {label}",
// the description to empty.
func (g ArchiveGroupsSettings) Heading(n int64, archiveTitle string) (title, desc string) {
	r := strings.NewReplacer(
		groupNumberToken, strconv.FormatInt(n, 10),
		"{label}", g.GroupLabel(n),
		"{archive_title}", archiveTitle,
	)
	t := g.Title
	if t == "" {
		t = "{archive_title}: {label}"
	}
	return r.Replace(t), r.Replace(g.Description)
}

// EntryTarget returns where an entry with these attributes redirects to: its
// group page, plus "#<anchor attribute value>" when set. It is "" unless
// RedirectEntries is on and the entry falls in a bucket.
func (g ArchiveGroupsSettings) EntryTarget(archivePath string, attrs map[string]any) string {
	if !g.RedirectEntries {
		return ""
	}
	n, ok := g.Bucket(attrs)
	if !ok {
		return ""
	}
	target := g.URL(archivePath, n)
	if g.AnchorAttribute != "" {
		if a, _ := attrs[g.AnchorAttribute].(string); a != "" {
			target += "#" + url.PathEscape(a)
		}
	}
	return target
}
