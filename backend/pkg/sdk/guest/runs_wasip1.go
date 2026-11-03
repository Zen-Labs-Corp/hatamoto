//go:build wasip1

package guest

import "hatamoto.dev/sdk"

// The Host variants below are the run functions of runs.go bound to the real
// host call, so a plugin writes guest.EntityUpsertHost(in) instead of passing
// guest.Call by hand.

// RunInfoHost is RunInfo bound to the host.
func RunInfoHost() (sdk.RunInfo, error) {
	return RunInfo(Call)
}

// RunCheckpointHost is RunCheckpoint bound to the host.
func RunCheckpointHost(cursor string) error {
	return RunCheckpoint(Call, cursor)
}

// RunProgressHost is RunProgress bound to the host.
func RunProgressHost(done, total int, message string) error {
	return RunProgress(Call, done, total, message)
}

// RunFinishHost is RunFinish bound to the host.
func RunFinishHost(summary any) error {
	return RunFinish(Call, summary)
}

// RunFailHost is RunFail bound to the host.
func RunFailHost(reason string) error {
	return RunFail(Call, reason)
}

// UploadReadHost is UploadRead bound to the host.
func UploadReadHost(id string, offset, n int64) ([]byte, bool, error) {
	return UploadRead(Call, id, offset, n)
}

// BufferPutHost is BufferPut bound to the host.
func BufferPutHost(key string, value []byte) error {
	return BufferPut(Call, key, value)
}

// BufferGetHost is BufferGet bound to the host.
func BufferGetHost(key string) ([]byte, bool, error) {
	return BufferGet(Call, key)
}

// BufferListHost is BufferList bound to the host.
func BufferListHost(prefix, after string, limit int) ([]string, string, error) {
	return BufferList(Call, prefix, after, limit)
}

// MediaImportURLHost is MediaImportURL bound to the host.
func MediaImportURLHost(url, filename string) (string, error) {
	return MediaImportURL(Call, url, filename)
}

// SiteInfoHost is SiteInfo bound to the host.
func SiteInfoHost() (sdk.SiteInfo, error) {
	return SiteInfo(Call)
}

// SlugTakenHost is SlugTaken bound to the host.
func SlugTakenHost(schema, slug, locale string) (bool, error) {
	return SlugTaken(Call, schema, slug, locale)
}

// EntityUpsertHost is EntityUpsert bound to the host.
func EntityUpsertHost(in sdk.EntityUpsert) (sdk.UpsertResult, error) {
	return EntityUpsert(Call, in)
}

// TermUpsertHost is TermUpsert bound to the host.
func TermUpsertHost(in sdk.TermUpsert) (sdk.UpsertResult, error) {
	return TermUpsert(Call, in)
}

// PersonUpsertHost is PersonUpsert bound to the host.
func PersonUpsertHost(in sdk.PersonUpsert) (sdk.UpsertResult, error) {
	return PersonUpsert(Call, in)
}

// MediaAttachHost is MediaAttach bound to the host.
func MediaAttachHost(in sdk.MediaAttach) (sdk.UpsertResult, error) {
	return MediaAttach(Call, in)
}

// MenuUpsertHost is MenuUpsert bound to the host.
func MenuUpsertHost(in sdk.MenuUpsert) (sdk.UpsertResult, error) {
	return MenuUpsert(Call, in)
}

// RedirectUpsertHost is RedirectUpsert bound to the host.
func RedirectUpsertHost(in sdk.RedirectUpsert) (sdk.UpsertResult, error) {
	return RedirectUpsert(Call, in)
}

// DeleteHost is Delete bound to the host.
func DeleteHost(kind, sourceKey string) (sdk.UpsertResult, error) {
	return Delete(Call, kind, sourceKey)
}

// ProposeHost is Propose bound to the host.
func ProposeHost(kind string, payload any) error {
	return Propose(Call, kind, payload)
}
