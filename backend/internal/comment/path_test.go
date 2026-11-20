package comment

import (
	"sort"
	"strings"
	"testing"
)

func TestPathSegmentAndDepth(t *testing.T) {
	t.Parallel()
	if got := Segment(255); got != "0000000000ff" {
		t.Errorf("Segment(255) = %q", got)
	}
	p := Segment(1) + "." + Segment(2) + "." + Segment(3)
	if Depth(p) != 3 || Depth("") != 0 || Depth(Segment(1)) != 1 {
		t.Errorf("Depth wrong for %q", p)
	}
	if got := AncestorPath(p, 2); got != Segment(1)+"."+Segment(2) {
		t.Errorf("AncestorPath(2) = %q", got)
	}
	if AncestorPath(p, 0) != "" || AncestorPath(p, 9) != p {
		t.Error("AncestorPath bounds wrong")
	}
}

func TestPathOrderParentBeforeChildAndSubtreeContiguous(t *testing.T) {
	t.Parallel()
	// 17+ siblings so hex width matters, replies down to depth 5.
	var paths []string
	var seq int64
	next := func() string { seq++; return Segment(seq) }
	for i := 0; i < 18; i++ {
		root := next()
		paths = append(paths, root)
		cur := root
		for d := 2; d <= 5; d++ {
			cur += "." + next()
			paths = append(paths, cur)
		}
		paths = append(paths, root+"."+next()) // a second child of the root
	}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	pos := map[string]int{}
	for i, p := range sorted {
		pos[p] = i
	}
	for _, p := range paths {
		if i := strings.LastIndex(p, "."); i > 0 && pos[p[:i]] > pos[p] {
			t.Fatalf("parent %q sorts after child %q", p[:i], p)
		}
	}
	for _, p := range paths {
		if Depth(p) != 1 {
			continue
		}
		lo, hi := p, subtreeEnd(p)
		for _, q := range sorted {
			inRange := q >= lo && q < hi
			isSub := q == p || strings.HasPrefix(q, p+".")
			if inRange != isSub {
				t.Fatalf("range [%q,%q) membership of %q = %v, want %v", lo, hi, q, inRange, isSub)
			}
		}
		// contiguity: members occupy consecutive positions
		first, count := pos[p], 0
		for _, q := range sorted {
			if q >= lo && q < hi {
				count++
			}
		}
		for i := first; i < first+count; i++ {
			if q := sorted[i]; !(q >= lo && q < hi) {
				t.Fatalf("subtree of %q not contiguous at %d", p, i)
			}
		}
	}
}

func TestReplyParentPath(t *testing.T) {
	t.Parallel()
	var chain []string
	cur := ""
	for i := int64(1); i <= 6; i++ {
		if cur != "" {
			cur += "."
		}
		cur += Segment(i)
		chain = append(chain, cur)
	}
	// maxDepth 5: replies to depth 1..4 attach in place; depth 5 and 6 attach to the depth-4 comment.
	for d := 1; d <= 6; d++ {
		want := chain[d-1]
		if d >= 5 {
			want = chain[3]
		}
		if got := ReplyParentPath(chain[d-1], 5); got != want {
			t.Errorf("maxDepth 5, parent depth %d: got %q, want %q", d, got, want)
		}
	}
	if got := ReplyParentPath(chain[0], 1); got != "" {
		t.Errorf("maxDepth 1: got %q, want root", got)
	}
	if got := ReplyParentPath(chain[2], 1); got != "" {
		t.Errorf("maxDepth 1 deep parent: got %q, want root", got)
	}
}
