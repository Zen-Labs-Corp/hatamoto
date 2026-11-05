package admin

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// auditLog collects audit events for a test.
type auditLog struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (l *auditLog) add(_ context.Context, e AuditEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *auditLog) find(action string) *AuditEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.events {
		if l.events[i].Action == action {
			return &l.events[i]
		}
	}
	return nil
}

func newMediaRig(t *testing.T) (*contentRig, *auditLog) {
	t.Helper()
	log := &auditLog{}
	return newContentRigWith(t, func(d *Deps) { d.Audit = log.add }), log
}

// Moving files is a plain form post: no script, one redirect.
func TestMediaBulkMoveCreatesAFolderAndFiltersByIt(t *testing.T) {
	t.Parallel()
	c, log := newMediaRig(t)

	list := c.get(t, "owner-token", "/admin/media", "")
	has(t, list, `name="new_folder"`, `value="move"`, `Move to folder`)

	rec := c.post("/admin/media/bulk", url.Values{"action": {"move"}, "new_folder": {"Team"}, "id": {fid(51).String(), fid(52).String()}, "back": {"/admin/media"}})
	if rec.Code != 303 {
		t.Fatalf("move: %d", rec.Code)
	}
	if body := c.next(rec, "/admin/media"); !strings.Contains(body, "Moved 2 files to Team.") {
		t.Errorf("no confirmation in %q", body)
	}
	ev := log.find("media.move")
	if ev == nil || ev.Detail["count"] != 2 || ev.Detail["folder"] != "/Team" {
		t.Fatalf("audit = %+v", ev)
	}

	// The pill row lists the folder and the files outside any folder; the filter is a plain link.
	all := c.get(t, "owner-token", "/admin/media", "")
	has(t, all, `aria-label="Folders"`, `href="/admin/media?folder=Team"`, `href="/admin/media?folder=%2F"`, "All files", "Uncategorised")
	in := c.get(t, "owner-token", "/admin/media?folder=team", "")
	has(t, in, "team.png", "logo.png")
	if strings.Contains(in, "brochure.pdf") {
		t.Error("the folder filter shows a file outside the folder")
	}
	out := c.get(t, "owner-token", "/admin/media?folder=%2F", "")
	has(t, out, "brochure.pdf")
	if strings.Contains(out, "team.png") {
		t.Error("Uncategorised shows a file in a folder")
	}

	// Moving into an existing folder by a differently cased name reuses it.
	c.post("/admin/media/bulk", url.Values{"action": {"move"}, "new_folder": {"TEAM"}, "id": {fid(53).String()}})
	if got := c.media.files[2].Folder; got != "/Team" {
		t.Errorf("folder = %q, want /Team", got)
	}
}

func TestMediaBulkMoveRefusesASecondLevelAndAnEmptyChoice(t *testing.T) {
	t.Parallel()
	c, log := newMediaRig(t)
	for _, form := range []url.Values{
		{"action": {"move"}, "new_folder": {"a/b"}, "id": {fid(51).String()}},
		{"action": {"move"}, "new_folder": {"<b>"}, "id": {fid(51).String()}},
		{"action": {"move"}, "id": {fid(51).String()}},
	} {
		rec := c.post("/admin/media/bulk", form)
		if rec.Code != 303 {
			t.Fatalf("%v: %d", form, rec.Code)
		}
		body := c.next(rec, "/admin/media")
		if !strings.Contains(body, "folders cannot be nested") && !strings.Contains(body, "Choose a folder") {
			t.Errorf("%v: no reason shown", form)
		}
	}
	if log.find("media.move") != nil || c.media.files[0].Folder != "/" {
		t.Error("a refused move changed something")
	}
}

func TestMediaDetailMovesAndShowsReplace(t *testing.T) {
	t.Parallel()
	c, log := newMediaRig(t)
	page := c.get(t, "owner-token", "/admin/media/"+fid(51).String(), "")
	has(t, page, `action="/admin/media/`+fid(51).String()+`/replace"`, `enctype="multipart/form-data"`, `name="file"`, `name="folder"`, `name="new_folder"`, "Replace file")

	rec := c.post("/admin/media/"+fid(51).String(), url.Values{"alt_text": {"The team"}, "folder": {"/"}, "new_folder": {"Press kit"}})
	if rec.Code != 303 || c.media.files[0].Folder != "/Press kit" {
		t.Fatalf("detail move: %d folder %q", rec.Code, c.media.files[0].Folder)
	}
	if ev := log.find("media.move"); ev == nil || ev.Detail["folder"] != "/Press kit" {
		t.Errorf("audit = %+v", ev)
	}
	rec = c.post("/admin/media/"+fid(51).String(), url.Values{"alt_text": {"x"}, "new_folder": {"a/b"}})
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "folders cannot be nested") {
		t.Errorf("nested folder on the detail page: %d", rec.Code)
	}
}

