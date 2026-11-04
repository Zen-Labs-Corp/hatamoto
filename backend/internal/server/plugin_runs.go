package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/sdk"
)

// setupPluginRuns hands the run services to the plugin host and fails the
// runs the previous process left behind: no driver owns them any more.
func (s *Server) setupPluginRuns(svc *pluginhost.Services) {
	svc.Runs = &plugrun.Store{DB: s.db}
	if s.changesets != nil {
		svc.Changesets = s.changesets
	}
	if s.mediaService != nil {
		svc.Blobs = s.mediaService
		svc.Uploads = runUploads{s}
	}
	if s.siteConfig != nil {
		svc.Site = siteInfoSource{s}
	}
	if s.db != nil {
		svc.SlugTaken = entity.NewPostgresPublicRepository(s.db).SlugTaken
	}
	svc.LivePath = func(ctx context.Context, path string) bool {
		live, err := s.pathIsLivePage(ctx, path)
		return live || err != nil // when unsure, refuse the redirect
	}
}

// recoverPluginRuns runs after the runtime is configured.
func (s *Server) recoverPluginRuns(ctx context.Context, rt *pluginhost.Runtime) {
	if err := rt.RecoverRuns(ctx); err != nil {
		s.logger.Error("plugins: failed to recover interrupted runs", slog.Any("error", err))
	}
	rt.RecoverRunsEvery(time.Minute)
}

// runUploads opens the file the owner uploaded for a run, from the staged
// blob storage. It never exposes a path.
type runUploads struct{ s *Server }

func (u runUploads) Open(ctx context.Context, plugin string, id uuid.UUID) (io.ReaderAt, int64, error) {
	up, err := (&plugrun.Store{DB: u.s.db}).Upload(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	if up.Plugin != plugin {
		return nil, 0, plugrun.ErrNotFound
	}
	f, size, err := u.s.mediaService.OpenStaged(up.BlobKey)
	if err != nil {
		return nil, 0, err
	}
	return f, size, nil
}

// siteInfoSource answers site.info with the public facts of the site; never
// notification addresses, contact details or any secret.
type siteInfoSource struct{ s *Server }

func (i siteInfoSource) Info(ctx context.Context) (sdk.SiteInfo, error) {
	cfg, err := i.s.siteConfig.Get(ctx)
	if err != nil {
		return sdk.SiteInfo{}, err
	}
	info := sdk.SiteInfo{Title: cfg.CompanyName, Tagline: cfg.Tagline, TimeZone: cfg.Timezone, Language: cfg.Language}
	snap := i.s.urlSnapshot(ctx)
	info.Locales = snap.Locales().PublicTags()
	for _, c := range snap.Configs() {
		if c.Pattern != nil {
			info.URLPatterns = append(info.URLPatterns, c.SchemaSlug+": "+c.Pattern.String())
		}
	}
	return info, nil
}

// pluginRunStarter is the admin's way to start a run: it reads the runtime at
// call time, as plugins are set up after the admin is mounted.
type pluginRunStarter struct{ s *Server }

func (p pluginRunStarter) StartRun(ctx context.Context, plugin, run string, in admin.StartRunInput) (uuid.UUID, error) {
	if p.s.plugins == nil {
		return uuid.Nil, admin.ErrRunsUnavailable
	}
	id, err := p.s.plugins.StartRun(ctx, plugin, run, pluginhost.StartRunInput{Inputs: in.Inputs, Hosts: in.Hosts, UploadID: in.UploadID})
	var bad *pluginhost.InvalidRunInputError
	switch {
	case errors.Is(err, pluginhost.ErrActiveRun):
		return uuid.Nil, admin.ErrRunActive
	case errors.Is(err, pluginhost.ErrPluginNotLoaded), errors.Is(err, pluginhost.ErrUnknownRun):
		return uuid.Nil, admin.ErrRunNotFound
	case errors.Is(err, pluginhost.ErrRunsUnavailable):
		return uuid.Nil, admin.ErrRunsUnavailable
	case errors.As(err, &bad):
		return uuid.Nil, &admin.RunInputError{Msg: bad.Msg}
	case err != nil:
		return uuid.Nil, err
	}
	p.s.changesetAudit(ctx, "plugin.run.start", "plugin:"+plugin, map[string]any{"run": run, "run_id": id.String()})
	return id, nil
}

// Run reads one run for the run pages.
func (r *changesetReviewer) Run(ctx context.Context, id uuid.UUID) (admin.RunView, error) {
	run, err := r.runs.Get(ctx, id)
	if errors.Is(err, plugrun.ErrNotFound) {
		return admin.RunView{}, admin.ErrRunNotFound
	}
	if err != nil {
		return admin.RunView{}, err
	}
	v := runView(run)
	if run.ChangesetID != nil {
		if err := r.eng.DB.Pool().QueryRow(ctx, `SELECT item_count, payload_bytes FROM hatamoto_changesets WHERE id = $1`, *run.ChangesetID).
			Scan(&v.StagedItems, &v.StagedBytes); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return v, fmt.Errorf("failed to read staged size: %w", err)
		}
	}
	return v, nil
}

