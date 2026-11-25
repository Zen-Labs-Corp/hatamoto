package admin

import (
	"strings"
	"testing"
)

func TestCommentersIdentityCell(t *testing.T) {
	t.Parallel()

	svc := cmtrCommentersFake()
	svc.commenters[0].Origin = "forum"
	p := newCommentsRig(t, svc)
	body := p.do("GET", "/admin/settings/comments/commenters", "owner-token", nil, nil).Body.String()
	table := body[strings.Index(body, "<table"):strings.Index(body, "</table>")]
	for _, want := range []string{"Identity", "plugin: forum", "Email"} {
		if !strings.Contains(table, want) {
			t.Errorf("table lacks %q", want)
		}
	}
}

func TestCommenterSignoutConfirmNoEmail(t *testing.T) {
	t.Parallel()

	p := newCommentsRig(t, cmtrCommentersFake())
	body := p.do("GET", "/admin/settings/comments/commenters/"+cmtrTestID+"/signout", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "sign in again") {
		t.Fatalf("confirm page lacks the signout copy: %.300s", body)
	}
	if strings.Contains(strings.ToLower(cmtrConfirms["signout"].Body), "email") {
		t.Errorf("signout copy mentions email")
	}
}