func TestMediaReplaceKeepsTheFileAndAuditsIt(t *testing.T) {
	t.Parallel()
	c, log := newMediaRig(t)
	target := "/admin/media/" + fid(51).String() + "/replace"

	rec := c.upload(t, target, map[string][]byte{"fresh.png": png1x1}, true)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/media/"+fid(51).String() {
		t.Fatalf("replace: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if body := c.next(rec, "/admin/media/"+fid(51).String()); !strings.Contains(body, "Replaced. The address stays the same.") {
		t.Error("no success message")
	}
	ev := log.find("media.replace")
	if ev == nil || ev.Detail["from"] != int64(2048) || ev.Detail["to"] != int64(len(png1x1)) {
		t.Fatalf("audit = %+v", ev)
	}
	if len(c.media.replaced) != 1 {
		t.Errorf("Replace called %d times", len(c.media.replaced))
	}

	// Another kind of file is refused with the reason, and nothing is audited twice.
	rec = c.upload(t, target, map[string][]byte{"doc.pdf": []byte("%PDF-1.4\n%abc")}, true)
	if body := c.next(rec, "/admin/media/"+fid(51).String()); !strings.Contains(body, "replace with the same kind of file") {
		t.Errorf("no refusal reason: %s", body)
	}
	if len(c.media.replaced) != 1 {
		t.Error("a refused replace was applied")
	}
}

func TestMediaUploadReportsWhatWasRemovedFromAnSVG(t *testing.T) {
	t.Parallel()
	c, log := newMediaRig(t)
	dirty := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script><rect width="1"/></svg>`)
	rec := c.upload(t, "/admin/media/upload", map[string][]byte{"logo.svg": dirty}, true)
	loc := rec.Header().Get("Location")
	if rec.Code != 303 || !strings.HasPrefix(loc, "/admin/media?created=") {
		t.Fatalf("svg upload: %d %q", rec.Code, loc)
	}
	body := c.next(rec, loc)
	if !strings.Contains(body, "logo.svg was cleaned: removed &lt;script&gt; element.") {
		t.Errorf("no notice of what was removed in %q", body)
	}
	if ev := log.find("media.svg.upload"); ev == nil {
		t.Error("no svg audit row")
	}

	bomb := []byte(`<!DOCTYPE svg [<!ENTITY a "b">]><svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`)
	rec = c.upload(t, "/admin/media/upload", map[string][]byte{"bomb.svg": bomb}, true)
	if body := c.next(rec, "/admin/media"); !strings.Contains(body, "bomb.svg was not uploaded: ") || !strings.Contains(body, "Remove it and try again.") {
		t.Errorf("refusal not explained in %q", body)
	}
}

// The picker filters by folder with a labelled select that works as a plain GET.
func TestMediaPickerFiltersByFolder(t *testing.T) {
	t.Parallel()
	c, _ := newMediaRig(t)
	pick := func(path string) string {
		return c.do("GET", path, "owner-token", nil, map[string]string{"HX-Request": "true", "HX-Target": "hm-picker"}).Body.String()
	}
	if body := pick("/admin/media/picker"); strings.Contains(body, `name="folder"`) {
		t.Error("the folder select shows when no folder exists")
	}
	c.post("/admin/media/bulk", url.Values{"action": {"move"}, "new_folder": {"Team"}, "id": {fid(51).String()}})

	body := pick("/admin/media/picker")
	has(t, body, `<label class="hm-visually-hidden" for="picker-folder">Folder</label>`, `name="folder"`, `<option value="" selected>All folders</option>`, `<option value="Team">Team</option>`, "team.png", "logo.png")
	body = pick("/admin/media/picker?folder=Team")
	has(t, body, `<option value="Team" selected>Team</option>`, "team.png")
	if strings.Contains(body, "logo.png") {
		t.Error("the picker shows an image outside the folder")
	}
	body = pick("/admin/media/picker?folder=%2F")
	has(t, body, "logo.png")
	if strings.Contains(body, "team.png") {
		t.Error("Uncategorised shows an image in a folder")
	}
}

// Saving the detail page of a file in a legacy folder (a stored value that
// breaks today's naming rules) keeps it there and writes no move.
func TestMediaDetailSaveKeepsALegacyFolder(t *testing.T) {
	t.Parallel()
	c, log := newMediaRig(t)
	c.media.files[0].Folder = "/2024/summer"
	page := c.get(t, "owner-token", "/admin/media/"+fid(51).String(), "")
	has(t, page, `<option value="2024/summer" selected>2024/summer</option>`)
	rec := c.post("/admin/media/"+fid(51).String(), url.Values{"alt_text": {"New alt"}, "folder": {"2024/summer"}})
	if rec.Code != 303 || c.media.files[0].Folder != "/2024/summer" {
		t.Fatalf("save: %d folder %q", rec.Code, c.media.files[0].Folder)
	}
	if log.find("media.move") != nil {
		t.Error("a save without a folder change wrote a media.move row")
	}
}
