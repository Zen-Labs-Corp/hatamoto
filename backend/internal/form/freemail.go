package form

import (
	_ "embed"
	"strings"
	"sync"

	"hatamoto.dev/core/internal/jsonx"
)

//go:embed freemail.json
var freeMailJSON []byte

var freeMail = sync.OnceValue(func() []string {
	var doc struct {
		Domains []string `json:"domains"`
	}
	if err := jsonx.Unmarshal(freeMailJSON, &doc, jsonx.Stored); err != nil {
		panic("form: freemail.json: " + err.Error())
	}
	return doc.Domains
})

// FreeEmailDomains returns the embedded free-provider domains, lowercase and
// sorted. The slice is a copy.
func FreeEmailDomains() []string {
	return append([]string(nil), freeMail()...)
}

// IsFreeEmail reports whether the address is on a listed free-provider domain.
func IsFreeEmail(addr string) bool {
	at := strings.LastIndexByte(addr, '@')
	if at < 0 {
		return false
	}
	domain := strings.ToLower(strings.TrimSpace(addr[at+1:]))
	for _, d := range freeMail() {
		if d == domain {
			return true
		}
	}
	return false
}
