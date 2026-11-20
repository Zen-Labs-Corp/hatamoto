package admin

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
)

const (
	cmtrBlocklistsPath  = "/settings/comments/blocklists"
	cmtrBlocklistsLimit = 5000
	cmtrDomainMax       = 253
)

// cmtrBlocklistKinds are the blocklist kinds in page order. The domain kinds
// are edited here; the hash kinds are learned from Mark as spam and can only
// be cleared.
var cmtrBlocklistKinds = []struct {
	Kind, Title, Help string
	Editable          bool
}{
	{"email_domain", "Email domains", "One domain per line, e.g. example.test. Includes the built-in list of disposable-email domains.", true},
	{"link_domain", "Link domains", "Comments linking to these domains are marked as spam.", true},
	{"body_hash", "Remembered spam texts", "Learned from Mark as spam.", false},
	{"ip_hash", "Blocked addresses", "Learned from Mark as spam. Stored as hashes; raw addresses are never kept.", false},
	{"net_hash", "Blocked networks", "Learned from Mark as spam.", false},
}

// cmtrBlocklistCard is one blocklist card. Learned kinds carry a count only.
type cmtrBlocklistCard struct {
	ID, Kind, Title, Help string
	Editable              bool
	Value, Error          string
	Count                 string
	HasEntries            bool
}

type cmtrBlocklistsData struct {
	Supported bool
	Summary   []ui.FieldError
	Cards     []cmtrBlocklistCard
}

func cmtrBlocklistKind(kind string) (editable, ok bool) {
	for _, k := range cmtrBlocklistKinds {
		if k.Kind == kind {
			return k.Editable, true
		}
	}
	return false, false
}

func (a *Admin) handleCommentBlocklists(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
		return
	}
	a.renderCmtrBlocklists(w, r, svc, http.StatusOK, "", "", nil)
}

// renderCmtrBlocklists draws the page. After a refused save, kind and text
// are the submitted entries of that card and errs the problems found.
func (a *Admin) renderCmtrBlocklists(w http.ResponseWriter, r *http.Request, svc CommentsService, status int, kind, text string, errs []ui.FieldError) {
	d := cmtrBlocklistsData{Supported: svc.Supports("blocklists"), Summary: errs}
	if d.Supported {
		lists, err := svc.Blocklists(r.Context())
		if err != nil {
			a.logger().Error("comment blocklists", "err", err)
			a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The blocklists could not be loaded.")
			return
		}
		byKind := map[string]Blocklist{}
		for _, l := range lists {
			byKind[l.Kind] = l
		}
		for _, k := range cmtrBlocklistKinds {
			l := byKind[k.Kind]
			c := cmtrBlocklistCard{ID: "bl-" + k.Kind, Kind: k.Kind, Title: k.Title, Help: k.Help, Editable: k.Editable}
			if k.Editable {
				c.Value = strings.Join(l.Entries, "\n")
				if k.Kind == kind {
					c.Value = text
					if len(errs) > 0 {
						c.Error = errs[0].Message
					}
				}
			} else {
				c.HasEntries = l.Count > 0
				c.Count = fmt.Sprintf("%d entries", l.Count)
				if l.Count == 1 {
					c.Count = "1 entry"
				}
			}
			d.Cards = append(d.Cards, c)
		}
	}
	v := a.NewView(w, r, "Comment blocklists")
	v.Section, v.Tab = "comments", "comment-blocklists"
	v.Data = d
	a.Render(w, r, status, "settings/comments-blocklists", v)
}

func (a *Admin) handleCommentBlocklistsSave(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
		return
	}
	if !svc.Supports("blocklists") {
		a.Error(w, r, http.StatusNotFound, "Not found", "Blocklists are not available yet.")
		return
	}
	_ = r.ParseForm() // a malformed body leaves the form empty and fails the kind check
	kind := r.PostForm.Get("kind")
	editable, ok := cmtrBlocklistKind(kind)
	clear := r.PostForm.Get("clear") == "1"
	if !ok || (!editable && !clear) {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "That blocklist cannot be changed.")
		return
	}
	var entries []string
	if !clear {
		text := r.PostForm.Get("entries")
		var errs []ui.FieldError
		entries, errs = cmtrParseDomains(text, "bl-"+kind+"-entries")
		if len(errs) > 0 {
			a.renderCmtrBlocklists(w, r, svc, http.StatusUnprocessableEntity, kind, text, errs)
			return
		}
	}
	if err := svc.SaveBlocklist(r.Context(), kind, entries, cmtrActor(r)); err != nil {
		a.logger().Error("comment blocklist save", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The blocklist could not be saved.")
		return
	}
	a.audit(r, "comments.blocklist", kind, map[string]any{"count": len(entries)})
	msg := "Blocklist saved."
	if clear {
		msg = "Blocklist cleared."
	}
	a.SetFlash(w, r, "success", msg)
	Redirect(w, r, a.adminPath+cmtrBlocklistsPath)
}

// cmtrParseDomains reads one domain per line: trimmed, lower case, without a
// scheme, a path or a leading "@", deduplicated. Each bad line is reported by
// its number.
func cmtrParseDomains(text, fieldID string) ([]string, []ui.FieldError) {
	var out []string
	var errs []ui.FieldError
	seen := map[string]bool{}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		d := strings.ToLower(strings.TrimSpace(line))
		if d == "" {
			continue
		}
		if j := strings.Index(d, "://"); j >= 0 {
			d = d[j+3:]
		}
		d = strings.TrimPrefix(d, "@")
		if j := strings.IndexAny(d, "/?#"); j >= 0 {
			d = d[:j]
		}
		if !cmtrDomainShape(d) {
			errs = append(errs, ui.FieldError{ID: fieldID, Label: "Line " + strconv.Itoa(i+1), Message: "Line " + strconv.Itoa(i+1) + " is not a domain"})
			continue
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	if len(errs) == 0 && len(out) > cmtrBlocklistsLimit {
		errs = append(errs, ui.FieldError{ID: fieldID, Label: "Entries", Message: "A list holds at most " + strconv.Itoa(cmtrBlocklistsLimit) + " entries"})
	}
	return out, errs
}

// cmtrDomainShape reports whether d is made of dot separated labels of
// letters, digits and hyphens, with at least one dot.
func cmtrDomainShape(d string) bool {
	if len(d) > cmtrDomainMax || !strings.Contains(d, ".") {
		return false
	}
	for _, l := range strings.Split(d, ".") {
		if l == "" || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range l {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// handleCommenters is a stub until its screen lands.
func (a *Admin) handleCommenters(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommenterAction is a stub until its screen lands.
func (a *Admin) handleCommenterAction(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommenterExport is a stub until its screen lands.
func (a *Admin) handleCommenterExport(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommenterErasePage is a stub until its screen lands.
func (a *Admin) handleCommenterErasePage(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// handleCommenterErase is a stub until its screen lands.
func (a *Admin) handleCommenterErase(w http.ResponseWriter, r *http.Request) {
	a.unavailable(w, r, "Comments")
}

// cmtrActor is the signed-in user's id for the service's audit trail.
func cmtrActor(r *http.Request) string {
	if u := userFrom(r.Context()); u != nil {
		return u.ID
	}
	return ""
}
