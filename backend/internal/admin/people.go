package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/person"
)

// peoplePerPage is the People list's page size.
const peoplePerPage = 50

// mountPeople registers Entities > People. A person is edited in the generic
// entity form (grouped fields, and any field a site adds to the type), so the
// list links there; only the list and "Add person" live here.
func (a *Admin) mountPeople(authed *httpx.Group) {
	g := authed.Group("", a.RequireRole("owner", "editor"))
	g.HandleFunc("GET /people", a.handlePeople)
	g.HandleFunc("GET /people/new", a.handlePersonNew)
	g.HandleFunc("GET /people/duplicates", a.handlePeopleDuplicates)
	g.HandleFunc("POST /people", a.handlePersonCreate)
	g.HandleFunc("POST /people/merge", a.handlePeopleMerge)
}

type peopleData struct {
	Table   ui.Table
	Actions []ui.Button
	Form    personNewData // the Add person dialog
}

type duplicatesData struct {
	Duplicates []dupGroup
}

// dupGroup is one set of people who look alike, with the form to merge them.
type dupGroup struct {
	Index int
	Title string
	IDs   []string
	// Items are the people side by side as radio cards; the default one to keep is checked.
	Items []ui.RadioCardItem
}

type peopleParams struct {
	q, team, sortKey, dir string
	page                  int
}

func readPeopleParams(r *http.Request) peopleParams {
	q := r.URL.Query()
	p := peopleParams{q: strings.TrimSpace(q.Get("q")), team: q.Get("team"), sortKey: q.Get("sort"), dir: q.Get("dir")}
	p.page, _ = strconv.Atoi(q.Get("page"))
	if p.page < 1 {
		p.page = 1
	}
	if p.team != "yes" && p.team != "no" {
		p.team = ""
	}
	switch p.sortKey {
	case "name", "job_title", "posts":
	default:
		p.sortKey = "name"
	}
	if p.dir != "asc" && p.dir != "desc" {
		p.dir = "asc"
		if p.sortKey == "posts" {
			p.dir = "desc"
		}
	}
	return p
}

