package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
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
		a.SetFlash(w, r, "error", "Blocklists are not available yet.")
		Redirect(w, r, a.adminPath+cmtrBlocklistsPath)
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
		entries, errs = cmtrParseDomains(text, "bl-"+kind+"-entries", a.cmtrStoredEntries(r.Context(), svc, kind))
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

// cmtrStoredEntries is what the kind already holds. Spam learning stores hosts
// the shape check refuses (IDN, single label), and saving the card must not
// fail on lines the owner never typed.
func (a *Admin) cmtrStoredEntries(ctx context.Context, svc CommentsService, kind string) map[string]bool {
	lists, err := svc.Blocklists(ctx)
	if err != nil {
		return nil
	}
	known := map[string]bool{}
	for _, l := range lists {
		if l.Kind == kind {
			for _, e := range l.Entries {
				known[e] = true
			}
		}
	}
	return known
}

// cmtrParseDomains reads one domain per line: trimmed, lower case, without a
// scheme, a path or a leading "@", deduplicated. Each bad line is reported by
// its number. A line the store already holds (known) is kept as it is.
func cmtrParseDomains(text, fieldID string, known map[string]bool) ([]string, []ui.FieldError) {
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
		if !known[d] && !cmtrDomainShape(d) {
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

const cmtrCommentersPath = "/settings/comments/commenters"

type cmtrCommentersData struct {
	Table              ui.Table
	PrevHref, NextHref string
}

func (a *Admin) handleCommenters(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page := pageParam(r)
	list, err := svc.Commenters(r.Context(), q, page)
	if err != nil {
		a.logger().Error("comment commenters", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The commenters could not be loaded.")
		return
	}
	base := a.adminPath + cmtrCommentersPath
	query := url.Values{}
	if q != "" {
		query.Set("q", q)
	}
	pageHref := func(n int) string {
		v := url.Values{}
		for k, vs := range query {
			v[k] = vs
		}
		if n > 1 {
			v.Set("page", strconv.Itoa(n))
		}
		if len(v) == 0 {
			return base
		}
		return base + "?" + v.Encode()
	}
	here := pageHref(page)
	back := "?back=" + url.QueryEscape(here)
	t := ui.Table{
		ID: "commenters", Caption: "Commenters", BaseURL: base, CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "name", Label: "Name", Role: "title"}, {Key: "identity", Label: "Identity"}, {Key: "trust", Label: "Trust"}, {Key: "approved", Label: "Approved", Role: "num"}, {Key: "seen", Label: "Last seen"}},
		Query:   q, SearchLabel: "Search commenters",
		Empty: ui.Empty{Icon: "users", Title: "No commenters", Body: "People who comment on your site show up here."},
	}
	privacy := a.cmtrPrivacy(r, svc)
	for _, c := range list.Rows {
		name := c.Name
		if name == "" {
			name = "Unnamed"
		}
		seen := "Never"
		if !c.LastSeen.IsZero() {
			seen = fmtDate(c.LastSeen.UTC())
		}
		act := a.adminPath + cmtrCommentersPath + "/" + c.ID
		var acts []ui.Action
		if c.Trust != "trusted" && c.Trust != "blocked" {
			acts = append(acts, ui.Action{Label: "Vouch", Post: act + "/vouch" + back})
		}
		acts = append(acts,
			ui.Action{Label: "Comments", Href: a.adminPath + "/comments?" + url.Values{"status": {"approved"}, "commenter": {c.ID}}.Encode()},
			ui.Action{Label: "Sign out all devices", Href: act + "/signout" + back, Post: act + "/signout" + back, ConfirmLabel: "Sign out",
				Confirm: "Sign this commenter out on every device? They sign in again next time. Their trust level stays."},
		)
		if privacy {
			acts = append(acts, ui.Action{Label: "Export", Href: act + "/export"})
		}
		if c.Trust != "blocked" {
			acts = append(acts, ui.Action{Label: "Block", Href: act + "/block" + back, Post: act + "/block" + back, Danger: true, ConfirmLabel: "Block commenter",
				Confirm: "Block this commenter? Their comments will be held and never shown."})
		}
		if privacy {
			acts = append(acts, ui.Action{Label: "Erase", Href: act + "/erase", Danger: true})
		}
		identity := "Email"
		if c.Origin != "" {
			identity = "plugin: " + c.Origin
		}
		t.Rows = append(t.Rows, ui.Row{ID: c.ID, Label: "commenter " + name, Cells: []ui.Cell{
			{Text: name}, {Text: identity}, {Badge: cmtrTrustBadge(c.Trust)}, {Text: strconv.Itoa(c.ApprovedCount)}, {Text: seen, Nowrap: true},
		}, Actions: acts})
	}
	d := cmtrCommentersData{Table: t}
	if page > 1 {
		d.PrevHref = pageHref(page - 1)
	}
	if list.HasMore {
		d.NextHref = pageHref(page + 1)
	}
	v := a.NewView(w, r, "Commenters")
	v.Section, v.Tab = "comments", "commenters"
	v.Data = d
	a.Render(w, r, http.StatusOK, "settings/comments-commenters", v)
}

// cmtrTrustBadge names a trust level. Blocked is the only coloured one.
func cmtrTrustBadge(trust string) *ui.Badge {
	label := "New"
	if trust != "" {
		label = strings.ToUpper(trust[:1]) + trust[1:]
	}
	kind := "neutral"
	switch trust {
	case "blocked":
		kind = "danger"
	case "trusted":
		kind = "success"
	case "known":
		kind = "info"
	}
	return &ui.Badge{Label: label, Kind: kind}
}

// handleCommenterAction runs vouch, block or signout for one commenter.
func (a *Admin) handleCommenterAction(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
		return
	}
	id, action := r.PathValue("id"), r.PathValue("action")
	if _, err := uuid.Parse(id); err != nil || (action != "vouch" && action != "block" && action != "signout") {
		a.Error(w, r, http.StatusNotFound, "Not found", "That commenter action does not exist.")
		return
	}
	var (
		err error
		msg string
	)
	switch action {
	case "vouch":
		_, err = svc.Vouch(r.Context(), id, cmtrActor(r))
		msg = "Commenter is now known."
	case "block":
		err = svc.Block(r.Context(), id, cmtrActor(r))
		msg = "Commenter blocked."
	case "signout":
		err = svc.RevokeDevices(r.Context(), id)
		msg = "Commenter signed out on every device."
	}
	if errors.Is(err, ErrCommentNotFound) {
		a.Error(w, r, http.StatusNotFound, "Not found", "That commenter does not exist.")
		return
	}
	if errors.Is(err, ErrCommentBlocked) {
		a.SetFlash(w, r, "error", "A blocked commenter cannot be vouched for.")
		Redirect(w, r, backTo(r, a.adminPath+cmtrCommentersPath, a.adminPath+cmtrCommentersPath))
		return
	}
	if err != nil {
		a.logger().Error("comment commenter action", "action", action, "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The commenter could not be changed.")
		return
	}
	a.audit(r, "comments.commenter."+action, id, map[string]any{})
	a.SetFlash(w, r, "success", msg)
	Redirect(w, r, backTo(r, a.adminPath+cmtrCommentersPath, a.adminPath+cmtrCommentersPath))
}

