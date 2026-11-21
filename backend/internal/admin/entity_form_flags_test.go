package admin

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

// entityFormFlagsFill runs fill for u on self (nil: a new entry) and returns the form.
func entityFormFlagsFill(t *testing.T, c *contentRig, u *user.User, self *entity.Entity) *entityForm {
	t.Helper()
	ctx := context.WithValue(context.Background(), userKey, &CurrentUser{ID: u.ID.String(), Email: u.Email, Role: string(u.Role)})
	r := httptest.NewRequest("GET", "/admin/entities/x/edit", nil).WithContext(ctx)
	f := &entityForm{Status: "draft", New: self == nil}
	if self != nil {
		f.Status = string(self.Status)
	}
	c.a.fill(r, f, c.service, self, "", "", nil)
	return f
}

func entityFormFlagsValues(opts []ui.Option) []string {
	var out []string
	for _, o := range opts {
		out = append(out, o.Value)
	}
	return out
}

func TestEntityFormFlags(t *testing.T) {
	c, users := newRolesRig(t)

	t.Run("a contributor on their own draft cannot publish or schedule", func(t *testing.T) {
		u := users[user.RoleContributor]
		f := entityFormFlagsFill(t, c, u, c.entityBy(u, entity.StatusDraft))
		if !f.HidePublish || !f.HideUnpublish || f.StatusHelp == "" {
			t.Errorf("HidePublish=%v HideUnpublish=%v help=%q", f.HidePublish, f.HideUnpublish, f.StatusHelp)
		}
		if got, want := entityFormFlagsValues(f.StatusOptions), []string{"draft", "archived"}; !reflect.DeepEqual(got, want) {
			t.Errorf("options = %v, want %v", got, want)
		}
	})

	t.Run("an author on their own draft can", func(t *testing.T) {
		u := users[user.RoleAuthor]
		f := entityFormFlagsFill(t, c, u, c.entityBy(u, entity.StatusDraft))
		if f.HidePublish || f.HideUnpublish || f.StatusHelp != "" {
			t.Errorf("HidePublish=%v HideUnpublish=%v help=%q", f.HidePublish, f.HideUnpublish, f.StatusHelp)
		}
		if len(f.StatusOptions) != 4 {
			t.Errorf("options = %v, want all four", entityFormFlagsValues(f.StatusOptions))
		}
	})

	t.Run("a new entry follows the role", func(t *testing.T) {
		if f := entityFormFlagsFill(t, c, users[user.RoleContributor], nil); !f.HidePublish {
			t.Error("contributor: new entry offers Publish")
		}
		if f := entityFormFlagsFill(t, c, users[user.RoleAuthor], nil); f.HidePublish {
			t.Error("author: new entry hides Publish")
		}
	})

	t.Run("a contributor still sees the status of a published entry", func(t *testing.T) {
		u := users[user.RoleContributor]
		f := entityFormFlagsFill(t, c, u, c.entityBy(u, entity.StatusPublished))
		if got, want := entityFormFlagsValues(f.StatusOptions), []string{"draft", "published", "archived"}; !reflect.DeepEqual(got, want) {
			t.Errorf("options = %v, want %v", got, want)
		}
	})

	t.Run("owner and editor get today's form", func(t *testing.T) {
		for _, u := range []*user.User{c.owner, c.editor} {
			self := c.entityBy(users[user.RoleAuthor], entity.StatusDraft)
			f := entityFormFlagsFill(t, c, u, self)
			if f.HidePublish || f.HideUnpublish || f.StatusHelp != "" {
				t.Errorf("%s: flags set", u.Role)
			}
			if !reflect.DeepEqual(f.StatusOptions, entityStatusOptions("draft", false)) {
				t.Errorf("%s: options differ from today's", u.Role)
			}
		}
	})
}