// Recent lists runs newest first; plugin "" is every plugin.
func (r *changesetReviewer) Recent(ctx context.Context, plugin string, limit int) ([]admin.RunView, error) {
	runs, err := r.runs.Recent(ctx, plugin, limit)
	if err != nil {
		return nil, err
	}
	out := make([]admin.RunView, 0, len(runs))
	for _, run := range runs {
		out = append(out, runView(run))
	}
	return out, nil
}

func runView(run plugrun.Run) admin.RunView {
	v := admin.RunView{ID: run.ID, Plugin: run.Plugin, Name: run.Name, State: run.State, Phase: run.Phase, Reason: run.Reason,
		Steps: run.Steps, Hosts: run.Hosts, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}
	var p struct {
		Done, Total int
		Message     string
	}
	if json.Unmarshal(run.Progress, &p) == nil {
		v.Done, v.Total, v.Message = p.Done, p.Total, p.Message
	}
	if run.ChangesetID != nil {
		v.ChangesetID = *run.ChangesetID
	}
	return v
}

// pluginRunCanceller is the admin's way to cancel a run; like the starter it
// reads the runtime at call time.
type pluginRunCanceller struct{ s *Server }

// cancelErr maps the runtime's answer to the admin's. A run that already ended
// is left as it is and reported as admin.ErrRunNotCancellable.
func cancelErr(err error) error {
	switch {
	case errors.Is(err, plugrun.ErrNotFound):
		return admin.ErrRunNotFound
	case errors.Is(err, pluginhost.ErrRunsUnavailable):
		return admin.ErrRunsUnavailable
	case errors.Is(err, pluginhost.ErrRunNotCancellable):
		return admin.ErrRunNotCancellable
	}
	return err
}

// CancelRun ends a running run.
func (c pluginRunCanceller) CancelRun(ctx context.Context, plugin string, id uuid.UUID) error {
	if c.s.plugins == nil {
		return admin.ErrRunsUnavailable
	}
	if err := cancelErr(c.s.plugins.CancelRun(ctx, plugin, id)); err != nil {
		return err
	}
	c.s.changesetAudit(ctx, "plugin.run.cancel", "plugin:"+plugin, map[string]any{"run_id": id.String()})
	return nil
}

// runDecls maps the runs a manifest declares for the Plugins page.
func runDecls(runs []sdk.Run) []admin.RunDecl {
	out := make([]admin.RunDecl, 0, len(runs))
	for _, r := range runs {
		d := admin.RunDecl{Name: r.Name, Title: r.Title}
		for _, in := range r.Inputs {
			d.Inputs = append(d.Inputs, admin.RunInputDecl{Name: in.Name, Label: in.Label, Type: in.Type, Required: in.Required, Options: in.Options})
		}
		out = append(out, d)
	}
	return out
}
