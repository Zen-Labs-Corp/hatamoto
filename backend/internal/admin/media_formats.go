package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/media"
)

// MediaFormatsService is the switches for WebP and AVIF copies and the job
// that rebuilds the copies of existing images.
type MediaFormatsService interface {
	Settings(ctx context.Context) (media.Settings, error)
	SaveSettings(ctx context.Context, set media.Settings) error
	Status(ctx context.Context) (media.State, error)
	// StartRebuild returns media.ErrRebuildRunning while a run is active.
	StartRebuild(ctx context.Context, actor string) error
}

// mediaFormatsView is the Image formats card.
type mediaFormatsView struct {
	WebP, AVIF  bool
	Running     bool
	Progress    *ui.Progress
	Line        string // last run, or what a run does
	Skipped     []media.Skipped
	SkippedMore int
}

func (a *Admin) mediaFormatsView(ctx context.Context) *mediaFormatsView {
	svc := a.deps.MediaFormats
	if svc == nil {
		return nil
	}
	set, err := svc.Settings(ctx)
	if err != nil {
		return nil
	}
	st, err := svc.Status(ctx)
	if err != nil {
		return nil
	}
	v := &mediaFormatsView{WebP: set.WebP, AVIF: set.AVIF, Running: st.Running}
	loc := a.siteLocation(ctx)
	switch {
	case st.Running:
		v.Progress = &ui.Progress{Label: "Rebuilding copies", Value: fmt.Sprintf("%d of %d images", st.Done, st.Total), Pct: float64(st.Pct()), Kind: "info", Thin: true}
		v.Line = "The rebuild keeps going if you leave this page, and continues after a restart."
	case st.Last != nil:
		v.Line = fmt.Sprintf("Last rebuild: %s, %s", fmtDateTime(st.Last.FinishedAt.In(loc)), countNoun(st.Last.Images, "image"))
		if st.Last.Skipped > 0 {
			v.Line += fmt.Sprintf(", %d skipped", st.Last.Skipped)
		}
		v.Line += "."
	}
	if !st.Running || len(st.Skipped) > 0 {
		v.Skipped = st.Skipped
		v.SkippedMore = max(st.SkippedCount-len(st.Skipped), 0)
	}
	return v
}

func (a *Admin) handleMediaFormatsSave(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.MediaFormats
	if svc == nil {
		a.unavailable(w, r, "Image formats")
		return
	}
	_ = r.ParseForm()
	old, err := svc.Settings(r.Context())
	if err != nil {
		a.serverError(w, r, "The image format settings could not be loaded", err)
		return
	}
	next := media.Settings{WebP: r.PostFormValue("webp") == "on", AVIF: r.PostFormValue("avif") == "on"}
	if err := svc.SaveSettings(r.Context(), next); err != nil {
		a.serverError(w, r, "The image format settings could not be saved", err)
		return
	}
	a.audit(r, "media.formats", "media", map[string]any{
		"old": map[string]any{"webp": old.WebP, "avif": old.AVIF}, "new": map[string]any{"webp": next.WebP, "avif": next.AVIF},
	})
	a.SetFlash(w, r, "success", "Image format settings saved. New uploads follow them; rebuild to apply them to existing images.")
	http.Redirect(w, r, a.adminPath+"/settings/media", http.StatusSeeOther)
}

func (a *Admin) handleMediaRebuild(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.MediaFormats
	if svc == nil {
		a.unavailable(w, r, "Image formats")
		return
	}
	actor := ""
	if u := userFrom(r.Context()); u != nil {
		actor = u.Email
	}
	switch err := svc.StartRebuild(r.Context(), actor); {
	case err == nil:
		a.SetFlash(w, r, "success", "Rebuild started.")
	case errors.Is(err, media.ErrRebuildRunning):
		a.SetFlash(w, r, "info", "A rebuild is already running.")
	default:
		a.logger().Error("image rebuild", "err", err)
		a.SetFlash(w, r, "error", "The rebuild did not start.")
	}
	http.Redirect(w, r, a.adminPath+"/settings/media", http.StatusSeeOther)
}
