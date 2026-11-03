package pluginhost

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/sdk"
)

// every changeset.* function, with a valid-looking argument document.
var changesetCalls = map[string]string{
	sdk.FnChangesetEntityUpsert:   `{"source_key":"k","schema":"post","name":"N"}`,
	sdk.FnChangesetTermUpsert:     `{"source_key":"k","kind":"tag","slug":"s","name":"N"}`,
	sdk.FnChangesetPersonUpsert:   `{"source_key":"k","slug":"s","name":"N"}`,
	sdk.FnChangesetMediaAttach:    `{"source_key":"k","handle":"h"}`,
	sdk.FnChangesetMenuUpsert:     `{"source_key":"k","slug":"s","name":"N"}`,
	sdk.FnChangesetRedirectUpsert: `{"source_key":"k","from":"/a","to":"/b"}`,
	sdk.FnChangesetDelete:         `{"kind":"entity","source_key":"k"}`,
}

// ISC-13: an unknown field in any new function's args is invalid_argument,
// including the ones whose services are absent.
func TestChangesetArgsUnknownFieldNewFns(t *testing.T) {
	a := runAPI(t, Services{})
	rc := newRun(sdk.RunPhaseTransform)
	rc.Proposes = []string{sdk.ProposeSiteSettings}
	calls := map[string]string{sdk.FnChangesetPropose: `{"kind":"site_settings","payload":{"title":"T"}}`}
	for fn, args := range changesetCalls {
		calls[fn] = args
	}
	for fn, args := range calls {
		var m map[string]any
		_ = json.Unmarshal([]byte(args), &m)
		m["origin"] = "owner" // a plugin may not pick the origin
		if _, he := callRun(t, a, rc, fn, m); he == nil || he.Code != sdk.ErrInvalidArgument {
			t.Errorf("%s with an unknown field: %v", fn, he)
		}
	}
	// And a good call without the services is unavailable, never a crash.
	for fn, args := range changesetCalls {
		var m map[string]any
		_ = json.Unmarshal([]byte(args), &m)
		_, he := callRun(t, a, rc, fn, m)
		if fn == sdk.FnChangesetMediaAttach || fn == sdk.FnChangesetPersonUpsert {
			continue
		}
		if he == nil || he.Code != sdk.ErrUnavailable {
			t.Errorf("%s without services: %v", fn, he)
		}
	}
}

func TestChangesetPersonUpsertUnavailable(t *testing.T) {
	a := runAPI(t, Services{})
	_, he := callRun(t, a, newRun(sdk.RunPhaseTransform), sdk.FnChangesetPersonUpsert, sdk.PersonUpsert{SourceKey: "p", Slug: "s", Name: "N"})
	if he == nil || he.Code != sdk.ErrUnavailable || !strings.Contains(he.Message, "not available yet") {
		t.Fatalf("person.upsert: %v", he)
	}
}

// ISC-12: a proposal of a kind the run did not declare is refused with the
// run's name; a declared one is checked against the allow-list.
func TestChangesetProposeKinds(t *testing.T) {
	a := runAPI(t, Services{})
	rc := newRun(sdk.RunPhaseTransform)
	rc.Proposes = []string{sdk.ProposeSiteSettings}
	_, he := callRun(t, a, rc, sdk.FnChangesetPropose, sdk.Proposal{Kind: sdk.ProposeHomePage, Payload: json.RawMessage(`{}`)})
	if he == nil || he.Code != sdk.ErrInvalidArgument || he.Message != `run "import" did not declare it proposes "home_page"` {
		t.Fatalf("undeclared kind: %v", he)
	}
	for name, payload := range map[string]string{
		"extra key":    `{"title":"T","theme":"x"}`,
		"empty":        `{}`,
		"not an obj":   `["a"]`,
		"bad zone":     `{"time_zone":"Mars/Base"}`,
		"non string":   `{"title":3}`,
		"long title":   `{"title":"` + strings.Repeat("t", maxSiteTitleLen+1) + `"}`,
		"blank title":  `{"title":" "}`,
		"newline lang": `{"language":"e\nn"}`,
	} {
		if _, he := callRun(t, a, rc, sdk.FnChangesetPropose, sdk.Proposal{Kind: sdk.ProposeSiteSettings, Payload: json.RawMessage(payload)}); he == nil || he.Code != sdk.ErrInvalidArgument {
			t.Errorf("%s: %v", name, he)
		}
	}
	// A valid one passes validation and only then needs the run store.
	if _, he := callRun(t, a, rc, sdk.FnChangesetPropose, sdk.Proposal{Kind: sdk.ProposeSiteSettings, Payload: json.RawMessage(`{"title":"T","time_zone":"Europe/Berlin"}`)}); he == nil || he.Code != sdk.ErrUnavailable {
		t.Fatalf("valid proposal without a run store: %v", he)
	}
}

