package guest

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"hatamoto.dev/sdk"
)

// Caller makes one host call: it runs the host function fn with args and
// decodes the result into out (nil ignores it). Call, on wasip1, is the real
// one; tests pass a fake built on Invoke.
type Caller func(fn string, args, out any) error

// OnRunStep registers the handler of a run's steps (export on_run_step). The
// host calls it again and again, in the fetch phase and then in the transform
// phase, until the handler calls RunFinish in each (or RunFail, or returns an
// error, which fails the run).
func OnRunStep(fn func(sdk.RunStep) (sdk.RunStepResult, error)) {
	register(sdk.RunStepExport, func(in []byte) ([]byte, error) {
		var step sdk.RunStep
		if err := json.Unmarshal(in, &step); err != nil {
			return nil, fmt.Errorf("decode run step: %w", err)
		}
		res, err := fn(step)
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	})
}

// PhaseDenied reports whether err is a host call refused because the function
// is not available in the run's current phase (or outside a run).
func PhaseDenied(err error) bool { return hostCode(err, sdk.ErrPhaseDenied) }

// NotOwned reports whether err is a changeset call refused because the object
// belongs to the owner or to another source.
func NotOwned(err error) bool { return hostCode(err, sdk.ErrNotOwned) }

func hostCode(err error, code string) bool {
	var he *sdk.HostError
	return errors.As(err, &he) && he.Code == code
}

// --- run ---------------------------------------------------------------------

// RunInfo returns what the host knows about the run: phase, cursor, the
// owner's inputs and the hosts they confirmed.
func RunInfo(call Caller) (sdk.RunInfo, error) {
	var out sdk.RunInfo
	err := call(sdk.FnRunInfo, struct{}{}, &out)
	return out, err
}

// RunCheckpoint stores cursor; the next on_run_step receives it.
func RunCheckpoint(call Caller, cursor string) error {
	return call(sdk.FnRunCheckpoint, sdk.RunCheckpointArgs{Cursor: cursor}, nil)
}

// RunProgress reports progress to the owner.
func RunProgress(call Caller, done, total int, message string) error {
	return call(sdk.FnRunProgress, sdk.RunProgressArgs{Done: done, Total: total, Message: message}, nil)
}

// RunFinish ends the current phase. summary (any JSON value, may be nil) is
// shown to the owner with the changeset.
func RunFinish(call Caller, summary any) error {
	args := sdk.RunFinishArgs{}
	if summary != nil {
		raw, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		args.Summary = raw
	}
	return call(sdk.FnRunFinish, args, nil)
}

// RunFail fails the run; its changeset is discarded.
func RunFail(call Caller, reason string) error {
	return call(sdk.FnRunFail, sdk.RunFailArgs{Reason: reason}, nil)
}

// --- upload and buffer -----------------------------------------------------------

// UploadRead reads up to n bytes of the owner's upload from offset (the host
// caps n at 256 KiB). eof is true when the read reached the end.
func UploadRead(call Caller, id string, offset, n int64) (data []byte, eof bool, err error) {
	var chunk sdk.UploadChunk
	if err := call(sdk.FnUploadRead, sdk.UploadReadArgs{UploadID: id, Offset: offset, Len: n}, &chunk); err != nil {
		return nil, false, err
	}
	data, err = base64.StdEncoding.DecodeString(chunk.DataB64)
	return data, chunk.EOF, err
}

// BufferPut stores value under key for the transform phase (fetch phase only).
func BufferPut(call Caller, key string, value []byte) error {
	return call(sdk.FnBufferPut, sdk.BufferPutArgs{Key: key, ValueB64: base64.StdEncoding.EncodeToString(value)}, nil)
}

// BufferGet returns the value stored under key (transform phase only).
func BufferGet(call Caller, key string) (value []byte, found bool, err error) {
	var out sdk.BufferValue
	if err := call(sdk.FnBufferGet, sdk.BufferGetArgs{Key: key}, &out); err != nil || !out.Found {
		return nil, false, err
	}
	value, err = base64.StdEncoding.DecodeString(out.ValueB64)
	return value, true, err
}

