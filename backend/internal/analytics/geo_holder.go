package analytics

import (
	"log/slog"
	"os"
	"sync/atomic"
	"time"
)

// geoSig identifies one on-disk version of the database file. An atomic rename
// changes the inode, which os.SameFile sees even when size and mtime match.
type geoSig struct {
	fi os.FileInfo
}

func (a *geoSig) same(b *geoSig) bool {
	if a == nil || b == nil || a.fi == nil || b.fi == nil {
		return false
	}
	return os.SameFile(a.fi, b.fi) && a.fi.Size() == b.fi.Size() && a.fi.ModTime().Equal(b.fi.ModTime())
}

// GeoHolder serves country lookups from a database file and swaps in a new
// copy when the file is replaced. A failed reload keeps the last good
// database, so a bad refresh never turns lookups off. Lookups are lock-free.
type GeoHolder struct {
	path      string
	log       *slog.Logger
	p         atomic.Pointer[GeoDB]
	sig       atomic.Pointer[geoSig] // signature of the loaded file
	failed    atomic.Pointer[geoSig] // signature of the last file that failed to load
	lastCheck atomic.Int64           // unix nanoseconds of the last stat
}

// NewGeoHolder returns a holder for the database at path. Call Reload to load
// it. An empty path makes a holder that is not configured and never loads.
func NewGeoHolder(path string, log *slog.Logger) *GeoHolder {
	if log == nil {
		log = slog.Default()
	}
	return &GeoHolder{path: path, log: log}
}

// Country returns the upper-case country code for ip, or "" when no database
// is loaded or the address is unknown. Safe on a nil receiver.
func (h *GeoHolder) Country(ip string) string {
	if h == nil {
		return ""
	}
	return h.p.Load().Country(ip)
}

// Configured reports whether a database path is set.
func (h *GeoHolder) Configured() bool { return h != nil && h.path != "" }

// Loaded reports whether a database is currently serving lookups.
func (h *GeoHolder) Loaded() bool { return h != nil && h.p.Load() != nil }

// Reload loads the database when the file is new or changed since the last
// successful load. An unchanged file is a no-op. A failure keeps the last good
// database and is logged once per distinct file version, without the path or
// any file content.
func (h *GeoHolder) Reload() {
	if !h.Configured() {
		return
	}
	h.lastCheck.Store(time.Now().UnixNano())
	fi, err := os.Stat(h.path)
	if err != nil {
		h.noteFailure(nil, err)
		return
	}
	sig := &geoSig{fi: fi}
	if sig.same(h.sig.Load()) || sig.same(h.failed.Load()) {
		return
	}
	g, err := OpenGeo(h.path)
	if err != nil {
		h.failed.Store(sig)
		h.noteFailure(sig, err)
		return
	}
	h.p.Store(g)
	h.sig.Store(sig)
	h.failed.Store(nil)
}

// noteFailure logs a failed reload. A missing file (sig nil) is logged only on
// the first miss after a success or after start.
func (h *GeoHolder) noteFailure(sig *geoSig, err error) {
	if sig == nil {
		if h.failed.Swap(&geoSig{}) != nil {
			return
		}
	}
	h.log.Warn("analytics: country database not loaded", "error", err)
}

// MaybeReload calls Reload unless the file was checked less than minGap ago.
func (h *GeoHolder) MaybeReload(minGap time.Duration) {
	if !h.Configured() {
		return
	}
	if time.Since(time.Unix(0, h.lastCheck.Load())) < minGap {
		return
	}
	h.Reload()
}