func TestChangesetArgsValidation(t *testing.T) {
	a := runAPI(t, Services{})
	rc := newRun(sdk.RunPhaseTransform)
	long := strings.Repeat("k", maxSourceKeyLen+1)
	for name, c := range map[string]struct {
		fn   string
		args any
	}{
		"entity no key":      {sdk.FnChangesetEntityUpsert, sdk.EntityUpsert{Schema: "post", Name: "N"}},
		"entity long key":    {sdk.FnChangesetEntityUpsert, sdk.EntityUpsert{SourceKey: long, Schema: "post", Name: "N"}},
		"entity no schema":   {sdk.FnChangesetEntityUpsert, sdk.EntityUpsert{SourceKey: "k", Name: "N"}},
		"entity attrs array": {sdk.FnChangesetEntityUpsert, sdk.EntityUpsert{SourceKey: "k", Schema: "post", Name: "N", Attributes: json.RawMessage(`[1]`)}},
		"entity self parent": {sdk.FnChangesetEntityUpsert, sdk.EntityUpsert{SourceKey: "k", Schema: "post", Name: "N", ParentKey: "k"}},
		"term bad kind":      {sdk.FnChangesetTermUpsert, sdk.TermUpsert{SourceKey: "k", Kind: "Bad Kind", Slug: "s", Name: "N"}},
		"menu both":          {sdk.FnChangesetMenuUpsert, sdk.MenuUpsert{SourceKey: "k", Slug: "s", Name: "N", Items: []sdk.MenuItem{{Label: "L", URL: "/a", EntityKey: "e"}}}},
		"menu js url":        {sdk.FnChangesetMenuUpsert, sdk.MenuUpsert{SourceKey: "k", Slug: "s", Name: "N", Items: []sdk.MenuItem{{Label: "L", URL: "javascript:alert(1)"}}}},
		"redirect external":  {sdk.FnChangesetRedirectUpsert, sdk.RedirectUpsert{SourceKey: "k", From: "/a", To: "https://example.com/"}},
		"redirect protocol":  {sdk.FnChangesetRedirectUpsert, sdk.RedirectUpsert{SourceKey: "k", From: "/a", To: "//example.com/"}},
		"redirect status":    {sdk.FnChangesetRedirectUpsert, sdk.RedirectUpsert{SourceKey: "k", From: "/a", To: "/b", StatusCode: 200}},
		"delete media":       {sdk.FnChangesetDelete, sdk.ChangesetDeleteArgs{Kind: "media", SourceKey: "k"}},
		"delete no kind":     {sdk.FnChangesetDelete, sdk.ChangesetDeleteArgs{SourceKey: "k"}},
	} {
		if _, he := callRun(t, a, rc, c.fn, c.args); he == nil || he.Code != sdk.ErrInvalidArgument {
			t.Errorf("%s: %v", name, he)
		}
	}
}

func TestFlattenMenu(t *testing.T) {
	tree := []sdk.MenuItem{
		{Label: "A", URL: "/a", Children: []sdk.MenuItem{{Label: "A1", EntityKey: "e1"}, {Label: "A2", URL: "https://example.com"}}},
		{Label: "B"},
	}
	var items []menuItemView
	keys := map[int]string{}
	if err := flattenMenuForTest(tree, &items, keys); err != nil {
		t.Fatal(err)
	}
	want := []menuItemView{{"A", "custom", -1}, {"A1", "entity", 0}, {"A2", "external", 0}, {"B", "none", -1}}
	if len(items) != len(want) {
		t.Fatalf("items %v", items)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("item %d = %v, want %v", i, items[i], want[i])
		}
	}
	if len(keys) != 1 || keys[1] != "e1" {
		t.Fatalf("entity keys %v", keys)
	}
}

type menuItemView struct {
	Label, LinkType string
	Parent          int
}

func flattenMenuForTest(tree []sdk.MenuItem, out *[]menuItemView, keys map[int]string) error {
	var items []menu.ReplaceItem
	if err := flattenMenu(tree, -1, 1, &items, keys); err != nil {
		return err
	}
	for _, it := range items {
		*out = append(*out, menuItemView{it.Label, it.LinkType, it.Parent})
	}
	return nil
}

func TestQuotaErrorMapsToInvalidArgument(t *testing.T) {
	a := &API{}
	he := a.toHostError("p", fmt.Errorf("stage: %w", changeset.ErrQuotaExceeded))
	if he == nil || he.Code != sdk.ErrInvalidArgument || !strings.Contains(he.Message, "quota") {
		t.Fatalf("quota error mapped to %+v", he)
	}
}
