package author

import (
	"strings"
	"sync"
	"testing"
)

func TestSlugify(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Jane Guest":          "jane-guest",
		"  jane guest ":       "jane-guest",
		"Bob O'Brien-Smith":   "bob-o-brien-smith",
		"Zoë Álvarez":         "zo-lvarez",     // non-ASCII letters are dropped, as the byline page always did
		"Conan O’Brien":       "conan-o-brien", // curly apostrophe is punctuation
		"Jane—Doe":            "jane-doe",
		"Anne\u00a0Marie":     "anne-marie",
		"Ñ":                   "n", // accents are stripped when nothing ASCII is left
		"   ":                 "",
		"A  B":                "a-b",
		"Dr. Who?":            "dr-who",
		"Ada Lovelace (1815)": "ada-lovelace-1815",
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	// No ASCII letter left: a stable hash slug that is itself a valid slug.
	for _, name := range []string{"李明", "Ελένη", "🙂", "Иван Петров", "---"} {
		got := Slugify(name)
		if !strings.HasPrefix(got, "author-") || len(got) != len("author-")+8 || !ValidSlug(got) {
			t.Errorf("Slugify(%q) = %q, want author-<8 hex>", name, got)
		}
		if got != Slugify("  "+name+" ") || got != Slugify(strings.ToUpper(name)) {
			t.Errorf("Slugify(%q) is not stable across spacing and case", name)
		}
	}
	if Slugify("李明") == Slugify("李红") {
		t.Error("different names share a hash slug")
	}
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	if got := Slugify(string(long)); len(got) != MaxSlugLen {
		t.Errorf("long slug has %d characters", len(got))
	}
}

func TestValidSlug(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"jane", "jane-guest", "a1-b2"} {
		if !ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = false", s)
		}
	}
	for _, s := range []string{"", "Jane", "-a", "a-", "a--b", "a b", "a/b"} {
		if ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = true", s)
		}
	}
}

func TestInputNormalise(t *testing.T) {
	t.Parallel()
	in := Input{Name: "  Jane Guest ", Bio: " Writes. "}
	if err := in.normalise(""); err != nil || in.Slug != "jane-guest" || in.Name != "Jane Guest" || in.Bio != "Writes." {
		t.Errorf("normalise = %+v, %v", in, err)
	}
	for _, bad := range []Input{{Name: ""}, {Name: "x", Slug: "Bad Slug"}, {Name: "x", Bio: string(make([]byte, MaxBioLen+1))}} {
		if err := bad.normalise(""); err == nil {
			t.Errorf("normalise(%+v) accepted", bad)
		}
	}
}

func TestNormaliseKeepsStoredSlugOnRename(t *testing.T) {
	t.Parallel()
	in := Input{Name: "Jane Renamed"}
	if err := in.normalise("jane-guest"); err != nil || in.Slug != "jane-guest" {
		t.Errorf("normalise = %+v, %v", in, err)
	}
	in = Input{Name: "李明"}
	if err := in.normalise(""); err != nil || !strings.HasPrefix(in.Slug, "author-") {
		t.Errorf("a CJK name must be accepted: %+v, %v", in, err)
	}
}

// A transformer is stateful: slugifying from several goroutines must not share one.
func TestSlugifyConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if got := Slugify("Ñandú Ünïcode"); got == "" {
					t.Error("empty slug")
				}
				_ = Slugify("Ελένη")
			}
		}()
	}
	wg.Wait()
}
