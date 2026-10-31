package migrations

import (
	"net/netip"
	"os"
	"strings"
	"testing"
)

// The relabel migration must not follow the live Cloudflare list: it carries
// a frozen copy of its own.
func TestRelabelCDNEdgePrefixesFrozen(t *testing.T) {
	src, err := os.ReadFile("202610311110_relabel_cdn_edge_spoofed.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "CloudflareFallback") || strings.Contains(string(src), "internal/agents") {
		t.Fatal("relabel migration must not depend on the live agents list")
	}
	if len(relabelCDNEdgePrefixes) == 0 {
		t.Fatal("frozen prefix list is empty")
	}
	for _, s := range relabelCDNEdgePrefixes {
		if _, err := netip.ParsePrefix(s); err != nil {
			t.Errorf("bad prefix %q: %v", s, err)
		}
	}
}