// cmtrConfirms are the actions that ask first. The dialog is the JS path;
// the confirm page is the same question without JS.
var cmtrConfirms = map[string]struct{ Title, Body, Label string }{
	"block":   {"Block commenter", "Block this commenter? Their comments will be held and never shown.", "Block commenter"},
	"signout": {"Sign out all devices", "Sign this commenter out on every device? They sign in again next time. Their trust level stays.", "Sign out"},
}

type cmtrConfirmData struct{ Title, Body, Label, Action, Back string }

// handleCommenterConfirm is the no-JS confirm step of block and sign out.
func (a *Admin) handleCommenterConfirm(w http.ResponseWriter, r *http.Request) {
	if a.deps.Comments == nil {
		a.unavailable(w, r, "Comments")
		return
	}
	id, action := r.PathValue("id"), r.PathValue("action")
	c, known := cmtrConfirms[action]
	if _, err := uuid.Parse(id); err != nil || !known {
		a.Error(w, r, http.StatusNotFound, "Not found", "That commenter action does not exist.")
		return
	}
	list := a.adminPath + cmtrCommentersPath
	v := a.NewView(w, r, c.Title)
	v.Section, v.Tab = "comments", "commenters"
	v.Width = "compact"
	v.Data = cmtrConfirmData{Title: c.Title, Body: c.Body, Label: c.Label, Action: list + "/" + id + "/" + action, Back: backTo(r, list, list)}
	a.Render(w, r, http.StatusOK, "settings/comments-commenter-confirm", v)
}

// cmtrPrivacy reports whether the privacy actions (export, erase) apply: the
// user may export commenters and the comment service supports them.
func (a *Admin) cmtrPrivacy(r *http.Request, svc CommentsService) bool {
	return a.can(r.Context(), authz.ActionCommentCommenterExport) && svc.Supports("privacy")
}

