package form

import (
	"os"
	"reflect"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

// The case table is shared with the preview and the public island tests: the
// strings in it are the validation contract.
type validationCases struct {
	Cases []struct {
		Name    string         `json:"name"`
		Fields  []FormField    `json:"fields"`
		Data    map[string]any `json:"data"`
		Context struct {
			PageURL  string `json:"page_url"`
			Referrer string `json:"referrer"`
		} `json:"context"`
		WantData   map[string]any    `json:"want_data"`
		WantErrors map[string]string `json:"want_errors"`
	} `json:"cases"`
	Visibility []struct {
		Name        string          `json:"name"`
		Fields      []FormField     `json:"fields"`
		Data        map[string]any  `json:"data"`
		WantVisible map[string]bool `json:"want_visible"`
	} `json:"visibility"`
}

func loadValidationCases(t *testing.T) validationCases {
	t.Helper()
	raw, err := os.ReadFile("testdata/validation_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var tbl validationCases
	if err := jsonx.Unmarshal(raw, &tbl, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	return tbl
}

func TestValidationCases(t *testing.T) {
	t.Parallel()
	tbl := loadValidationCases(t)
	if len(tbl.Cases) < 30 {
		t.Fatalf("fixture has %d cases, want >= 30", len(tbl.Cases))
	}
	for _, tc := range tbl.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			ctx := SubmitContext{PageURL: tc.Context.PageURL, Referrer: tc.Context.Referrer}
			data, errs := Validate(tc.Fields, tc.Data, ctx)
			if len(tc.WantErrors) > 0 {
				if data != nil {
					t.Errorf("data = %v, want nil on error", data)
				}
				if !reflect.DeepEqual(errs, tc.WantErrors) {
					t.Errorf("errors = %v, want %v", errs, tc.WantErrors)
				}
				return
			}
			if errs != nil && len(errs) > 0 {
				t.Fatalf("errors = %v, want none", errs)
			}
			want := tc.WantData
			if want == nil {
				want = map[string]any{}
			}
			if !reflect.DeepEqual(data, want) {
				t.Errorf("data = %v, want %v", data, want)
			}
		})
	}
}

func TestVisibilityCases(t *testing.T) {
	t.Parallel()
	tbl := loadValidationCases(t)
	if len(tbl.Visibility) == 0 {
		t.Fatal("fixture has no visibility cases")
	}
	for _, tc := range tbl.Visibility {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			if got := Visible(tc.Fields, tc.Data); !reflect.DeepEqual(got, tc.WantVisible) {
				t.Errorf("visible = %v, want %v", got, tc.WantVisible)
			}
		})
	}
}
