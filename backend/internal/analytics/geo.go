package analytics

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/oschwald/maxminddb-golang"
)

// maxGeoDBSize caps the .mmdb read into memory; country databases are a few MB.
const maxGeoDBSize = 256 << 20

// GeoDB is a country database loaded fully into memory, so replacing the file
// on disk cannot affect a running server (restart to reload).
type GeoDB struct {
	r *maxminddb.Reader
}

// OpenGeo reads the .mmdb at path. Errors never include file content.
func OpenGeo(path string) (*GeoDB, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, errors.New("geoip database is not readable")
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("geoip database is not a regular file")
	}
	if fi.Size() > maxGeoDBSize {
		return nil, fmt.Errorf("geoip database is larger than %d MB", maxGeoDBSize>>20)
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
