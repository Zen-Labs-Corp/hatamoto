package media

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// RowlessSampleMax is how many keys a rowless report keeps by default.
const RowlessSampleMax = 20

// RowlessOptions configures Rowless.
type RowlessOptions struct {
	// Prefix limits the check to one key prefix as CleanKeyPrefix returns it;
	// "" means the whole store.
	Prefix string
	// SampleMax is how many keys the result keeps; zero or less means
	// RowlessSampleMax.
	SampleMax int
}

// RowlessResult is the outcome of Rowless: every key is counted, only the
// first SampleMax in lexical order are kept.
type RowlessResult struct {
	Count  int
	Bytes  int64
	Sample []string
}

// IgnoredKey reports whether key is a file the application itself owns or a
// name nothing can adopt: a dot segment, a staged changeset file, a replace
// temp or leftover file, an import temp name, a backslash, NUL or invalid UTF-8.
func IgnoredKey(key string) bool {
	if key == "" || isStagedKey(key) || !utf8.ValidString(key) || strings.ContainsAny(key, "\\\x00") {
		return true
	}
	for _, seg := range strings.Split(key, "/") {
		if strings.HasPrefix(seg, ".") || strings.HasPrefix(seg, "wpimport-") {
			return true
		}
	}
	if path.Dir(key) == legacyMediaTenantDir {
		base := path.Base(key)
		return strings.HasPrefix(base, "replace-") && (strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".tmp.old"))
	}
	return isReplaceAside(path.Base(key))
}

// isReplaceAside reports whether name is a variant file a replace moved aside:
// "<name>.<uuid>.old".
func isReplaceAside(name string) bool {
	rest, ok := strings.CutSuffix(name, ".old")
	if !ok || len(rest) < 37 {
		return false
	}
	_, err := uuid.Parse(rest[len(rest)-36:])
	return err == nil && rest[len(rest)-37] == '.'
}

// hasUnsafeRune reports whether key holds a control character or an invisible
// format character (a bidi override, a zero-width joiner).
func hasUnsafeRune(key string) bool {
	return strings.IndexFunc(key, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0
}

// DisplayKey returns key as it is safe to print to a terminal: unchanged, or
// quoted with the unsafe characters escaped when it holds control or format
// characters (an escape sequence, a newline, a bidi override).
func DisplayKey(key string) string {
	if hasUnsafeRune(key) {
		return strconv.Quote(key)
	}
	return key
}

// CleanKeyPrefix turns an operator-given directory into a key prefix relative
// to the store root. "", "/" and "." mean the whole store (""). A path that
// climbs out with "..", or holds a backslash or NUL, is refused.
func CleanKeyPrefix(dir string) (string, error) {
	if strings.ContainsAny(dir, "\\\x00") {
		return "", fmt.Errorf("media: invalid directory %q", dir)
	}
	for _, seg := range strings.Split(dir, "/") {
		if seg == ".." {
			return "", fmt.Errorf("media: invalid directory %q", dir)
		}
	}
	p := strings.TrimLeft(path.Clean("/"+dir), "/")
	return p, nil
}

// inPrefix reports whether key is prefix itself or lies below it.
func inPrefix(key, prefix string) bool {
	return prefix == "" || key == prefix || strings.HasPrefix(key, prefix+"/")
}

// listRowless returns the objects under prefix that are not in referenced and
// are not ignored, sorted by key, and how many listed objects do have a row.
func listRowless(ctx context.Context, l Lister, referenced map[string]struct{}, prefix string) (out []ObjectInfo, withRow int, err error) {
	listed, err := l.List(ctx, prefix)
	if err != nil {
		return nil, 0, fmt.Errorf("media: list: %w", err)
	}
	for _, obj := range listed {
		if !inPrefix(obj.Key, prefix) || IgnoredKey(obj.Key) {
			continue
		}
		if _, ok := referenced[obj.Key]; ok {
			withRow++
			continue
		}
		out = append(out, obj)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, withRow, nil
}

// Rowless finds the files of the store that no row refers to. referenced holds
// every stored key, variants included.
func Rowless(ctx context.Context, l Lister, referenced map[string]struct{}, o RowlessOptions) (RowlessResult, error) {
	var res RowlessResult
	max := o.SampleMax
	if max <= 0 {
		max = RowlessSampleMax
	}
	objs, _, err := listRowless(ctx, l, referenced, o.Prefix)
	if err != nil {
		return res, err
	}
	for _, obj := range objs {
		res.Count++
		res.Bytes += obj.Size
		if len(res.Sample) < max {
			res.Sample = append(res.Sample, obj.Key)
		}
	}
	return res, nil
}
