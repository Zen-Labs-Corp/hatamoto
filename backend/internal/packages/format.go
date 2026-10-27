// Package packages implements the .hmp package format (a zip holding a plugin
// or a theme), its zip-safety rules, ed25519 signing, the signed registry
// index and the Postgres store plus manager that installs, activates and
// removes packages while the server runs. See docs/PACKAGES.md.
package packages

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

// Kind is what a package holds.
type Kind string

// The two package kinds, named by their manifest: plugin.json or theme.json.
const (
	KindPlugin Kind = "plugin"
	KindTheme  Kind = "theme"
)

// Well-known entries of a package.
const (
	PluginManifest = "plugin.json"
	ThemeManifest  = "theme.json"
	PluginWasm     = "plugin.wasm"
	SignatureFile  = "signature.json"
	// Extension is the file extension of a package.
	Extension = ".hmp"
)

// Limits bound what an archive may expand to. Parse enforces them on the bytes
// actually read, never on the sizes the archive declares.
type Limits struct {
	Archive int64 // size of the .hmp file
	Entries int   // files and directories
	File    int64 // one file, uncompressed
	Total   int64 // all files, uncompressed
	// Ratio is the largest uncompressed:compressed ratio of a file bigger than
	// RatioFloor (a zip bomb compresses far beyond any real asset).
	Ratio      int64
	RatioFloor int64
	Path       int // longest entry name
}

// DefaultLimits are generous for real plugins (a wasm module is the big file)
// and far below what a bomb needs.
var DefaultLimits = Limits{
	Archive: 64 << 20, Entries: 2048, File: 64 << 20, Total: 128 << 20,
	Ratio: 200, RatioFloor: 1 << 20, Path: 200,
}

// topDirs are the directories a package may hold. Anything else is refused.
var topDirs = map[string]bool{
	"sections": true, "schemas": true, "layouts": true, "partials": true,
	"assets": true, "migrations": true, "ui": true, "widgets": true,
}

// topFiles are the files allowed at the root next to the directories.
var topFiles = map[string]bool{
	PluginManifest: true, ThemeManifest: true, PluginWasm: true, SignatureFile: true,
	"README.md": true, "LICENSE": true, "CHANGELOG.md": true,
}

var namePattern = sdk.NamePattern

// Package is a parsed, validated .hmp. It is immutable.
type Package struct {
	Kind     Kind
	Name     string
	Version  string
	Manifest json.RawMessage // plugin.json or theme.json, as shipped
	// Digest is what the signature covers: a hash over every file name and
	// content hash except signature.json, so it does not depend on how the
	// zip was compressed.
	Digest [32]byte
	// Signature is the embedded signature.json, nil when the package is unsigned.
	Signature *Signature
	// ScreenshotErr is why theme.json's "screenshot" was ignored (nil: valid or
	// absent). Parse stays lenient so installed themes keep loading; publishing
	// paths (theme check --registry) refuse on it.
	ScreenshotErr error

	data []byte
	zr   *zip.Reader
}

// Data is the archive bytes the package was parsed from.
func (p *Package) Data() []byte { return p.data }

// SHA256 is the hash of the archive bytes (what registries list).
func (p *Package) SHA256() [32]byte { return sha256.Sum256(p.data) }

// FS is the package's files (without signature.json). Reads decompress on
// demand; Parse already read every file once, so they cannot fail on a lying
// header.
func (p *Package) FS() fs.FS { return packageFS{p.zr} }

// ReadFile returns one file of the package.
func (p *Package) ReadFile(name string) ([]byte, error) { return fs.ReadFile(p.FS(), name) }

// Has reports whether the package holds the file.
func (p *Package) Has(name string) bool {
	_, err := fs.Stat(p.FS(), name)
	return err == nil
}

// Wasm returns plugin.wasm of a plugin package.
func (p *Package) Wasm() ([]byte, error) { return p.ReadFile(PluginWasm) }

// PluginManifest decodes plugin.json of a plugin package.
func (p *Package) PluginManifest() (sdk.Manifest, error) {
	var m sdk.Manifest
	if p.Kind != KindPlugin {
		return m, errors.New("not a plugin package")
	}
	err := json.Unmarshal(p.Manifest, &m)
	return m, err
}

// ThemeMeta is the part of theme.json the host reads.
type ThemeMeta struct {
	Name           string          `json:"name"`
	Version        string          `json:"version"`
	Description    string          `json:"description,omitempty"`
	SettingsSchema json.RawMessage `json:"settings_schema,omitempty"`
	// Screenshot is an optional preview image under assets/, see ValidateScreenshot.
	Screenshot string `json:"screenshot,omitempty"`
}

// ThemeMeta decodes theme.json of a theme package.
func (p *Package) ThemeMeta() (ThemeMeta, error) {
	var m ThemeMeta
	if p.Kind != KindTheme {
		return m, errors.New("not a theme package")
	}
	err := json.Unmarshal(p.Manifest, &m)
	if p.ScreenshotErr != nil {
		m.Screenshot = ""
	}
	return m, err
}

