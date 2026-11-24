package admin

import (
	"context"
	"net/http/httptest"
	"testing"
)

// enableComments is the one place comments go on: it audits old and new, and drops the cache once.
func TestEnableCommentsAuditsAndInvalidates(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	p := newCommentsRig(t, svc)
	invalidated := 0
	p.a.deps.Invalidate = func(context.Context) { invalidated++ }

	was, err := p.a.enableComments(httptest.NewRequest("POST", "/admin/settings/comments/enable", nil))
	if err != nil || was {
		t.Fatalf("enableComments = %v, %v", was, err)
	}
	if !svc.settings.Enabled {
		t.Error("comments not enabled")
	}
	if invalidated != 1 {
		t.Errorf("Invalidate called %d times, want 1", invalidated)
	}
	if len(p.events) != 1 || p.events[0].Action != "comments.enable" || p.events[0].Detail["old"] != false || p.events[0].Detail["new"] != true {
		t.Errorf("audit = %+v", p.events)
	}
}
