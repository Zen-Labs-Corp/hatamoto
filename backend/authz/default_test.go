package authz_test

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/auth"
)

// want is the default policy written out independently of default.go.
var want = map[authz.Action]struct {
	editor bool
	scope  string
}{
	"admin.styleguide":                {false, ""},
	"analytics.read":                  {true, "read:reports"},
	"analytics.settings.manage":       {false, ""},
	"apikey.creator.act":              {true, ""},
	"apikey.manage":                   {false, ""},
	"audit.read":                      {false, ""},
	"author.manage":                   {true, ""},
	"builder.block.manage":            {true, ""},
	"builder.preset.create":           {true, ""},
	"builder.style":                   {false, ""},
	"cache.flush":                     {false, ""},
	"comment.blocklist.manage":        {true, ""},
	"comment.commenter.erase":         {false, ""},
	"comment.commenter.export":        {false, ""},
	"comment.moderate":                {true, "write:comments"},
	"comment.read":                    {true, "read:comments"},
	"comment.receive_moderation_mail": {true, ""},
	"comment.reply_as_staff":          {true, "write:comments"},
	"comment.settings.manage":         {false, ""},
	"comment.settings.read":           {true, ""},
	"content.write":                   {true, ""},
	"dashboard.owner.read":            {false, ""},
	"dashboard.read":                  {true, ""},
	"entity.create":                   {true, "write:entities"},
	"entity.delete":                   {true, ""},
	"entity.duplicate":                {true, "write:entities"},
	"entity.export":                   {true, ""},
	"entity.handback":                 {true, ""},
	"entity.publish":                  {true, "write:entities"},
	"entity.read":                     {true, "read:entities"},
	"entity.reorder":                  {true, "write:entities"},
	"entity.review.bypass":            {false, "write:entities"},
	"entity.review.decide":            {true, ""},
	"entity.review.request":           {true, "write:entities"},
	"entity.share":                    {true, ""},
	"entity.takeover":                 {true, "write:entities"},
	"entity.terms.update":             {true, ""},
	"entity.translate":                {true, ""},
	"entity.unpublish":                {true, "write:entities"},
	"entity.update":                   {true, "write:entities"},
	"entity.version.restore":          {true, "write:entities"},
	"form.manage":                     {true, ""},
	"form.read":                       {true, "read:forms"},
	"form.settings.manage":            {false, ""},
	"form.submission.manage":          {true, ""},
	"form.submission.read":            {true, "read:forms"},
	"import.run":                      {false, ""},
	"link.insert":                     {true, "write:entities"},
	"link.rule.manage":                {true, ""},
	"link.suggest":                    {true, "read:entities"},
	"media.delete":                    {true, ""},
	"media.read":                      {true, "read:media"},
	"media.settings.manage":           {false, ""},
	"media.update":                    {true, ""},
	"media.upload":                    {true, "write:media"},
	"menu.manage":                     {true, "write:menus"},
	"menu.read":                       {true, "read:entities"},
	"person.manage":                   {true, ""},
	"plugin.changeset.review":         {false, ""},
	"plugin.grant":                    {false, ""},
	"plugin.manage":                   {false, ""},
	"plugin.route.invoke":             {true, ""},
	"plugin.ui.access":                {true, ""},
	"plugin.ui.host":                  {false, ""},
	"plugin.view":                     {true, ""},
	"profile.update":                  {true, ""},
	"redirect.manage":                 {true, "write:redirects"},
	"redirect.read":                   {true, "read:entities"},
	"render.preview":                  {true, "read:entities"},
	"robots.policy.write":             {false, ""},
	"schema.manage":                   {false, ""},
	"schema.read":                     {true, "read:entities"},
	"section_template.manage":         {true, ""},
	"settings.manage":                 {false, ""},
	"settings.seo.read":               {false, "read:seo"},
	"settings.seo.write":              {false, "write:seo"},
	"setup.run":                       {false, ""},
	"site.receive_owner_mail":         {false, ""},
	"site.write":                      {true, ""},
	"term.manage":                     {false, "write:terms"},
	"term.read":                       {true, "read:entities"},
	"theme.manage":                    {false, ""},
	"theme.preview":                   {true, ""},
	"user.delete":                     {false, ""},
	"user.manage":                     {false, ""},
	"user.owner.grant":                {false, ""},
	"webhook.manage":                  {false, ""},
}

