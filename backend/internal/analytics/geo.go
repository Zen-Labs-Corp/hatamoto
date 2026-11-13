package analytics

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"runtime"
	"strings"

	"github.com/oschwald/maxminddb-golang"
)

// maxGeoDBSize caps the .mmdb read into memory; country databases are a few MB.
const maxGeoDBSize = 256 << 20

// GeoDB is a country database, by default loaded fully into memory so
// replacing the file on disk cannot affect lookups already being served.
type GeoDB struct {
	r *maxminddb.Reader
}

// checkGeoFile applies the shared file checks of both open modes.
func checkGeoFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return errors.New("geoip database is not readable")
	}
	if !fi.Mode().IsRegular() {
		return errors.New("geoip database is not a regular file")
	}
	if fi.Size() > maxGeoDBSize {
		return fmt.Errorf("geoip database is larger than %d MB", maxGeoDBSize>>20)
	}
	return nil
}

// OpenGeo reads the .mmdb at path into a private in-memory copy. Errors never
// include file content.
func OpenGeo(path string) (*GeoDB, error) {
	if err := checkGeoFile(path); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("geoip database is not readable")
	}
	r, err := maxminddb.FromBytes(b)
	if err != nil {
		return nil, errors.New("geoip database is not a valid mmdb file")
	}
	return &GeoDB{r: r}, nil
}

// OpenGeoMapped maps the .mmdb at path read-only instead of copying it to the
// heap. The mapping is released by the library once the GeoDB is unreachable,
// never by an explicit close, so a lookup in flight cannot lose its memory.
// The file must be replaced by rename; overwriting it in place can crash the
// process. Platforms without mmap fall back to an in-memory copy.
func OpenGeoMapped(path string) (*GeoDB, error) {
	if err := checkGeoFile(path); err != nil {
		return nil, err
	}
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, errors.New("geoip database is not a valid mmdb file")
	}
	return &GeoDB{r: r}, nil
}

type geoRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

// Country returns the upper-case country code for ip, or "" when the address
// is invalid, not in the database or the receiver is nil.
func (g *GeoDB) Country(ip string) string {
	if g == nil || g.r == nil {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	// Keep the reader (and its mapping, if any) alive until the decode ends.
	defer runtime.KeepAlive(g.r)
	var rec geoRecord
	if err := g.r.Lookup(addr.AsSlice(), &rec); err != nil {
		return ""
	}
	c := strings.ToUpper(rec.Country.ISOCode)
	if len(c) != 2 || c[0] < 'A' || c[0] > 'Z' || c[1] < 'A' || c[1] > 'Z' {
		return ""
	}
	return c
}
