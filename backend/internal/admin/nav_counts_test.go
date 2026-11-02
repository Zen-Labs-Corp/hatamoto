package admin

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCountBadge(t *testing.T) {
	t.Parallel()

	clock := time.Unix(1_700_000_000, 0)
	now := func() time.Time { return clock }
	n, err := 158, error(nil)
	calls := 0
	badge := countBadge(30*time.Second, now, func() (int, error) { calls++; return n, err })

	if got := badge(); got != "158" {
		t.Fatalf("badge %q, want 158", got)
	}
	n = 3
	clock = clock.Add(10 * time.Second)
	if got := badge(); got != "158" || calls != 1 {
		t.Errorf("within the ttl: badge %q after %d calls, want the cached 158 after 1", got, calls)
	}
	clock = clock.Add(31 * time.Second)
	if got := badge(); got != "3" || calls != 2 {
		t.Errorf("after the ttl: badge %q after %d calls, want 3 after 2", got, calls)
	}
	n = 0
	clock = clock.Add(time.Minute)
	if got := badge(); got != "" {
		t.Errorf("zero shows %q, want no badge", got)
	}
	n, err = 5, errors.New("down")
	clock = clock.Add(time.Minute)
	if got := badge(); got != "" {
		t.Errorf("an error shows %q, want no badge", got)
	}
	err = nil
	if got := badge(); got != "" || calls != 4 {
		t.Errorf("a failed count is retried only after the ttl: badge %q after %d calls", got, calls)
	}
}

// The sidebar shows the Entities and Plugins counts, and only when there is something to count.
func TestSidebarShowsEntityAndPluginCounts(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	body := c.get(t, "owner-token", "/admin/entities", "")
	if !strings.Contains(body, `<span>Entities</span><span class="hm-nav-link__badge">4</span>`) {
		t.Errorf("the Entities item lacks its count of 4")
	}
	if strings.Contains(body, `<span>Analytics</span><span class="hm-nav-link__badge">`) {
		t.Error("Analytics must not carry a count")
	}
}