// packageFS hides signature.json from consumers of the package's files.
type packageFS struct{ zr *zip.Reader }

func (f packageFS) Open(name string) (fs.File, error) {
	if name == SignatureFile {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return f.zr.Open(name)
}

// ParseError is a package that was refused; its message is safe to show.
type ParseError struct{ Msg string }

func (e *ParseError) Error() string { return "package: " + e.Msg }

// readEntry inflates one entry, at most limit+1 bytes, and reports how many
// compressed bytes the stream consumed. The sizes and checksum in the header
// are checked against what was read.
func readEntry(f *zip.File, limit int64) ([]byte, int64, error) {
	raw, err := f.OpenRaw()
	if err != nil {
		return nil, 0, err
	}
	cr := &countingByteReader{r: bufio.NewReader(raw)}
	var src io.Reader
	switch f.Method {
	case zip.Store:
		src = cr
	case zip.Deflate:
		fr := flate.NewReader(cr)
		defer fr.Close()
		src = fr
	default:
		return nil, 0, fmt.Errorf("unsupported compression method %d", f.Method)
	}
	b, err := io.ReadAll(io.LimitReader(src, limit+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(b)) > limit {
		return b, cr.n, nil // the caller reports which limit it broke
	}
	if uint64(len(b)) != f.UncompressedSize64 || crc32.ChecksumIEEE(b) != f.CRC32 {
		return nil, 0, errors.New("the contents do not match the archive header")
	}
	return b, cr.n, nil
}

// countingByteReader counts the bytes read through it (flate takes single
// bytes from an io.ByteReader, so the count is what the stream consumed).
type countingByteReader struct {
	r *bufio.Reader
	n int64
}

func (c *countingByteReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingByteReader) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err == nil {
		c.n++
	}
	return b, err
}

// checkEnvelope refuses what the signed digest does not cover and what costs
// memory before the entry cap could apply: data before the first entry, an
// archive comment, and more central-directory records than the limit.
func checkEnvelope(raw []byte, lim Limits) error {
	if !bytes.HasPrefix(raw, []byte("PK\x03\x04")) {
		return refuse("not a valid zip archive: it must start with its first entry")
	}
	// The end-of-central-directory record is 22 bytes plus the comment; with no
	// comment it is the last 22 bytes.
	if len(raw) < 22 || !bytes.HasPrefix(raw[len(raw)-22:], []byte("PK\x05\x06")) {
		if bytes.Contains(raw[max(0, len(raw)-22-0xffff):], []byte("PK\x05\x06")) {
			return refuse("an archive comment is not allowed")
		}
		return refuse("not a valid zip archive")
	}
	eocd := raw[len(raw)-22:]
	if int(binary.LittleEndian.Uint16(eocd[20:])) != 0 {
		return refuse("an archive comment is not allowed")
	}
	if n := int(binary.LittleEndian.Uint16(eocd[10:])); n > lim.Entries {
		return refuse("more than %d entries", lim.Entries)
	}
	return nil
}

func refuse(format string, a ...any) error { return &ParseError{Msg: fmt.Sprintf(format, a...)} }

// Parse validates raw as a .hmp: zip safety (no traversal, links, odd names,
// oversized or highly compressed entries), the layout, and the manifest. It
// does not check the signature; see TrustSet.Verify and Manager.Install.
func Parse(raw []byte, lim Limits) (*Package, error) {
	if int64(len(raw)) > lim.Archive {
		return nil, refuse("archive is larger than %d bytes", lim.Archive)
	}
	if err := checkEnvelope(raw, lim); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, refuse("not a valid zip archive: %v", err)
	}
	if len(zr.File) > lim.Entries {
		return nil, refuse("more than %d entries", lim.Entries)
	}
	type entry struct {
		name string
		sum  [32]byte
		size int
	}
	var (
		entries  []entry
		seen     = map[string]bool{}
		total    int64
		manifest []byte
		kinds    []Kind
		sigRaw   []byte
		hasWasm  bool
		// anyWidgets: an entry under widgets/ (a theme may hold none);
		// hasWidgetFiles: a file there (a widget-only plugin needs one).
		anyWidgets, hasWidgetFiles bool
		assets                     = map[string][]byte{} // assets/ contents, for the screenshot check
	)
	for _, f := range zr.File {
		name := f.Name
		isDir := strings.HasSuffix(name, "/")
		clean := strings.TrimSuffix(name, "/")
		if err := checkName(clean, lim); err != nil {
			return nil, err
		}
		if f.Flags&0x1 != 0 {
			return nil, refuse("%s: encrypted entry", name)
		}
		if mode := f.Mode(); mode&(os.ModeSymlink|os.ModeDevice|os.ModeNamedPipe|os.ModeSocket|os.ModeIrregular) != 0 {
			return nil, refuse("%s: not a regular file (links and special files are refused)", name)
		}
		key := strings.ToLower(clean)
		if seen[key] {
			return nil, refuse("%s: duplicate entry", name)
		}
		seen[key] = true
		if !strings.Contains(clean, "/") && !isDir {
			if !topFiles[clean] {
				return nil, refuse("%s: not a file a package may hold at its root", name)
			}
		} else if !topDirs[strings.SplitN(clean, "/", 2)[0]] {
			return nil, refuse("%s: not in a directory a package may hold", name)
		}
		if clean == "widgets" || strings.HasPrefix(clean, "widgets/") {
			if err := checkWidgetEntry(clean, isDir); err != nil {
				return nil, err
			}
			anyWidgets = true
			if !isDir {
				hasWidgetFiles = true
			}
		}
		if isDir {
			if f.UncompressedSize64 != 0 {
				return nil, refuse("%s: directory with content", name)
			}
			continue
		}
		if f.UncompressedSize64 > uint64(lim.File) {
			return nil, refuse("%s: larger than %d bytes", name, lim.File)
		}
		// Read what is really there, bounded: the header is not trusted.
		limit := min(lim.File, lim.Total-total)
		b, compressed, err := readEntry(f, limit)
		if err != nil {
			return nil, refuse("%s: %v", name, err)
		}
		if int64(len(b)) > limit {
			if int64(len(b)) > lim.File {
				return nil, refuse("%s: larger than %d bytes", name, lim.File)
			}
			return nil, refuse("archive expands to more than %d bytes", lim.Total)
		}
		// The ratio uses the compressed bytes the stream really took.
		if int64(len(b)) > lim.RatioFloor && compressed > 0 && int64(len(b))/compressed > lim.Ratio {
			return nil, refuse("%s: compression ratio above %d:1 (zip bomb)", name, lim.Ratio)
		}
		total += int64(len(b))
		if strings.HasPrefix(clean, "assets/") {
			assets[clean] = b
		}
		switch clean {
		case PluginManifest:
			manifest, kinds = b, append(kinds, KindPlugin)
		case ThemeManifest:
			manifest, kinds = b, append(kinds, KindTheme)
		case SignatureFile:
			sigRaw = b
			continue // not part of the digest
		case PluginWasm:
			hasWasm = true
		}
		entries = append(entries, entry{clean, sha256.Sum256(b), len(b)})
	}
	if len(kinds) != 1 {
		return nil, refuse("a package holds exactly one of %s or %s", PluginManifest, ThemeManifest)
	}
	p := &Package{Kind: kinds[0], Manifest: manifest, data: raw, zr: zr}
	switch p.Kind {
	case KindPlugin:
		if !hasWasm && !hasWidgetFiles {
			return nil, refuse("a plugin package needs %s (or widgets/)", PluginWasm)
		}
		m, err := p.PluginManifest()
		if err != nil {
			return nil, refuse("%s: %v", PluginManifest, err)
		}
		if err := m.Validate(); err != nil {
			return nil, refuse("%s: %v", PluginManifest, err)
		}
		p.Name, p.Version = m.Name, m.Version
	case KindTheme:
		if hasWasm {
			return nil, refuse("a theme package cannot hold %s", PluginWasm)
		}
		if anyWidgets {
			return nil, refuse("a theme package cannot hold widgets/")
		}
		m, err := p.ThemeMeta()
		if err != nil {
			return nil, refuse("%s: %v", ThemeManifest, err)
		}
		if !namePattern.MatchString(m.Name) {
			return nil, refuse("%s: invalid theme name %q (want %s)", ThemeManifest, m.Name, namePattern)
		}
		if !semverPattern.MatchString(m.Version) {
			return nil, refuse("%s: version %q is not semver", ThemeManifest, m.Version)
		}
		if m.Screenshot != "" {
			p.ScreenshotErr = ValidateScreenshot(m.Screenshot, assets)
		}
		p.Name, p.Version = m.Name, m.Version
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%d\x00", e.name, e.size)
		h.Write(e.sum[:])
	}
	copy(p.Digest[:], h.Sum(nil))
	if sigRaw != nil {
		var s Signature
		if err := json.Unmarshal(sigRaw, &s); err != nil {
			return nil, refuse("%s: %v", SignatureFile, err)
		}
		p.Signature = &s
	}
	return p, nil
}

var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// checkName refuses entry names that could escape or confuse an extractor, even
// though nothing is extracted to disk.
func checkName(name string, lim Limits) error {
	switch {
	case name == "":
		return refuse("empty entry name")
	case len(name) > lim.Path:
		return refuse("entry name longer than %d bytes", lim.Path)
	case !utf8.ValidString(name):
		return refuse("entry name is not valid UTF-8")
	case strings.ContainsAny(name, "\\\x00:") || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return refuse("%q: entry name has a backslash, colon or control character", name)
	case strings.HasPrefix(name, "/"):
		return refuse("%q: absolute path", name)
	case !fs.ValidPath(name) || path.Clean(name) != name:
		return refuse("%q: path traversal or unclean path", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if strings.HasPrefix(seg, ".") {
			return refuse("%q: hidden files are not allowed", name)
		}
	}
	return nil
}
