package builder

import (
	"encoding/json"
	"os"
	"testing"
)

// statesCase is one compiled state case of the shared fixture
// testdata/stylespec/states.json: the style map of a node (under a parent when
// Parent is set) and the CSS the Go compiler gives it. The JS tests replay the
// single-declaration keys through the client table and check the states flags.
type statesCase struct {
	Name   string                       `json:"name"`
	S      map[string]map[string]string `json:"s"`
	Parent bool                         `json:"parent,omitempty"`
	CSS    string                       `json:"css"`
}

func statesCases() []statesCase {
	in := []statesCase{
		{Name: "hover-lift card", S: sm{
			"base":       {"shadow": "sm", "trans": "transform-shadow", "tdur": "200", "tease": "ease-out"},
			"base:hover": {"ty": "-4px", "shadow": "lg"}}},
		{Name: "focus and active", S: sm{"base:focus": {"bc": "accent", "bw": "2", "bs": "solid"}, "base:active": {"sc": "0.98", "op": "0.8"}}},
		{Name: "parent hover", S: sm{"base:parent-hover": {"color": "accent", "td": "underline"}, "sm:parent-hover": {"color": "primary"}}, Parent: true},
		{Name: "transform keeps the base rotation", S: sm{"base": {"rot": "10deg"}, "base:hover": {"ty": "-4px"}, "md:hover": {"sc": "1.1"}}},
		{Name: "filter and backdrop", S: sm{"base:hover": {"blur": "2px", "bright": "120%", "bblur": "8px", "blend": "multiply"}}},
		{Name: "states without a parent give nothing", S: sm{"base:parent-hover": {"op": "0.5"}}},
		{Name: "a layout key in a state is ignored", S: sm{"base:hover": {"display": "none", "pt": "space-2", "bg": "accent"}}},
	}
	for i := range in {
		var parent *Node
		if in[i].Parent {
			parent = &Node{ID: "p", T: "container"}
		}
		in[i].CSS = nodeCSSIn(&Node{ID: "g", T: "container", S: in[i].S}, parent, CSSOpts{})
	}
	return in
}

func TestStyleSpecFixtureStates(t *testing.T) {
	if *updateStyleSpecFixture {
		writeJSON(t, fixtureDir+"states.json", statesCases())
	}
	raw, err := os.ReadFile(fixtureDir + "states.json")
	if err != nil {
		t.Fatal(err)
	}
	var got []statesCase
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := statesCases()
	if !jsonEqual(got, want) {
		t.Fatal("testdata/stylespec/states.json differs from the compiler; run `go test ./internal/builder -run StyleSpecFixture -update` and review the diff")
	}
}
