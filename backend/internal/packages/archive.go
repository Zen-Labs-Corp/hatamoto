package packages

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ReadArchive verifies the sha256 of the exact bytes of a zip, then returns its
// files as a path->bytes map for PackFiles. Everything happens in memory: no
// entry is extracted and no entry name reaches the file system. It refuses a
// sha mismatch, unsafe names (checkName), links and special files, duplicate
// entries (case-insensitive) and anything over lim (entries, file, total,
// ratio), measured on the bytes actually read. Directories are skipped. When
// theme.json is not at the root, ONE common wrapper directory is stripped.
func ReadArchive(data []byte, wantSHA256 string, lim Limits) (map[string][]byte, error) {
	if !sha256Pattern.MatchString(wantSHA256) {
		return nil, refuse("sha256 must be 64 lowercase hex characters")
	}
	if int64(len(data)) > lim.Archive {
		return nil, refuse("archive is larger than %d bytes", lim.Archive)
	}
	sum := sha256.Sum256(data)
	if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(wantSHA256)) != 1 {
		return nil, refuse("archive sha256 does not match the pinned value")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, refuse("not a valid zip archive: %v", err)
	}
	if len(zr.File) > lim.Entries {
		return nil, refuse("more than %d entries", lim.Entries)
	}
	var (
		files = map[string][]byte{}
		seen  = map[string]bool{}
		total int64
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
		if isDir {
			continue
		}
		if f.UncompressedSize64 > uint64(lim.File) {
			return nil, refuse("%s: larger than %d bytes", name, lim.File)
		}
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
		if int64(len(b)) > lim.RatioFloor && compressed > 0 && int64(len(b))/compressed > lim.Ratio {
			return nil, refuse("%s: compression ratio above %d:1 (zip bomb)", name, lim.Ratio)
		}
		total += int64(len(b))
		if clean == SignatureFile {
			continue // a re-signed package gets a fresh signature
		}
		files[clean] = b
	}
	return stripWrapper(files, lim)
}

// stripWrapper removes the single directory every file lives in (the shape of
// a GitHub source zip) when theme.json is not at the root. Anything else is
// returned untouched.
func stripWrapper(files map[string][]byte, lim Limits) (map[string][]byte, error) {
	if _, ok := files[ThemeManifest]; ok || len(files) == 0 {
		return files, nil
	}
	var top string
	for name := range files {
		first, _, nested := strings.Cut(name, "/")
		if !nested || (top != "" && first != top) {
			return files, nil
		}
		top = first
	}
	out := make(map[string][]byte, len(files))
	for name, b := range files {
		rel := strings.TrimPrefix(name, top+"/")
		if err := checkName(rel, lim); err != nil {
			return nil, err
		}
		out[rel] = b
	}
	return out, nil
}
