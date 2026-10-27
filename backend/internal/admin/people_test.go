package admin

import (
	"context"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/person"
)

// peopleRig has a complete owner (Olga, linked, 81 posts), a loose public team
// member, a loose hidden person, and the editor complete as well.
func peopleRig(t *testing.T) (*ownerRig, *fakePeople) {
	t.Helper()
	g := newOwnerRig(t, false)
	people := newFakePeople()
	olga := &person.Person{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000a1"), Slug: "olga", Name: "Owner Olga", JobTitle: "Founder", Photo: "/m/o.jpg", Public: true, OnTeam: true, Links: map[string]string{}}
	edie := &person.Person{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000a2"), Slug: "edie", Name: "Edie Editor", Photo: "/m/e.jpg", Links: map[string]string{}}
	people.set(g.owner.ID, olga)
	people.set(g.editor.ID, edie)
	sasha := &person.Person{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000a3"), Slug: "sasha", Name: "Sasha Reid", JobTitle: "Director", Public: true, OnTeam: true, Links: map[string]string{}}
	jason := &person.Person{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000a4"), Slug: "jason", Name: "Jason Holder", Links: map[string]string{}}
	people.loose = []*person.Person{sasha, jason}
	people.posts[olga.ID], people.posts[sasha.ID], people.posts[jason.ID] = 81, 44, 1
	g.a.deps.People = people
	return g, people
}

func TestPeopleListColumnsAndRows(t *testing.T) {
	t.Parallel()

	g, _ := peopleRig(t)
	rec := g.do("GET", "/admin/people", "owner-token", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d\n%s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, col := range []string{"Name", "Job title", "Login", "Public", "On team", "Posts"} {
		if !strings.Contains(body, ">"+col+"<") && !strings.Contains(body, ">"+col+" ") {
			t.Errorf("no %q column", col)
		}
	}
	for _, want := range []string{"Owner Olga", "Founder", "owner@example.com", "Sasha Reid", "Jason Holder", ">81<", ">44<", "Add person", `href="/admin/people/new"`, "/author/olga"} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	// A person without a login shows no email; a hidden person has no public path.
	if strings.Contains(body, "/author/jason") {
		t.Error("a hidden person shows a public path")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	if strings.ContainsAny(body, "\u2013\u2014") {
		t.Error("em or en dash in the People page")
	}
	compareGolden(t, "pages/people-list.html", normalizePage(body))
}

// Deleting a person with a login says what happens to that account.
func TestPeopleDeleteConfirmWarnsAboutALogin(t *testing.T) {
	t.Parallel()

	g, _ := peopleRig(t)
	body := g.do("GET", "/admin/people", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "that account loses its profile") {
		t.Error("no warning for deleting a person with a login")
	}
	if strings.Count(body, "that account loses its profile") != 2 { // Olga and Edie only
		t.Errorf("warning on %d rows, want the 2 with a login", strings.Count(body, "that account loses its profile"))
	}
}

func TestPeopleListFilterOnTeamAndSearch(t *testing.T) {
	t.Parallel()

	g, _ := peopleRig(t)
	names := func(body string) (out []string) {
		for _, n := range []string{"Owner Olga", "Edie Editor", "Sasha Reid", "Jason Holder"} {
			if strings.Contains(body, ">"+n+"</a>") {
				out = append(out, n)
			}
		}
		return out
	}
	get := func(q string) string { return g.do("GET", "/admin/people?"+q, "owner-token", nil, nil).Body.String() }
	if got := names(get("team=yes")); strings.Join(got, ",") != "Owner Olga,Sasha Reid" {
		t.Errorf("team=yes lists %v", got)
	}
	if got := names(get("team=no")); strings.Join(got, ",") != "Edie Editor,Jason Holder" {
		t.Errorf("team=no lists %v", got)
	}
	if got := names(get("q=sash")); strings.Join(got, ",") != "Sasha Reid" {
		t.Errorf("search lists %v", got)
	}
	if got := names(get("q=director&team=yes")); strings.Join(got, ",") != "Sasha Reid" {
		t.Errorf("search by job title lists %v", got)
	}
	// Sorted by posts, most first by default; the first row is Olga (81).
	body := get("sort=posts")
	if strings.Index(body, ">Owner Olga</a>") > strings.Index(body, ">Sasha Reid</a>") || strings.Index(body, ">Sasha Reid</a>") > strings.Index(body, ">Jason Holder</a>") {
		t.Error("sort=posts is not most posts first")
	}
	// The counts in the filter and the selected option.
	if b := get("team=yes"); !strings.Contains(b, "On the team (2)") || !strings.Contains(b, "Not on the team (2)") {
		t.Error("filter options lack the counts")
	}
}

func TestAddPersonCreatesAPersonWithoutALogin(t *testing.T) {
	t.Parallel()

	g, people := peopleRig(t)
	page := g.do("GET", "/admin/people/new", "owner-token", nil, nil)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Add person") {
		t.Fatalf("new page: %d", page.Code)
	}
	if issues := a11y.Check(page.Body.String(), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	rec := g.do("POST", "/admin/people", "owner-token", url.Values{"name": {"Grace Hopper"}}, nil)
	if rec.Code != 303 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created *person.Person
	for _, p := range people.loose {
		if p.Name == "Grace Hopper" {
			created = p
		}
	}
	if created == nil {
		t.Fatal("the person was not created")
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/entities/"+created.ID.String() {
		t.Errorf("redirect to %q, want the person's edit page", loc)
	}
	linked, _ := people.LinkedUsers(context.Background())
	if _, ok := linked[created.ID]; ok {
		t.Error("the new person has a login")
	}
	// A blank name is a field error, nothing is created.
	before := len(people.loose)
	rec = g.do("POST", "/admin/people", "owner-token", url.Values{"name": {"  "}}, nil)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Enter a name") || len(people.loose) != before {
		t.Errorf("blank name: %d, people %d -> %d", rec.Code, before, len(people.loose))
	}
	// The error summary links to the name field that exists on the page.
	body := rec.Body.String()
	m := regexp.MustCompile(`href="#([^"]+)"`).FindAllStringSubmatch(body, -1)
	found := false
	for _, h := range m {
		if h[1] == "person-name" {
			found = true
		}
	}
	if !found || !strings.Contains(body, `id="person-name"`) {
		t.Errorf("summary link and field id disagree (links %v):\n%s", m, body)
	}
}

func TestPeopleScreensAreForOwnersAndEditors(t *testing.T) {
	t.Parallel()

	g, _ := peopleRig(t)
	if rec := g.do("GET", "/admin/people", "", nil, nil); rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
		t.Errorf("anonymous: %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/people", "editor-token", nil, nil); rec.Code != 200 {
		t.Errorf("editor: %d", rec.Code)
	}
	// The person type has no create form of its own: Create > Person lands on the list.
	rec := g.do("GET", "/admin/entities/new?type=person", "owner-token", nil, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/people" {
		t.Errorf("entities/new?type=person: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestPeopleListOffersDuplicatesAndMergesThem(t *testing.T) {
	t.Parallel()

	g, people := peopleRig(t)
	// Another Sasha, a loose duplicate of the first.
	dup := &person.Person{ID: uuid.MustParse("00000000-0000-4000-8000-0000000000a5"), Slug: "sasha-r", Name: "Sasha R", Links: map[string]string{}}
	people.loose = append(people.loose, dup)
	people.posts[dup.ID] = 3
	sasha := people.loose[0]

	// The list stays first: it only links to the duplicates view.
	list := g.do("GET", "/admin/people", "owner-token", nil, nil).Body.String()
	if !strings.Contains(list, "Possible duplicates (1)") || !strings.Contains(list, `href="/admin/people/duplicates"`) || strings.Contains(list, `action="/admin/people/merge"`) {
		t.Errorf("list should link to the duplicates view and hold no merge form:\n%s", list)
	}
	body := g.do("GET", "/admin/people/duplicates", "owner-token", nil, nil).Body.String()
	for _, want := range []string{"Possible duplicate", "Sasha R, Sasha Reid", `action="/admin/people/merge"`, `name="ids" value="` + dup.ID.String() + `"`,
		"44 posts", "3 posts", `name="keep" value="`, "hm-radiocard", ">Merge into the one you keep<", `data-hm-confirm="Every post moves`} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if strings.Count(body, `action="/admin/people/merge"`) != 1 {
		t.Errorf("want one duplicate group:\n%s", body)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	compareGolden(t, "pages/people-duplicates.html", normalizePage(body))

	rec := g.do("POST", "/admin/people/merge", "owner-token", url.Values{"keep": {sasha.ID.String()}, "ids": {sasha.ID.String(), dup.ID.String()}}, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/people" {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body)
	}
	if len(people.merged) != 1 || people.merged[0] != [2]uuid.UUID{sasha.ID, dup.ID} {
		t.Errorf("merged = %v", people.merged)
	}
	if people.posts[sasha.ID] != 47 {
		t.Errorf("posts after merge = %d, want 47", people.posts[sasha.ID])
	}
	if !strings.Contains(afterRedirect(g, rec, "/admin/people"), "Merged 1 person into Sasha Reid. 3 posts now credit Sasha Reid.") {
		t.Error("no confirmation after the merge")
	}
	// The group is gone.
	if strings.Contains(g.do("GET", "/admin/people", "owner-token", nil, nil).Body.String(), "Possible duplicates (") {
		t.Error("the merged group is still offered")
	}
}

func TestPeopleMergeReturnsToDuplicatesWhileGroupsRemain(t *testing.T) {
	t.Parallel()

	g, people := peopleRig(t)
	sasha := people.loose[0]
	var drops []*person.Person
	for i, name := range []string{"Sasha R", "Sasha Re"} {
		d := &person.Person{ID: uuid.New(), Slug: "sasha-" + strconv.Itoa(i), Name: name, Links: map[string]string{}}
		people.loose = append(people.loose, d)
		drops = append(drops, d)
	}
	// Merge one of the three; the other two still look alike (same group).
	rec := g.do("POST", "/admin/people/merge", "owner-token", url.Values{"keep": {sasha.ID.String()}, "ids": {sasha.ID.String(), drops[0].ID.String()}}, nil)
	if rec.Code != 303 || len(people.merged) != 1 {
		t.Fatalf("merge: %d, merged %v", rec.Code, people.merged)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/people/duplicates" {
		t.Fatalf("redirect = %q, want the duplicates view", loc)
	}
	if !strings.Contains(afterRedirect(g, rec, "/admin/people/duplicates"), "Merged 1 person into") {
		t.Error("flash lost on the duplicates view")
	}
	// An error keeps the person where the groups are.
	rec = g.do("POST", "/admin/people/merge", "owner-token", url.Values{"keep": {"nope"}}, nil)
	if loc := rec.Header().Get("Location"); loc != "/admin/people/duplicates" {
		t.Errorf("error redirect = %q", loc)
	}
}

func TestPeopleMergeRefusesTwoLoginsAndBadInput(t *testing.T) {
	t.Parallel()

	g, people := peopleRig(t)
	olga, edie := people.byUser[g.owner.ID], people.byUser[g.editor.ID]
	rec := g.do("POST", "/admin/people/merge", "owner-token", url.Values{"keep": {olga.ID.String()}, "ids": {olga.ID.String(), edie.ID.String()}}, nil)
	if rec.Code != 303 || len(people.merged) != 0 {
		t.Fatalf("two logins: %d, merged %v", rec.Code, people.merged)
	}
	if !strings.Contains(afterRedirect(g, rec, "/admin/people"), "Two people with a login cannot be merged") {
		t.Error("no explanation for the refusal")
	}
	for name, form := range map[string]url.Values{
		"no keep":   {"ids": {olga.ID.String()}},
		"bad keep":  {"keep": {"nope"}, "ids": {olga.ID.String()}},
		"no others": {"keep": {olga.ID.String()}, "ids": {olga.ID.String()}},
		"unknown":   {"keep": {olga.ID.String()}, "ids": {uuid.NewString()}},
	} {
		if rec := g.do("POST", "/admin/people/merge", "owner-token", form, nil); rec.Code != 303 || len(people.merged) != 0 {
			t.Errorf("%s: %d, merged %v", name, rec.Code, people.merged)
		}
	}
	if rec := g.do("POST", "/admin/people/merge", "", url.Values{"keep": {olga.ID.String()}}, nil); rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/login") {
		t.Errorf("anonymous merge: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}

// afterRedirect fetches path as the owner with the flash cookie rec set: what
// the person sees after being redirected.
func afterRedirect(g *ownerRig, rec *httptest.ResponseRecorder, path string) string {
	cookies := []string{"hatamoto_session=owner-token"}
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "hm_flash" {
			cookies = append(cookies, ck.Name+"="+ck.Value)
		}
	}
	return g.do("GET", path, "owner-token", nil, map[string]string{"Cookie": strings.Join(cookies, "; ")}).Body.String()
}

// Account emails are for owners, as on the users screen: an editor sees only
// that a person has a login.
func TestPeopleListHidesAccountEmailsFromEditors(t *testing.T) {
	t.Parallel()

	g, _ := peopleRig(t)
	body := g.do("GET", "/admin/people", "editor-token", nil, nil).Body.String()
	if !strings.Contains(body, "Owner Olga") {
		t.Fatal("the editor cannot see the list")
	}
	if strings.Contains(body, "owner@example.com") {
		t.Error("an editor sees account emails")
	}
	if !strings.Contains(body, ">Yes<") {
		t.Error("the Login column shows nothing for a linked person")
	}
}
