package seo

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
)

// DefaultIndexNowHost is used when no endpoint is saved.
const DefaultIndexNowHost = "api.indexnow.org"

// IndexNowEndpoints lists the search engine hosts a ping may go to.
var IndexNowEndpoints = []string{"api.indexnow.org", "www.bing.com", "yandex.com", "search.seznam.cz", "searchadvisor.naver.com"}

var indexNowKeyRE = regexp.MustCompile(`^[a-zA-Z0-9-]{8,128}$`)

// IndexNow holds the opt-in IndexNow ping settings. The zero value is off.
type IndexNow struct {
	Enabled  bool   `json:"enabled,omitzero"`
	Key      string `json:"key,omitempty"`
	Endpoint string `json:"endpoint,omitempty"` // bare host; "" => DefaultIndexNowHost
}

// IsZero reports whether no IndexNow setting is saved.
func (n IndexNow) IsZero() bool { return n == IndexNow{} }

// Host is the endpoint host to ping.
func (n IndexNow) Host() string {
	if n.Endpoint == "" {
		return DefaultIndexNowHost
	}
	return n.Endpoint
}

// NewIndexNowKey returns a fresh random key of 32 hex characters.
func NewIndexNowKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("seo: crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// validateIndexNow checks the indexnow key.
func validateIndexNow(n IndexNow) []FieldError {
	var errs []FieldError
	if n.Endpoint != "" && !containsString(IndexNowEndpoints, n.Endpoint) {
		errs = append(errs, FieldError{"indexnow[endpoint]", "Choose one of the listed search engines."})
	}
	if n.Key != "" && !indexNowKeyRE.MatchString(n.Key) {
		errs = append(errs, FieldError{"indexnow[key]", "Use 8 to 128 letters, digits or dashes."})
	}
	return errs
}
