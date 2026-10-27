package person

import (
	"testing"

	"github.com/google/uuid"
)

func TestFromAttributes(t *testing.T) {
	p := fromAttributes(uuid.New(), "ada", "Ada", []byte(`{"job_title":" CTO ","photo":"/a.jpg","public":true,"on_team":true,"github":"https://g.example/a","x":"","expertise":["go"," ",3]}`))
	if p.JobTitle != "CTO" || p.Photo != "/a.jpg" || !p.Public || !p.OnTeam {
		t.Errorf("got %+v", p)
	}
	if len(p.Links) != 1 || p.Links["github"] == "" {
		t.Errorf("links = %v, want only github", p.Links)
	}
	if len(p.Expertise) != 1 || p.Expertise[0] != "go" {
		t.Errorf("expertise = %v", p.Expertise)
	}
	if q := fromAttributes(uuid.New(), "b", "B", nil); q.Public || q.Links == nil {
		t.Errorf("empty attributes: %+v", q)
	}
}
