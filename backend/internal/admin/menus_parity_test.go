package admin

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// menuOpsParityCase is one case of testdata/menu_ops_parity.json, shared with
// scripts/admin-qa/unit/menulogic.test.mjs so the editor's client-side tree
// operations cannot drift from applyMenuOp.
type menuOpsParityCase struct {
	Name   string  `json:"name"`
	Op     string  `json:"op"`
	Target string  `json:"target"`
	Max    int     `json:"max"`
	Rows   [][]any `json:"rows"`
	Want   [][]any `json:"want"`
}

func TestMenuOpsParity(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/menu_ops_parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []menuOpsParityCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no parity cases")
	}
	pairs := func(rows []menuRow) [][]any {
		out := [][]any{}
		for _, r := range rows {
			out = append(out, []any{r.Label, float64(r.Depth)})
		}
		return out
	}
	for _, tc := range cases {
		if tc.Max != maxMenuDepth {
			t.Errorf("%s: max %d, the server limit is %d", tc.Name, tc.Max, maxMenuDepth)
		}
		rows := make([]menuRow, 0, len(tc.Rows))
		for _, r := range tc.Rows {
			rows = append(rows, menuRow{Label: r[0].(string), Depth: int(r[1].(float64))})
		}
		got, _ := applyMenuOp(rows, tc.Op, "", tc.Target)
		if g := pairs(got); !reflect.DeepEqual(g, tc.Want) {
			t.Errorf("%s: %s got %v, want %v", tc.Name, tc.Op, g, tc.Want)
		}
	}
}
