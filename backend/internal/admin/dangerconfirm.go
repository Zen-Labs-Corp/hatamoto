package admin

import (
	"net/http"

	"hatamoto.dev/core/internal/admin/ui"
)

// dangerMismatch is the field error shown when the typed phrase is wrong.
const dangerMismatch = "The text does not match."

// CheckDangerPhrase is the server side of ui.DangerConfirm. It compares the
// posted confirm_phrase to dc.Phrase exactly: no trimming, no case folding,
// and an empty phrase never matches. On a mismatch it returns dc with Error
// set and false; the screen re-renders its page with that card and status 422.
// The browser's button state is only a hint, so every screen must call this.
func CheckDangerPhrase(r *http.Request, dc ui.DangerConfirm) (ui.DangerConfirm, bool) {
	if dc.Phrase != "" && r.PostFormValue("confirm_phrase") == dc.Phrase {
		return dc, true
	}
	dc.Error = dangerMismatch
	return dc, false
}
