package staticexport

import (
	"fmt"
	"io"
	"sort"

	"hatamoto.dev/core/internal/jsonx"
)

// ManifestFile is the name of the manifest written next to the output. It
// also marks a directory as an earlier export.
const ManifestFile = ".hatamoto-static.json"

const manifestVersion = 1

// Entry describes one published file.
type Entry struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Type   string `json:"type"`
}

// Manifest lists every published file. Keys are output-relative paths
// separated by "/".
type Manifest struct {
	Version int              `json:"version"`
	Files   map[string]Entry `json:"files"`
}

// Diff is the difference between two manifests. Every slice is sorted.
type Diff struct {
	Changed, Unchanged, Removed []string
}

// DiffManifests compares prev with next. A file is changed when it is new or
// its hash or size differs, removed when only prev has it.
func DiffManifests(prev, next Manifest) Diff {
	var d Diff
	for p, e := range next.Files {
		old, ok := prev.Files[p]
		if ok && old.SHA256 == e.SHA256 && old.Size == e.Size {
			d.Unchanged = append(d.Unchanged, p)
		} else {
			d.Changed = append(d.Changed, p)
		}
	}
	for p := range prev.Files {
		if _, ok := next.Files[p]; !ok {
			d.Removed = append(d.Removed, p)
		}
	}
	sort.Strings(d.Changed)
	sort.Strings(d.Unchanged)
	sort.Strings(d.Removed)
	return d
}

// ReadManifest decodes a manifest. An unknown version is an error.
func ReadManifest(r io.Reader) (Manifest, error) {
	var m Manifest
	if err := jsonx.UnmarshalRead(r, &m, jsonx.Stored); err != nil {
		return Manifest{}, fmt.Errorf("read static export manifest: %w", err)
	}
	if m.Version != manifestVersion {
		return Manifest{}, fmt.Errorf("static export manifest version %d is not supported", m.Version)
	}
	if m.Files == nil {
		m.Files = map[string]Entry{}
	}
	return m, nil
}

// Encode renders the manifest with sorted keys.
func (m Manifest) Encode() ([]byte, error) {
	m.Version = manifestVersion
	if m.Files == nil {
		m.Files = map[string]Entry{}
	}
	return jsonx.Indent(m)
}