// query is the URL state the links keep (everything but the page).
func (p peopleParams) query() url.Values {
	v := url.Values{}
	if p.q != "" {
		v.Set("q", p.q)
	}
	if p.team != "" {
		v.Set("team", p.team)
	}
	if p.sortKey != "name" || p.dir != "asc" {
		v.Set("sort", p.sortKey)
		v.Set("dir", p.dir)
	}
	return v
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func (a *Admin) handlePeople(w http.ResponseWriter, r *http.Request) {
	if a.deps.People == nil {
		a.unavailable(w, r, "People")
		return
	}
	ctx := r.Context()
	p := readPeopleParams(r)
	t := ui.Table{ID: "people", Caption: "People", BaseURL: a.adminPath + "/people", CSRF: csrfToken(ctx), SortKey: p.sortKey, SortDir: p.dir, Query: p.q, SearchLabel: "Search people"}
	var dups []dupGroup
	render := func(status int) {
		v := a.NewView(w, r, "People")
		v.entityNav, v.entityManage = a.peopleSubnav(ctx)
		var actions []ui.Button
		if len(dups) > 0 {
			actions = append(actions, ui.Button{Label: "Possible duplicates (" + strconv.Itoa(len(dups)) + ")", Href: a.adminPath + "/people/duplicates"})
		}
		actions = append(actions, ui.Button{Label: "Add person", Href: a.adminPath + "/people/new", Variant: "primary", Icon: "plus", Attrs: map[string]string{"data-hm-open": "new-person"}})
		v.Data = peopleData{Table: t, Actions: actions}
		a.Render(w, r, status, "people/list", v)
	}
	list, err := a.deps.People.List(ctx)
	if err != nil {
		a.logger().Error("list people failed", "err", err)
		t.Error = "The people could not be loaded."
		render(http.StatusOK)
		return
	}
	// The counts and logins are decoration: without them the list still works.
	posts, err := a.deps.People.PostCounts(ctx)
	if err != nil {
		a.logger().Error("count posts per person failed", "err", err)
	}
	linked, err := a.deps.People.LinkedUsers(ctx)
	if err != nil {
		a.logger().Error("read person logins failed", "err", err)
	}

	dups = duplicateGroups(list, posts, linked)

	needle := strings.ToLower(p.q)
	var rows []*person.Person
	var onTeam, off int
	for _, pe := range list {
		if needle != "" && !strings.Contains(strings.ToLower(pe.Name+" "+pe.JobTitle), needle) {
			continue
		}
		if pe.OnTeam {
			onTeam++
		} else {
			off++
		}
		if (p.team == "yes" && !pe.OnTeam) || (p.team == "no" && pe.OnTeam) {
			continue
		}
		rows = append(rows, pe)
	}
	sortPeople(rows, p, posts)

	t.PrimaryFilters = []ui.Filter{{Name: "team", Label: "On the team", Options: []ui.Option{
		{Value: "", Label: "Everyone (" + strconv.Itoa(onTeam+off) + ")", Selected: p.team == ""},
		{Value: "yes", Label: "On the team (" + strconv.Itoa(onTeam) + ")", Selected: p.team == "yes"},
		{Value: "no", Label: "Not on the team (" + strconv.Itoa(off) + ")", Selected: p.team == "no"},
	}}}
	t.Columns = []ui.Col{
		{Key: "name", Label: "Name", Sortable: true, Role: "title"},
		{Key: "job_title", Label: "Job title", Sortable: true, WideOnly: true},
		{Key: "login", Label: "Login", WideOnly: true},
		{Key: "public", Label: "Public"},
		{Key: "on_team", Label: "On team", WideOnly: true},
		{Key: "posts", Label: "Posts", Sortable: true, Role: "num"},
	}
	t.Empty = ui.Empty{Icon: "users", Title: "No people match", Body: "People are the authors of your posts and the faces of your site. Add the first one.",
		Actions: []ui.Button{{Label: "Add person", Variant: "primary", Href: a.adminPath + "/people/new"}}}

	total := len(rows)
	from := min((p.page-1)*peoplePerPage, max(total-1, 0)/peoplePerPage*peoplePerPage)
	page := from/peoplePerPage + 1
	rows = rows[from:min(from+peoplePerPage, total)]
	t.Page = ui.Pagination{Page: page, PerPage: peoplePerPage, Total: total, BaseURL: a.adminPath + "/people", Query: p.query()}

	// Account emails are for owners, as on the users screen; an editor sees only
	// whether a person has a login.
	owner := userFrom(ctx) != nil && userFrom(ctx).Role == "owner"
	logins := map[uuid.UUID]string{}
	for _, pe := range rows {
		uid, ok := linked[pe.ID]
		if !ok {
			continue
		}
		logins[pe.ID] = "Yes"
		if owner && a.deps.Users != nil {
			if u, err := a.deps.Users.GetByID(ctx, uid); err == nil {
				logins[pe.ID] = u.Email
			}
		}
	}
	for _, pe := range rows {
		id := pe.ID.String()
		edit := a.adminPath + "/entities/" + id
		path := ""
		if pe.Public {
			path = a.personPath(ctx, pe.Slug)
		}
		_, hasLogin := linked[pe.ID]
		row := ui.Row{ID: id, Label: pe.Name, Cells: []ui.Cell{
			{Text: pe.Name, Link: edit, Sub: path},
			{Text: dashIfEmpty(pe.JobTitle)},
			{Text: dashIfEmpty(logins[pe.ID])},
			{Text: yesNo(pe.Public)},
			{Text: yesNo(pe.OnTeam)},
			{Text: strconv.Itoa(posts[pe.ID])},
		}, Actions: []ui.Action{{Label: "Edit", Href: edit}}}
		if path != "" {
			row.Actions = append(row.Actions, ui.Action{Label: "View on site", Href: path})
		}
		row.Actions = append(row.Actions, ui.Action{Label: "Delete", Href: edit + "/delete?back=" + url.QueryEscape(r.URL.RequestURI()), Post: edit + "/delete", Danger: true,
			ConfirmLabel: "Delete person", Confirm: deletePersonConfirm(pe.Name, hasLogin)})
		t.Rows = append(t.Rows, row)
	}
	render(http.StatusOK)
}

// handlePeopleDuplicates lists the groups of people who look alike, each with
// its merge form. The People list only links here, so the list stays first.
func (a *Admin) handlePeopleDuplicates(w http.ResponseWriter, r *http.Request) {
	if a.deps.People == nil {
		a.unavailable(w, r, "People")
		return
	}
	ctx := r.Context()
	list, err := a.deps.People.List(ctx)
	if err != nil {
		a.serverError(w, r, "The people could not be loaded", err)
		return
	}
	posts, _ := a.deps.People.PostCounts(ctx)
	linked, _ := a.deps.People.LinkedUsers(ctx)
	v := a.NewView(w, r, "Possible duplicates")
	v.entityNav, v.entityManage = a.peopleSubnav(r.Context())
	v.Crumbs = []Crumb{{Label: "Entities", Href: a.adminPath + "/entities"}, {Label: "People", Href: a.adminPath + "/people"}, {Label: "Possible duplicates"}}
	v.Data = duplicatesData{Duplicates: duplicateGroups(list, posts, linked)}
	a.Render(w, r, http.StatusOK, "people/duplicates", v)
}

// deletePersonConfirm is the text of the Delete confirmation. A person with a
// login loses it: that user is asked to complete a new, empty profile.
func deletePersonConfirm(name string, hasLogin bool) string {
	msg := "“" + name + "” will be deleted. Their posts keep the name as plain text."
	if hasLogin {
		msg += " This person has a login: that account loses its profile and will be asked to fill in a new one."
	}
	return msg + " This cannot be undone."
}

func sortPeople(rows []*person.Person, p peopleParams, posts map[uuid.UUID]int) {
	cmp := func(x, y *person.Person) int {
		switch p.sortKey {
		case "job_title":
			return strings.Compare(strings.ToLower(x.JobTitle), strings.ToLower(y.JobTitle))
		case "posts":
			return posts[x.ID] - posts[y.ID]
		}
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	}
	slices.SortStableFunc(rows, func(x, y *person.Person) int {
		c := cmp(x, y)
		if p.dir == "desc" {
			c = -c
		}
		if c == 0 {
			c = strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
		}
		return c
	})
}

type personNewData struct {
	Name, NameErr string
	Summary       []ui.FieldError
}

func (a *Admin) renderPersonNew(w http.ResponseWriter, r *http.Request, status int, d personNewData) {
	v := a.NewView(w, r, "Add person")
	v.entityNav, v.entityManage = a.peopleSubnav(r.Context())
	v.Crumbs = []Crumb{{Label: "People", Href: a.adminPath + "/people"}, {Label: "Add person"}}
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, "people/new", v)
}

