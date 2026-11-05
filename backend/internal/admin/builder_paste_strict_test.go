package admin

import (
	"net/http"
	"testing"
)

// A duplicate member name or data after the value is a 400, never a guess.
func TestPasteRejectsDuplicateKeysAndTrailingJunk(t *testing.T) {
	t.Parallel()

	r := newReuseRig(t)
	nested := `{"hm":"nodes","v":2,"nodes":[{"id":"a","id":"b","t":"text","p":{"text":"x"}}]}`
	if rec, _ := r.paste(t, "owner-token", nested); rec.Code < 400 || rec.Code >= 500 {
		t.Errorf("duplicate key inside nodes: %d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]string{
		"duplicate key":  `{"hm":"nodes","hm":"nodes","v":2,"nodes":` + pasteNodes + `}`,
		"trailing junk":  envelope(pasteNodes) + ` junk`,
		"duplicate nest": `{"hm":"nodes","v":2,"nodes":[{"id":"a","id":"b","t":"text","p":{"text":"x"}}]}`,
	} {
		if rec, _ := r.paste(t, "owner-token", body); rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	for name, body := range map[string]string{
		"duplicate key": `{"hm":"nodes","hm":"nodes","v":2,"nodes":` + pasteNodes + `}`,
		"trailing junk": envelope(pasteNodes) + ` junk`,
	} {
		if rec, _ := r.paste(t, "owner-token", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %s", name, rec.Code, rec.Body)
		}
	}
}
