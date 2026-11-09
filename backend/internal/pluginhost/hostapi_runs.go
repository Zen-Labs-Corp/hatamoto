package pluginhost

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"errors"
	"io"
	"net/url"
	"path"
	"strings"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/sdk"
)

// The functions a plugin calls from inside a run. They carry no permission:
// the run context, checked in dispatch against fnPhases, is their gate.
func init() {
	hostFns[sdk.FnRunInfo] = hostFn{run: fnRunInfo}
	hostFns[sdk.FnRunCheckpoint] = hostFn{run: fnRunCheckpoint}
	hostFns[sdk.FnRunProgress] = hostFn{run: fnRunProgress}
	hostFns[sdk.FnRunFinish] = hostFn{run: fnRunFinish}
	hostFns[sdk.FnRunFail] = hostFn{run: fnRunFail}
	hostFns[sdk.FnUploadRead] = hostFn{run: fnUploadRead}
	hostFns[sdk.FnBufferPut] = hostFn{run: fnBufferPut}
	hostFns[sdk.FnBufferGet] = hostFn{run: fnBufferGet}
	hostFns[sdk.FnBufferList] = hostFn{run: fnBufferList}
	hostFns[sdk.FnMediaImportURL] = hostFn{run: fnMediaImportURL}
	hostFns[sdk.FnSiteInfo] = hostFn{run: fnSiteInfo}
	hostFns[sdk.FnEntitiesSlugTaken] = hostFn{run: fnEntitiesSlugTaken}
}

// Limits of the run functions.
const (
	maxUploadChunk   = 256 << 10
	maxProgressMsg   = 512
	maxRunSummary    = 64 << 10
	maxImportBytes   = 64 << 20
	maxImportURLLen  = 2048
	maxFilenameLen   = 200
	maxSlugCheckLen  = 200
	defaultListLimit = 100
)

// runStore returns the run the call belongs to and the store holding it.
func (a *API) runStore(ctx context.Context) (RunCtx, *plugrun.Store, error) {
	rc, ok := RunFrom(ctx)
	if !ok { // the phase gate refuses this first; kept so a bypass cannot reach the store
		return rc, nil, herr(sdk.ErrPhaseDenied, "only available inside a run")
	}
	if a.svc.Runs == nil {
		return rc, nil, unavailable("runs")
	}
	return rc, a.svc.Runs, nil
}

// runErr turns a plugrun error into the error a plugin sees.
func runErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, plugrun.ErrQuota):
		return herr(sdk.ErrInvalidArgument, "%s", err.Error())
	case errors.Is(err, plugrun.ErrInvalid):
		return herr(sdk.ErrInvalidArgument, "%s", err.Error())
	case errors.Is(err, plugrun.ErrNotFound):
		return herr(sdk.ErrNotFound, "%s", err.Error())
	case errors.Is(err, plugrun.ErrNotRunning):
		return herr(sdk.ErrUnavailable, "the run is no longer running")
	}
	return err
}

func fnRunInfo(ctx context.Context, a *API, _ *callEnv, _ jsontext.Value) (any, error) {
	rc, runs, err := a.runStore(ctx)
	if err != nil {
		return nil, err
	}
	run, err := runs.Get(ctx, rc.RunID)
	if err != nil {
		return nil, runErr(err)
	}
	info := sdk.RunInfo{RunID: run.ID.String(), Name: run.Name, Phase: rc.Phase, Cursor: run.Cursor, Params: run.Params, Hosts: run.Hosts}
	if run.UploadID != nil {
		info.UploadID = run.UploadID.String()
	}
	return info, nil
}

func fnRunCheckpoint(ctx context.Context, a *API, _ *callEnv, raw jsontext.Value) (any, error) {
	rc, runs, err := a.runStore(ctx)
	if err != nil {
		return nil, err
	}
	args, err := decodeArgs[sdk.RunCheckpointArgs](raw)
	if err != nil {
		return nil, err
	}
	return struct{}{}, runErr(runs.Checkpoint(ctx, rc.RunID, args.Cursor))
}

func fnRunProgress(ctx context.Context, a *API, _ *callEnv, raw jsontext.Value) (any, error) {
	rc, runs, err := a.runStore(ctx)
	if err != nil {
		return nil, err
	}
	args, err := decodeArgs[sdk.RunProgressArgs](raw)
	if err != nil {
		return nil, err
	}
	if args.Done < 0 || args.Total < 0 || len(args.Message) > maxProgressMsg {
		return nil, herr(sdk.ErrInvalidArgument, "progress: done and total must not be negative, message at most %d bytes", maxProgressMsg)
	}
	doc, _ := jsonx.MarshalPinned(args)
	return struct{}{}, runErr(runs.Progress(ctx, rc.RunID, doc))
}

