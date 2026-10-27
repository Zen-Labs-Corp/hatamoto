package person

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func names(groups [][]*Person) []string {
	var out []string
	for _, g := range groups {
		var n []string
		for _, p := range g {
			n = append(n, p.Slug)
		}
		out = append(out, strings.Join(n, "+"))
	}
	return out
}

func TestDuplicates(t *testing.T) {
	mk := func(slug, name string) *Person { return &Person{ID: uuid.New(), Slug: slug, Name: name} }
	people := []*Person{
		mk("alex-carter", "Alex Carter"),
		mk("alex-c", "Alex C"),
		mk("morgan-price", "Morgan Price"),
		mk("morgan-price-2", "  morgan   PRICE "),
		mk("sam", "Sam"),
		mk("samantha-jones", "Samantha Jones"),
		mk("jason-holder", "Jason Holder"),
		mk("jason-hall", "Jason Hall"),
		mk("hayley-white", "Hayley White"),
	}
	got := names(Duplicates(people))
	want := []string{"alex-c+alex-carter", "morgan-price+morgan-price-2"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

func TestDuplicatesChainsIntoOneGroupAndIgnoresLoners(t *testing.T) {
	mk := func(slug, name string) *Person { return &Person{ID: uuid.New(), Slug: slug, Name: name} }
	// a~b by name, b~c by slug prefix: one group of three.
	people := []*Person{mk("ann", "Ann Lee"), mk("ann-lee-2", "Ann Lee"), mk("ann-l", "A L"), mk("bob", "Bob")}
	groups := Duplicates(people)
	if len(groups) != 1 || len(groups[0]) != 3 {
		t.Fatalf("groups = %v", names(groups))
	}
	if Duplicates(nil) != nil || len(Duplicates(people[3:])) != 0 {
		t.Error("no people or one person must give no groups")
	}
}
