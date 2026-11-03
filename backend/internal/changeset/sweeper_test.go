package changeset

import "testing"

func TestRunOfKey(t *testing.T) {
	const run = "0b0e6a8e-5c52-4d8c-9d63-1f1c2f6d0a11"
	if id, ok := runOfKey("media/staged/" + run + "/abc.png"); !ok || id.String() != run {
		t.Errorf("runOfKey = %v, %v", id, ok)
	}
	for _, bad := range []string{"media/staged/abc.png", "abc.png", "media/staged/not-a-run/abc.png"} {
		if _, ok := runOfKey(bad); ok {
			t.Errorf("runOfKey(%q) accepted", bad)
		}
	}
}