// BufferList lists keys with prefix after the key `after`, in order. next is
// the value to pass as `after` for the following page, "" at the end.
func BufferList(call Caller, prefix, after string, limit int) (keys []string, next string, err error) {
	var out sdk.BufferKeys
	err = call(sdk.FnBufferList, sdk.BufferListArgs{Prefix: prefix, After: after, Limit: limit}, &out)
	return out.Keys, out.Next, err
}

// MediaImportURL downloads url from one of the run's hosts into a staged
// file and returns its handle for MediaAttach (fetch phase only).
func MediaImportURL(call Caller, url, filename string) (handle string, err error) {
	var out sdk.MediaHandle
	err = call(sdk.FnMediaImportURL, sdk.MediaImportURLArgs{URL: url, Filename: filename}, &out)
	return out.Handle, err
}

// --- site reads (transform phase) ------------------------------------------------

// SiteInfo returns the site's title, tagline, time zone, language and locales.
func SiteInfo(call Caller) (sdk.SiteInfo, error) {
	var out sdk.SiteInfo
	err := call(sdk.FnSiteInfo, struct{}{}, &out)
	return out, err
}

// SlugTaken reports whether a published, public entity of schema already
// holds slug.
func SlugTaken(call Caller, schema, slug, locale string) (bool, error) {
	var out sdk.SlugTaken
	err := call(sdk.FnEntitiesSlugTaken, sdk.SlugTakenArgs{Schema: schema, Slug: slug, Locale: locale}, &out)
	return out.Taken, err
}

// --- changeset (transform phase) -------------------------------------------------

func stage(call Caller, fn string, in any) (sdk.UpsertResult, error) {
	var out sdk.UpsertResult
	err := call(fn, in, &out)
	return out, err
}

// EntityUpsert stages the creation or update of an entity.
func EntityUpsert(call Caller, in sdk.EntityUpsert) (sdk.UpsertResult, error) {
	return stage(call, sdk.FnChangesetEntityUpsert, in)
}

// TermUpsert stages a term.
func TermUpsert(call Caller, in sdk.TermUpsert) (sdk.UpsertResult, error) {
	return stage(call, sdk.FnChangesetTermUpsert, in)
}

// PersonUpsert stages a byline.
func PersonUpsert(call Caller, in sdk.PersonUpsert) (sdk.UpsertResult, error) {
	return stage(call, sdk.FnChangesetPersonUpsert, in)
}

// MediaAttach stages a media item from a MediaImportURL handle.
func MediaAttach(call Caller, in sdk.MediaAttach) (sdk.UpsertResult, error) {
	return stage(call, sdk.FnChangesetMediaAttach, in)
}

// MediaAlt stages an alt text change for an owner media item (the run declares "media_alt").
func MediaAlt(call Caller, in sdk.MediaAlt) (sdk.UpsertResult, error) {
	return stage(call, sdk.FnChangesetMediaAlt, in)
}

// MenuUpsert stages a menu.
func MenuUpsert(call Caller, in sdk.MenuUpsert) (sdk.UpsertResult, error) {
	return stage(call, sdk.FnChangesetMenuUpsert, in)
}

// RedirectUpsert stages a redirect.
func RedirectUpsert(call Caller, in sdk.RedirectUpsert) (sdk.UpsertResult, error) {
	return stage(call, sdk.FnChangesetRedirectUpsert, in)
}

// Delete stages the deletion of an object an earlier run created. kind is
// entity, term, menu, media or redirect.
func Delete(call Caller, kind, sourceKey string) (sdk.UpsertResult, error) {
	return stage(call, sdk.FnChangesetDelete, sdk.ChangesetDeleteArgs{Kind: kind, SourceKey: sourceKey})
}

// Propose submits a change for the owner to sign off separately. kind must be
// listed in the run's "proposes".
func Propose(call Caller, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return call(sdk.FnChangesetPropose, sdk.Proposal{Kind: kind, Payload: raw}, nil)
}
