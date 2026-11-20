package app

import (
	"bytes"
	"testing"

	"hatamoto.dev/core/internal/wpimport"
)

func TestPrintCommentsResult(t *testing.T) {
	var b bytes.Buffer
	printCommentsResult(&b, nil)
	if b.Len() != 0 {
		t.Fatalf("nil result printed %q", b.String())
	}
	printCommentsResult(&b, &wpimport.CommentsResult{Imported: 412, Approved: 380, Pending: 20, Spam: 9, Trash: 3, CommentersCreated: 133, PingsTotal: 17})
	want := "  Comments: 412 imported (approved 380, pending 20, spam 9, trash 3), 133 commenters, 17 pingbacks/trackbacks skipped; author URLs dropped; comments stay off\n"
	if b.String() != want {
		t.Errorf("got  %q\nwant %q", b.String(), want)
	}
	b.Reset()
	printCommentsResult(&b, &wpimport.CommentsResult{Imported: 1, Approved: 1, SkippedNoEmail: 2, SkippedBody: 1})
	if got := b.String(); got != "  Comments: 1 imported (approved 1, pending 0, spam 0, trash 0), 0 commenters, 3 skipped; author URLs dropped; comments stay off\n" {
		t.Errorf("got %q", got)
	}
}