func (a *Admin) handlePersonNew(w http.ResponseWriter, r *http.Request) {
	if a.deps.People == nil {
		a.unavailable(w, r, "People")
		return
	}
	a.renderPersonNew(w, r, http.StatusOK, personNewData{})
}

func (a *Admin) handlePersonCreate(w http.ResponseWriter, r *http.Request) {
	if a.deps.People == nil {
		a.unavailable(w, r, "People")
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	p, err := a.deps.People.Create(r.Context(), actorID(r), name)
	var verr *person.ValidationError
	if errors.As(err, &verr) {
		d := personNewData{Name: name, NameErr: verr.Fields["name"]}
		d.Summary = []ui.FieldError{{ID: "person-name", Label: "Name", Message: d.NameErr}}
		a.renderPersonNew(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	if err != nil {
		a.serverError(w, r, "The person could not be added", err)
		return
	}
	a.SetFlash(w, r, "success", "Person added. Add a photo and a bio next.")
	http.Redirect(w, r, a.adminPath+"/entities/"+p.ID.String(), http.StatusSeeOther)
}

// peopleSubnav is the Entities subnav on the People pages, with People current.
// Without the content store it is empty and the page shows no subnav.
func (a *Admin) peopleSubnav(ctx context.Context) ([]EntityNavEntry, bool) {
	c := a.deps.Content
	if c.Entities == nil {
		return nil, false
	}
	schemas, err := c.Entities.ListSchemas(ctx)
	if err != nil {
		a.logger().Error("list entity types for the subnav failed", "err", err)
		return nil, false
	}
	owner := false
	if u := userFrom(ctx); u != nil && u.Role == "owner" {
		owner = true
	}
	return entitiesNavIn(a.adminPath, a.entityCounts(ctx, schemas, nil).types, person.SchemaSlug), owner
}

// duplicateGroups turns the people who look alike into merge forms: each group
// asks which one to keep, and names the posts and the login of every member.
func duplicateGroups(list []*person.Person, posts map[uuid.UUID]int, linked map[uuid.UUID]uuid.UUID) []dupGroup {
	var out []dupGroup
	for i, g := range person.Duplicates(list) {
		d := dupGroup{Index: i + 1}
		names := make([]string, 0, len(g))
		// The person with the most posts (a login counts as one more) is the
		// safest default to keep.
		best, bestScore := 0, -1
		for n, pe := range g {
			score := posts[pe.ID] * 2
			if _, ok := linked[pe.ID]; ok {
				score++
			}
			if score > bestScore {
				best, bestScore = n, score
			}
		}
		for n, pe := range g {
			d.IDs = append(d.IDs, pe.ID.String())
			names = append(names, pe.Name)
			desc := countOf(posts[pe.ID], "post")
			if _, ok := linked[pe.ID]; ok {
				desc += " · has a login"
			}
			d.Items = append(d.Items, ui.RadioCardItem{Value: pe.ID.String(), Title: pe.Name, Description: desc, Checked: n == best, Initials: initialsOf(pe.Name)})
		}
		d.Title = strings.Join(names, ", ")
		out = append(out, d)
	}
	return out
}

// handlePeopleMerge merges the people of one duplicate group into the one to
// keep. The page lists only groups it found itself, but the ids come from the
// browser, so each merge is checked by the service (both must be people, and
// not both with a login).
func (a *Admin) handlePeopleMerge(w http.ResponseWriter, r *http.Request) {
	if a.deps.People == nil {
		a.unavailable(w, r, "People")
		return
	}
	keep, err := uuid.Parse(r.PostFormValue("keep"))
	if err != nil {
		a.flashAndBack(w, r, "error", "Choose the person to keep.")
		return
	}
	var drops []uuid.UUID
	for _, raw := range r.PostForm["ids"] {
		if id, err := uuid.Parse(raw); err == nil && id != keep {
			drops = append(drops, id)
		}
	}
	if len(drops) == 0 {
		a.flashAndBack(w, r, "error", "There is nobody to merge into that person.")
		return
	}
	var merged, posts int
	var kept, warn string
	for _, id := range drops {
		res, err := a.deps.People.MergePeople(r.Context(), keep, id)
		switch {
		case errors.Is(err, person.ErrMergeBothLogins):
			a.flashAndBack(w, r, "error", "Two people with a login cannot be merged, because one account would lose its profile.")
			return
		case errors.Is(err, person.ErrNotFound):
			a.flashAndBack(w, r, "error", "One of those people no longer exists. The list is up to date now.")
			return
		case res != nil && err != nil:
			// Merged, but the redirect failed: say so, carry on.
			a.logger().Error("merge people: redirect", "err", err)
			warn = " " + sentence(err.Error())
		case err != nil:
			a.serverError(w, r, "The people could not be merged", err)
			return
		}
		if res != nil {
			merged++
			posts += res.Posts
			kept = res.Keep.Name
		}
	}
	if a.deps.Invalidate != nil {
		a.deps.Invalidate(r.Context())
	}
	kind := "success"
	if warn != "" {
		kind = "error"
	}
	a.SetFlash(w, r, kind, mergeMessage(merged, posts, kept)+warn)
	http.Redirect(w, r, a.mergeHome(r), http.StatusSeeOther)
}

func (a *Admin) flashAndBack(w http.ResponseWriter, r *http.Request, kind, msg string) {
	a.SetFlash(w, r, kind, msg)
	http.Redirect(w, r, a.mergeHome(r), http.StatusSeeOther)
}

// mergeHome is where to go after a merge: back to the duplicates while some
// groups remain, else the People list.
func (a *Admin) mergeHome(r *http.Request) string {
	ctx := r.Context()
	if list, err := a.deps.People.List(ctx); err == nil {
		posts, _ := a.deps.People.PostCounts(ctx)
		linked, _ := a.deps.People.LinkedUsers(ctx)
		if len(duplicateGroups(list, posts, linked)) > 0 {
			return a.adminPath + "/people/duplicates"
		}
	}
	return a.adminPath + "/people"
}

func mergeMessage(merged, posts int, kept string) string {
	who := "person"
	if merged != 1 {
		who = "people"
	}
	verb := "credits"
	if posts != 1 {
		verb = "credit"
	}
	return "Merged " + strconv.Itoa(merged) + " " + who + " into " + kept + ". " + countOf(posts, "post") + " now " + verb + " " + kept + "."
}

// countOf is "1 post", "3 posts".
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
