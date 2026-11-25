package entity

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The migration spells the character class out; it must stay the class
// isSegmentless reads, or queries and the index would split text differently.
func TestSegmentlessClassMatchesMigration(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../../migrations/202611251600_entity_search_cjk.sql")
	if err != nil {
		t.Fatal(err)
	}
	class := segmentlessClassSQL()
	up, _, _ := strings.Cut(string(raw), "-- +goose Down")
	// Every bracket expression of code points: the text test, the run test,
	// the run and the text between runs.
	found := regexp.MustCompile(`\[\^?(\\[uU][^\]]*)\]`).FindAllStringSubmatch(up, -1)
	if len(found) != 4 {
		t.Fatalf("migration has %d character classes, want 4", len(found))
	}
	for _, m := range found {
		if m[1] != class {
			t.Errorf("migration class\n %s\nwant\n %s", m[1], class)
		}
	}
}

func TestIsSegmentless(t *testing.T) {
	t.Parallel()
	for _, r := range "南房総ジャズフェスティバルみなー々〆서울𠮷ｶ" {
		if !isSegmentless(r) {
			t.Errorf("%q (U+%04X) is not segmentless", r, r)
		}
	}
	for _, r := range "aZ0é（）「」・、。 !-Ａ" {
		if isSegmentless(r) {
			t.Errorf("%q (U+%04X) is segmentless", r, r)
		}
	}
}

func TestSegmentlessTSQuery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ q, want string }{
		// Latin-only queries keep websearch_to_tsquery.
		{"jazz festival", ""},
		{`"jazz fest" -rock or pop`, ""},
		{"2026", ""},
		{"（）", ""},
		// The #412 cases.
		{"ジャズ", "('ジャ' <-> 'ャズ' <-> 'ズ':*)"},
		{"上映会", "('上映' <-> '映会' <-> '会':*)"},
		{"みなジャズ", "('みな' <-> 'なジ' <-> 'ジャ' <-> 'ャズ' <-> 'ズ':*)"},
		{"映", "('映':*)"},
		// Mixed scripts and the websearch syntax.
		{"ジャズ 2026", "('ジャ' <-> 'ャズ' <-> 'ズ':*) & ('2026')"},
		{"ジャズfes", "('ジャ' <-> 'ャズ' <-> 'ズ':* <-> 'fes')"},
		{"映画 -上映", "('映画' <-> '画':*) & !('上映' <-> '映':*)"},
		{"映画 or 音楽", "('映画' <-> '画':*) | ('音楽' <-> '楽':*)"},
		{`"映画「大きな家」"`, "('映画' <-> '画':* <-> '大き' <-> 'きな' <-> 'な家' <-> '家':*)"},
		{"or 映画 or", "('映画' <-> '画':*)"},
		{"（みなジャズ）", "('みな' <-> 'なジ' <-> 'ジャ' <-> 'ャズ' <-> 'ズ':*)"},
		{"서울 o'brien", "('서울' <-> '울':*) & ('o''brien')"},
		{`映画 a\b`, `('映画' <-> '画':*) & ('a\\b')`},
		{`"映画`, "('映画' <-> '画':*)"},
	} {
		if got := segmentlessTSQuery(tc.q); got != tc.want {
			t.Errorf("segmentlessTSQuery(%q) = %q, want %q", tc.q, got, tc.want)
		}
	}
}
