package kit

import (
	"encoding/json/jsontext"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/jsonx"
)

func TestSplitComments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in string
		want     kitComments
		rest     string // JSON the rest must equal ("" = no settings)
		wantErr  string
	}{
		{"order policy mode and url", `{"url_pattern":"/i/{slug}","comments":{"mode":"open","order":"rank","policy":{"enabled":true,"max_depth":32,"edit_window_minutes":120,"slow_mode_seconds":30}}}`,
			kitComments{Order: "rank", HasOrder: true, HasPolicy: true, Policy: comment.Policy{MaxDepth: 32, EditWindowMinutes: 120, SlowModeSeconds: 30}},
			`{"url_pattern":"/i/{slug}","comments":{"mode":"open"}}`, ""},
		{"missing numbers take the defaults", `{"comments":{"policy":{}}}`,
			kitComments{HasPolicy: true, Policy: comment.DefaultPolicy()}, `{}`, ""},
		{"enabled is ignored", `{"comments":{"policy":{"enabled":true}}}`,
			kitComments{HasPolicy: true, Policy: comment.DefaultPolicy()}, `{}`, ""},
		{"order alone", `{"comments":{"order":"oldest","mode":"closed"}}`,
			kitComments{Order: "oldest", HasOrder: true}, `{"comments":{"mode":"closed"}}`, ""},
		{"no comments key", `{"url_pattern":"/x"}`, kitComments{}, `{"url_pattern":"/x"}`, ""},
		{"no settings", ``, kitComments{}, ``, ""},
		{"a number as text", `{"comments":{"policy":{"max_depth":"x"}}}`, kitComments{}, "", "max_depth must be a whole number"},
		{"a fraction", `{"comments":{"policy":{"slow_mode_seconds":1.5}}}`, kitComments{}, "", "slow_mode_seconds must be a whole number"},
		{"order not text", `{"comments":{"order":3}}`, kitComments{}, "", "order must be text"},
		{"policy not an object", `{"comments":{"policy":true}}`, kitComments{}, "", "policy must be an object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rest, got, err := splitComments(jsontext.Value(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("comments = %+v, want %+v", got, tt.want)
			}
			if tt.rest == "" {
				if len(rest) != 0 {
					t.Errorf("rest = %s", rest)
				}
				return
			}
			var a, b any
			if err := jsonx.Unmarshal(rest, &a); err != nil {
				t.Fatal(err)
			}
			if err := jsonx.Unmarshal([]byte(tt.rest), &b); err != nil {
				t.Fatal(err)
			}
			if ja, _ := jsonx.Marshal(a); string(ja) != func() string { jb, _ := jsonx.Marshal(b); return string(jb) }() {
				t.Errorf("rest = %s, want %s", rest, tt.rest)
			}
		})
	}
}

func TestKitCommentsCheck(t *testing.T) {
	t.Parallel()
	bad := kitComments{HasPolicy: true, Policy: comment.Policy{MaxDepth: comment.PolicyDepthMax + 1, EditWindowMinutes: 1}}
	if err := bad.check(); err == nil || !strings.Contains(err.Error(), "max_depth") {
		t.Errorf("err = %v", err)
	}
	if err := (kitComments{Order: "sideways"}).check(); err == nil {
		t.Error("an unknown order must be refused")
	}
	if err := (kitComments{Order: "rank", HasOrder: true, HasPolicy: true, Policy: comment.DefaultPolicy()}).check(); err != nil {
		t.Error(err)
	}
}
