package admin

import (
	"context"
	"strings"
	"testing"
)

const eventSchema = `{"type":"object","properties":{"start":{"type":"string","title":"Start","format":"date-time"}}}`

func newEventRig(t *testing.T) (*contentRig, string, func() any) {
	t.Helper()
	c := newContentRig(t)
	c.a.deps.Site.(*fakeSite).cfg.Timezone = "America/New_York"
	sc := c.ents.addSchema(21, "Event", "event", eventSchema, `{}`, false)
	e := c.ents.addEntity(81, sc, "Launch", "launch", "draft", `{}`, t0)
	stored := func() any {
		got, _ := c.ents.GetEntity(context.Background(), e.ID)
		return attrsMap(got.Attributes)["start"]
	}
	return c, "/admin/entities/" + e.ID.String(), stored
}

func TestEntityEditDateTimeSavesInSiteZone(t *testing.T) {
	t.Parallel()

	c, path, stored := newEventRig(t)
	form := parseForm(t, c.get(t, "owner-token", path, ""))
	form.Set("_op", "save")
	form.Set(attrPrefix+"start", "2026-06-14T19:00")
	if rec := c.post(path, form); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if got := stored(); got != "2026-06-14T19:00:00-04:00" {
		t.Errorf("stored %v", got)
	}
	has(t, c.get(t, "owner-token", path, ""), `value="2026-06-14T19:00"`)
}

func TestEntityEditDateTimeRejectsNonexistentTime(t *testing.T) {
	t.Parallel()

	c, path, stored := newEventRig(t)
	form := parseForm(t, c.get(t, "owner-token", path, ""))
	form.Set("_op", "save")
	form.Set(attrPrefix+"start", "2026-03-08T02:30")
	rec := c.post(path, form)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "does not exist") {
		t.Fatalf("got %d, want 422 with the message", rec.Code)
	}
	if got := stored(); got != nil {
		t.Errorf("stored %v", got)
	}
}
