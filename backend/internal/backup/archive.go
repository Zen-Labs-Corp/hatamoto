package backup

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	manifestFormat  = "hatamoto-backup"
	manifestVersion = 1
	manifestName    = "manifest.json"
	secretsName     = "secrets.env"
	configName      = "config.env"
)

type part struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// manifest is the last entry of the archive: what the parts are and where the
// backup came from.
type manifest struct {
	Format          string           `json:"format"`
	Version         int              `json:"version"`
	Created         time.Time        `json:"created"`
	Kind            Kind             `json:"kind"`
	Target          string           `json:"target,omitempty"`
	HatamotoVersion string           `json:"hatamoto_version,omitempty"`
	ServerMajor     int              `json:"server_major"`
	PgDump          string           `json:"pg_dump"`
	Parts           []part           `json:"parts"`
	Plugins         []string         `json:"plugins"`
	ContentRows     map[string]int64 `json:"content_rows"`
	MediaFiles      int              `json:"media_files"`
	MediaBytes      int64            `json:"media_bytes"`
	MediaMissing    int              `json:"media_missing,omitempty"`
	Trust           *TrustIDs        `json:"trust,omitempty"`
	ConfigPath      string           `json:"config_path,omitempty"`
}

// describe fixes the mode of a finished part (pg_dump honours the umask) and
// returns its size and checksum.
func describe(dir, name string) (part, error) {
	path := filepath.Join(dir, name)
	if err := os.Chmod(path, 0o600); err != nil {
		return part{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return part{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return part{}, err
	}
	return part{Name: name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// writeArchive writes the parts and the manifest to a temp tar and renames it
// to <id>.tar (0600). It returns the size and the entry names.
func writeArchive(s *Store, id, work string, parts []part, mf manifest) (int64, []string, error) {
	f, err := os.CreateTemp(s.Dir(), tmpPrefix+"*.tar")
	if err != nil {
		return 0, nil, err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // gone already after a successful rename
	defer f.Close()
	if err := os.Chmod(tmp, 0o600); err != nil {
		return 0, nil, err
	}
	tw := tar.NewWriter(f)
	var names []string
	for _, p := range parts {
		if err := addFile(tw, filepath.Join(work, p.Name), p.Name, mf.Created); err != nil {
			return 0, nil, err
		}
		names = append(names, p.Name)
	}
	body, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return 0, nil, err
	}
	hdr := &tar.Header{Name: manifestName, Mode: 0o600, Size: int64(len(body)), ModTime: mf.Created}
	if err := tw.WriteHeader(hdr); err != nil {
		return 0, nil, err
	}
	if _, err := tw.Write(body); err != nil {
		return 0, nil, err
	}
	names = append(names, manifestName)
	if err := tw.Close(); err != nil {
		return 0, nil, err
	}
	if err := f.Sync(); err != nil {
		return 0, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		return 0, nil, err
	}
	if err := f.Close(); err != nil {
		return 0, nil, err
	}
	if err := os.Rename(tmp, s.archive(id)); err != nil {
		return 0, nil, err
	}
	return fi.Size(), names, nil
}

func addFile(tw *tar.Writer, path, name string, mod time.Time) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: fi.Size(), ModTime: mod}); err != nil {
		return err
	}
	_, err = io.Copy(tw, in)
	return err
}