// cmtrPrivacyID answers the privacy routes: the commenter id, or false after
// writing the refusal (not allowed to act, no service, no privacy support, bad id).
func (a *Admin) cmtrPrivacyID(w http.ResponseWriter, r *http.Request, act authz.Action) (CommentsService, string, bool) {
	if !a.requireCan(w, r, act) {
		return nil, "", false
	}
	svc := a.deps.Comments
	if svc == nil {
		a.unavailable(w, r, "Comments")
		return nil, "", false
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil || !svc.Supports("privacy") {
		a.Error(w, r, http.StatusNotFound, "Not found", "Export and erase are not available for that commenter.")
		return nil, "", false
	}
	return svc, id, true
}

// handleCommenterExport downloads everything stored about one commenter.
func (a *Admin) handleCommenterExport(w http.ResponseWriter, r *http.Request) {
	svc, id, ok := a.cmtrPrivacyID(w, r, authz.ActionCommentCommenterExport)
	if !ok {
		return
	}
	b, err := svc.ExportCommenter(r.Context(), id)
	if errors.Is(err, ErrCommentsUnsupported) || errors.Is(err, ErrCommentNotFound) {
		a.Error(w, r, http.StatusNotFound, "Not found", "That commenter cannot be exported.")
		return
	}
	if err != nil {
		a.logger().Error("comment commenter export", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The commenter could not be exported.")
		return
	}
	a.audit(r, "comments.commenter.export", id, nil)
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Disposition", `attachment; filename="commenter-`+id+`.json"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

type cmtrEraseData struct{ Danger ui.DangerConfirm }

// cmtrEraseDanger is the typed confirm of the erase page.
func (a *Admin) cmtrEraseDanger(r *http.Request, id string) ui.DangerConfirm {
	list := a.adminPath + cmtrCommentersPath
	return ui.DangerConfirm{
		ID:     "dc-erase-commenter",
		Title:  "Erase commenter",
		Phrase: "erase",
		Prompt: "To erase this commenter, type erase below.",
		Action: list + "/" + id + "/erase",
		Back:   list,
		Label:  "Erase commenter",
		CSRF:   csrfToken(r.Context()),
		Body: []ui.DangerText{
			{Parts: []ui.DangerPart{{Text: "Comments nobody replied to are deleted. Comments with replies keep their place in the thread, without the name or the text. The commenter record is deleted."}}},
			{Parts: []ui.DangerPart{{Text: "This cannot be undone."}}},
		},
	}
}

func (a *Admin) renderCmtrErase(w http.ResponseWriter, r *http.Request, status int, dc ui.DangerConfirm) {
	v := a.NewView(w, r, "Erase commenter")
	v.Section, v.Tab = "comments", "commenters"
	v.Width = "compact"
	v.Data = cmtrEraseData{Danger: dc}
	a.Render(w, r, status, "settings/comments-erase", v)
}

func (a *Admin) handleCommenterErasePage(w http.ResponseWriter, r *http.Request) {
	_, id, ok := a.cmtrPrivacyID(w, r, authz.ActionCommentCommenterErase)
	if !ok {
		return
	}
	a.renderCmtrErase(w, r, http.StatusOK, a.cmtrEraseDanger(r, id))
}

func (a *Admin) handleCommenterErase(w http.ResponseWriter, r *http.Request) {
	svc, id, ok := a.cmtrPrivacyID(w, r, authz.ActionCommentCommenterErase)
	if !ok {
		return
	}
	dc, ok := CheckDangerPhrase(r, a.cmtrEraseDanger(r, id))
	if !ok {
		a.renderCmtrErase(w, r, http.StatusUnprocessableEntity, dc)
		return
	}
	deleted, anonymised, err := svc.EraseCommenter(r.Context(), id)
	if errors.Is(err, ErrCommentsUnsupported) || errors.Is(err, ErrCommentNotFound) {
		a.Error(w, r, http.StatusNotFound, "Not found", "That commenter cannot be erased.")
		return
	}
	if err != nil {
		a.logger().Error("comment commenter erase", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The commenter could not be erased.")
		return
	}
	a.audit(r, "comments.commenter.erase", id, map[string]any{"deleted": deleted, "anonymised": anonymised})
	a.SetFlash(w, r, "success", fmt.Sprintf("Commenter erased: %d comments deleted, %d anonymised.", deleted, anonymised))
	Redirect(w, r, a.adminPath+cmtrCommentersPath)
}

// cmtrActor is the signed-in user's id for the service's audit trail.
func cmtrActor(r *http.Request) string {
	if u := userFrom(r.Context()); u != nil {
		return u.ID
	}
	return ""
}
