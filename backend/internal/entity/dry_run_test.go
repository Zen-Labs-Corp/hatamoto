package entity

import (
	"encoding/json/jsontext"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/testutil"
)

func TestDiffAttributes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		before, after string
		want          string
	}{
		{"unchanged", `{"a":1,"b":"x"}`, `{"b":"x","a":1}`, `[]`},
		{"scalar change", `{"a":1,"b":"x"}`, `{"a":2,"b":"x"}`, `[{"path":"a","before":1,"after":2}]`},
		{"nested leaf", `{"hours":{"open":"9","close":"5"}}`, `{"hours":{"open":"10","close":"5"}}`, `[{"path":"hours.open","before":"9","after":"10"}]`},
		{"added key", `{"a":1}`, `{"a":1,"b":2}`, `[{"path":"b","before":null,"after":2}]`},
		{"removed key", `{"a":1,"b":2}`, `{"a":1}`, `[{"path":"b","before":2,"after":null}]`},
		{"array is one change", `{"t":[1,2]}`, `{"t":[1,3]}`, `[{"path":"t","before":[1,2],"after":[1,3]}]`},
		{"object replaced by scalar", `{"o":{"x":1}}`, `{"o":5}`, `[{"path":"o","before":{"x":1},"after":5}]`},
		{"big number is exact", `{"n":9007199254740993}`, `{"n":9007199254740992}`, `[{"path":"n","before":9007199254740993,"after":9007199254740992}]`},
		{"sorted", `{"z":1,"a":1,"m":1}`, `{"z":2,"a":2,"m":2}`, `[{"path":"a","before":1,"after":2},{"path":"m","before":1,"after":2},{"path":"z","before":1,"after":2}]`},
		{"non-object root", `[1]`, `[2]`, `[{"path":"","before":[1],"after":[2]}]`},
		{"empty before", ``, `{"a":1}`, `[{"path":"a","before":null,"after":1}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := diffAttributes(jsontext.Value(tc.before), jsontext.Value(tc.after))
			testutil.RequireNoError(t, err)
			out, err := jsonx.Marshal(got)
			testutil.RequireNoError(t, err)
			if tc.name == "unchanged" || tc.name == "big number is exact" {
				testutil.Equal(t, tc.want, string(out))
				return
			}
			testutil.JSONEqual(t, tc.want, string(out))
		})
	}
}
