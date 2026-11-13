package entity

import "testing"

func TestLocaleSetDisplayName(t *testing.T) {
	set, err := NewLocaleSet("en", []LocaleEntry{
		{Tag: "en"},
		{Tag: "fr", Segment: "fr", Name: "Francais"},
		{Tag: "es", Segment: "es"},
	}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		set  *LocaleSet
		tag  string
		want string
	}{
		{"stored name wins", set, "fr", "Francais"},
		{"english fallback", set, "es", "Spanish"},
		{"unconfigured tag falls back to language name", set, "en", "English"},
		{"unparseable tag returns the tag", set, "xx-zz-q", "xx-zz-q"},
		{"shared marker", set, LocaleShared, ""},
		{"empty tag", set, "", ""},
		{"nil set", nil, "fr", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.set.DisplayName(tc.tag); got != tc.want {
				t.Errorf("DisplayName(%q) = %q, want %q", tc.tag, got, tc.want)
			}
		})
	}
}
