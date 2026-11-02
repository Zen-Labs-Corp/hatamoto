package redirect

import (
	"strings"
	"testing"
)

func TestVisitorClause(t *testing.T) {
	if c, a := visitorClause("", 3); c != "" || a != nil {
		t.Fatalf("no filter: %q %v", c, a)
	}
	if c, _ := visitorClause("other", 3); c != "" {
		t.Fatalf("unknown value must not filter: %q", c)
	}
	bots, args := visitorClause(VisitorBots, 2)
	if bots != "(lower(last_user_agent) LIKE ANY($2))" || len(args) == 0 {
		t.Fatalf("bots: %q %d", bots, len(args))
	}
	people, _ := visitorClause(VisitorPeople, 2)
	if !strings.Contains(people, "last_user_agent IS NULL OR NOT") {
		t.Fatalf("people: %q", people)
	}
	for _, p := range args {
		if !strings.HasPrefix(p, "%") || !strings.HasSuffix(p, "%") {
			t.Errorf("pattern %q not wrapped", p)
		}
	}
}

func TestLikeEscaper(t *testing.T) {
	if got := likeEscaper.Replace(`a%b_c\d`); got != `a\%b\_c\\d` {
		t.Fatalf("got %q", got)
	}
}
