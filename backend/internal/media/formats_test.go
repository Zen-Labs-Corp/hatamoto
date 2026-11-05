package media

import (
	"encoding/json/jsontext"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestLoadStateDefaults(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":       ``,
		"no key":      `{"builder":{}}`,
		"garbled":     `{"media_formats":"nope","media_rebuild":[1]}`,
		"not json":    `{{{`,
		"empty media": `{"media_formats":{},"media_rebuild":{}}`,
	} {
		set, st := LoadState(jsontext.Value(raw))
		if !set.WebP || !set.AVIF || st.Running || st.Total != 0 {
			t.Errorf("%s: %+v %+v", name, set, st)
		}
	}
}

func TestLoadStateRoundTrip(t *testing.T) {
	id := uuid.New()
	in := State{Running: true, RunID: "r1", Total: 10, Done: 4, Cursor: id}
	in.AddSkipped(Skipped{ID: id, Name: "a.jpg", Reason: "decode"})
	wrapped := jsontext.Value(`{"builder":{"x":1},"media_formats":` + string(SettingsRaw(Settings{WebP: true})) +
		`,"media_rebuild":` + string(StateRaw(in)) + `}`)
	set, st := LoadState(wrapped)
	if !set.WebP || set.AVIF {
		t.Errorf("settings %+v", set)
	}
	if !st.Running || st.Done != 4 || st.Total != 10 || st.Cursor != id || st.SkippedCount != 1 || len(st.Skipped) != 1 || st.Pct() != 40 {
		t.Errorf("state %+v", st)
	}
}

func TestSettingsMissingSwitchMeansOn(t *testing.T) {
	// Only the state was saved: the switches stay at their default.
	set, _ := LoadState(jsontext.Value(`{"media_formats":{"avif":true},"media_rebuild":{"running":false,"total":3}}`))
	if !set.WebP || !set.AVIF {
		t.Errorf("%+v", set)
	}
}

func TestSkippedIsCappedButCounted(t *testing.T) {
	var st State
	for range 30 {
		st.AddSkipped(Skipped{ID: uuid.New(), Name: strings.Repeat("a", 3)})
	}
	if st.SkippedCount != 30 || len(st.Skipped) != maxSkipped {
		t.Errorf("count %d list %d", st.SkippedCount, len(st.Skipped))
	}
}

func TestPct(t *testing.T) {
	for _, c := range []struct{ done, total, want int }{{0, 0, 0}, {1, 3, 33}, {3, 3, 100}, {5, 3, 100}} {
		if got := (State{Done: c.done, Total: c.total}).Pct(); got != c.want {
			t.Errorf("%d/%d = %d, want %d", c.done, c.total, got, c.want)
		}
	}
}