func fnRunFinish(ctx context.Context, a *API, _ *callEnv, raw jsontext.Value) (any, error) {
	rc, ok := RunFrom(ctx)
	if !ok || rc.Signal == nil {
		return nil, herr(sdk.ErrUnavailable, "run.finish is only available inside a run step")
	}
	args, err := decodeArgs[sdk.RunFinishArgs](raw)
	if err != nil {
		return nil, err
	}
	if len(args.Summary) > maxRunSummary {
		return nil, herr(sdk.ErrInvalidArgument, "summary over %d bytes", maxRunSummary)
	}
	rc.Signal.Set(true, "", args.Summary)
	return struct{}{}, nil
}

func fnRunFail(ctx context.Context, a *API, _ *callEnv, raw jsontext.Value) (any, error) {
	rc, ok := RunFrom(ctx)
	if !ok || rc.Signal == nil {
		return nil, herr(sdk.ErrUnavailable, "run.fail is only available inside a run step")
	}
	args, err := decodeArgs[sdk.RunFailArgs](raw)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(args.Reason)
	if reason == "" {
		return nil, herr(sdk.ErrInvalidArgument, "reason is required")
	}
	rc.Signal.Set(false, reason, nil)
	return struct{}{}, nil
}

func fnUploadRead(ctx context.Context, a *API, e *callEnv, raw jsontext.Value) (any, error) {
	rc, runs, err := a.runStore(ctx)
	if err != nil {
		return nil, err
	}
	args, err := decodeArgs[sdk.UploadReadArgs](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Uploads == nil {
		return nil, unavailable("upload.read")
	}
	id, err := parseID(args.UploadID)
	if err != nil {
		return nil, err
	}
	if args.Offset < 0 || args.Len < 1 || args.Len > maxUploadChunk {
		return nil, herr(sdk.ErrInvalidArgument, "offset must not be negative and len must be 1 to %d", maxUploadChunk)
	}
	run, err := runs.Get(ctx, rc.RunID)
	if err != nil {
		return nil, runErr(err)
	}
	if run.UploadID == nil || *run.UploadID != id {
		return nil, herr(sdk.ErrNotFound, "upload %s is not this run's upload", args.UploadID)
	}
	ra, size, err := a.svc.Uploads.Open(ctx, e.plugin, id)
	if err != nil {
		return nil, uploadOpenError(ctx, args.UploadID, err)
	}
	if c, ok := ra.(io.Closer); ok {
		defer c.Close()
	}
	n := max(min(args.Len, size-args.Offset), 0)
	buf := make([]byte, n)
	if n > 0 {
		if _, err := ra.ReadAt(buf, args.Offset); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
	}
	return sdk.UploadChunk{DataB64: base64.StdEncoding.EncodeToString(buf), Size: size, EOF: args.Offset+n >= size}, nil
}

// uploadOpenError maps a failed upload open. Only the caller's own ctx ending
// is transient (`timeout`: the host may still be fetching the file, so the same
// read can be retried). A failed download, including its own deadline, or a
// failed lookup is terminal.
func uploadOpenError(ctx context.Context, uploadID string, err error) *sdk.HostError {
	if ctx.Err() != nil {
		return herr(sdk.ErrTimeout, "upload %s is still being fetched; retry the read", uploadID)
	}
	return herr(sdk.ErrNotFound, "upload %s is not available", uploadID)
}

func fnBufferPut(ctx context.Context, a *API, _ *callEnv, raw jsontext.Value) (any, error) {
	rc, runs, err := a.runStore(ctx)
	if err != nil {
		return nil, err
	}
	args, err := decodeArgs[sdk.BufferPutArgs](raw)
	if err != nil {
		return nil, err
	}
	val, derr := base64.StdEncoding.DecodeString(args.ValueB64)
	if derr != nil {
		return nil, herr(sdk.ErrInvalidArgument, "value_b64 is not base64")
	}
	if err := runs.BufferPut(ctx, rc.RunID, args.Key, val); err != nil {
		if errors.Is(err, plugrun.ErrQuota) {
			return nil, herr(sdk.ErrInvalidArgument, "buffer quota exceeded: %v", err)
		}
		return nil, runErr(err)
	}
	return struct{}{}, nil
}

func fnBufferGet(ctx context.Context, a *API, _ *callEnv, raw jsontext.Value) (any, error) {
	rc, runs, err := a.runStore(ctx)
	if err != nil {
		return nil, err
	}
	args, err := decodeArgs[sdk.BufferGetArgs](raw)
	if err != nil {
		return nil, err
	}
	val, err := runs.BufferGet(ctx, rc.RunID, args.Key)
	if errors.Is(err, plugrun.ErrNotFound) {
		return sdk.BufferValue{}, nil
	}
	if err != nil {
		return nil, runErr(err)
	}
	return sdk.BufferValue{Found: true, ValueB64: base64.StdEncoding.EncodeToString(val)}, nil
}

func fnBufferList(ctx context.Context, a *API, _ *callEnv, raw jsontext.Value) (any, error) {
	rc, runs, err := a.runStore(ctx)
	if err != nil {
		return nil, err
	}
	args, err := decodeArgs[sdk.BufferListArgs](raw)
	if err != nil {
		return nil, err
	}
	limit := clamp(args.Limit, defaultListLimit, plugrun.MaxListPage)
	keys, err := runs.BufferList(ctx, rc.RunID, args.Prefix, args.After, limit)
	if err != nil {
		return nil, runErr(err)
	}
	out := sdk.BufferKeys{Keys: keys}
	if len(keys) == limit {
		out.Next = keys[len(keys)-1]
	}
	return out, nil
}

func fnMediaImportURL(ctx context.Context, a *API, e *callEnv, raw jsontext.Value) (any, error) {
	rc, ok := RunFrom(ctx)
	if !ok {
		return nil, herr(sdk.ErrPhaseDenied, "only available inside a run")
	}
	args, err := decodeArgs[sdk.MediaImportURLArgs](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Fetcher == nil || a.svc.Blobs == nil {
		return nil, unavailable("media.import_url")
	}
	if args.URL == "" || len(args.URL) > maxImportURLLen {
		return nil, herr(sdk.ErrInvalidArgument, "url is required and at most %d bytes", maxImportURLLen)
	}
	name := importFilename(args.Filename, args.URL)
	if name == "" {
		return nil, herr(sdk.ErrInvalidArgument, "filename is required when the url has no file name")
	}
	if !a.limits(e.plugin).fetch.Allow() {
		return nil, herr(sdk.ErrRateLimited, "http.fetch rate limit for this plugin reached; try again shortly")
	}
	hosts := rc.Hosts
	if !a.grants.Has(e.plugin, sdk.PermHTTPFetchAny) {
		hosts = withoutWildcard(hosts)
	}
	var handle string
	err = a.svc.Fetcher.Download(ctx, e.plugin, hosts, args.URL, maxImportBytes, func(r io.Reader) error {
		var serr error
		handle, serr = a.svc.Blobs.StageBlob(ctx, rc.RunID, r, name)
		return serr
	})
	var status *DownloadStatusError
	switch {
	case err == nil:
		return sdk.MediaHandle{Handle: handle}, nil
	case errors.Is(err, ErrDownloadTooLarge):
		return nil, herr(sdk.ErrInvalidArgument, "file over %d MiB", maxImportBytes>>20)
	case errors.As(err, &status):
		return nil, herr(sdk.ErrUnavailable, "download failed: %v", err)
	}
	return nil, err
}

// importFilename is the name a downloaded file is staged under: the plugin's
// choice, else the last element of the URL path. Never a path.
func importFilename(given, rawURL string) string {
	name := strings.TrimSpace(given)
	if name == "" {
		if u, err := url.Parse(rawURL); err == nil {
			name = path.Base(u.Path)
		}
	}
	if name == "." || name == "/" || strings.ContainsAny(name, `/\`) || len(name) > maxFilenameLen {
		return ""
	}
	return name
}

func fnSiteInfo(ctx context.Context, a *API, _ *callEnv, _ jsontext.Value) (any, error) {
	if a.svc.Site == nil {
		return nil, unavailable("site.info")
	}
	return a.svc.Site.Info(ctx)
}

func fnEntitiesSlugTaken(ctx context.Context, a *API, _ *callEnv, raw jsontext.Value) (any, error) {
	args, err := decodeArgs[sdk.SlugTakenArgs](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.SlugTaken == nil {
		return nil, unavailable("entities.slug_taken")
	}
	if args.Schema == "" || args.Slug == "" || len(args.Schema) > maxSlugCheckLen || len(args.Slug) > maxSlugCheckLen || len(args.Locale) > 35 {
		return nil, herr(sdk.ErrInvalidArgument, "schema and slug are required")
	}
	taken, err := a.svc.SlugTaken(ctx, args.Schema, args.Slug, args.Locale)
	if err != nil {
		return nil, err
	}
	return sdk.SlugTaken{Taken: taken}, nil
}