func TestCatalogueMatchesWant(t *testing.T) {
	got := authz.Actions()
	if len(got) != len(want) {
		t.Fatalf("catalogue has %d actions, want table has %d", len(got), len(want))
	}
	for _, a := range got {
		if _, ok := want[a]; !ok {
			t.Errorf("action %q missing from the want table", a)
		}
	}
}

func TestActionsReturnsCopy(t *testing.T) {
	a := authz.Actions()
	a[0] = "tampered"
	if authz.Actions()[0] == "tampered" {
		t.Fatal("Actions must return a copy")
	}
}

func TestDefaultSessions(t *testing.T) {
	ctx := context.Background()
	d := authz.Default()
	for a, w := range want {
		for _, c := range []struct {
			name string
			s    authz.Subject
			want bool
		}{
			{"owner", authz.Subject{UserID: "u", Role: "owner"}, true},
			{"editor", authz.Subject{UserID: "u", Role: "editor"}, w.editor},
			{"no role", authz.Subject{UserID: "u"}, false},
			{"unknown role", authz.Subject{UserID: "u", Role: "admin"}, false},
			{"anonymous", authz.Subject{}, false},
			{"owner without user", authz.Subject{Role: "owner"}, false},
		} {
			got, err := d.Can(ctx, c.s, a, authz.Object{})
			if err != nil || got != c.want {
				t.Errorf("%s %s: got (%v, %v), want %v", c.name, a, got, err, c.want)
			}
		}
	}
}

func TestDefaultKeys(t *testing.T) {
	ctx := context.Background()
	d := authz.Default()
	held := [][]auth.APIKeyScope{nil}
	for _, s := range auth.ValidScopes() {
		held = append(held, []auth.APIKeyScope{s})
	}
	for a, w := range want {
		for _, h := range held {
			for _, creator := range []string{"owner", "editor", ""} {
				strs := make([]string, len(h))
				for i, s := range h {
					strs[i] = string(s)
				}
				key := &auth.APIKey{Scopes: h, CreatorRole: creator}
				exp := (creator == "owner" || creator == "editor" && w.editor) && w.scope != "" &&
					key.HasScope(auth.APIKeyScope(w.scope))
				s := authz.Subject{UserID: "u", KeyID: "k", Scopes: strs, CreatorRole: creator}
				got, err := d.Can(ctx, s, a, authz.Object{})
				if err != nil || got != exp {
					t.Errorf("%s scopes=%v creator=%q: got (%v, %v), want %v", a, strs, creator, got, err, exp)
				}
			}
		}
	}
}

func TestDefaultKeyIgnoresSessionRole(t *testing.T) {
	s := authz.Subject{UserID: "u", Role: "owner", KeyID: "k", Scopes: []string{"*"}, CreatorRole: "editor"}
	d := authz.Default()
	if ok, _ := d.Can(context.Background(), s, authz.ActionEntityUpdate, authz.Object{}); !ok {
		t.Error("editor-created wildcard key should update entities")
	}
	// Role "owner" on the subject must not unlock an owner-only scope.
	if ok, _ := d.Can(context.Background(), s, authz.ActionSettingsSEOWrite, authz.Object{}); ok {
		t.Error("Role must be ignored for key subjects")
	}
}

func TestDefaultKeyNeedsCreatorID(t *testing.T) {
	s := authz.Subject{KeyID: "k", Scopes: []string{"*"}, CreatorRole: "owner"}
	if ok, _ := authz.Default().Can(context.Background(), s, authz.ActionEntityUpdate, authz.Object{}); ok {
		t.Error("a key subject without a creator id must be denied")
	}
}

func TestDefaultKeyRoleDoesNotNarrow(t *testing.T) {
	s := authz.Subject{UserID: "u", Role: "editor", KeyID: "k", Scopes: []string{"write:seo"}, CreatorRole: "owner"}
	if ok, _ := authz.Default().Can(context.Background(), s, authz.ActionSettingsSEOWrite, authz.Object{}); !ok {
		t.Error("an owner-created key may hold owner-only scopes whatever Role says")
	}
}

func TestDefaultUnknownAction(t *testing.T) {
	ok, err := authz.Default().Can(context.Background(), authz.Subject{UserID: "u", Role: "owner"}, "no.such", authz.Object{})
	if ok || !errors.Is(err, authz.ErrUnknownAction) {
		t.Errorf("got (%v, %v), want false and ErrUnknownAction", ok, err)
	}
}
