package admin

import (
	"context"
	"net/http"
	"sync"
	"testing"
)

// fakeComments is an in-memory CommentsService that records what it was asked.
type fakeComments struct {
	mu         sync.Mutex
	settings   CommentSettings
	rows       []CommentRow
	counts     CommentCounts
	calls      []string
	lastIDs    []string
	lastTeach  SpamTeach
	lastActor  string
	lastFilter CommentFilter
	lastBody   string
	lastAuthor string
	lastMode   string
	entity     EntityComments
	schemaMode string
	types      []SchemaCommentConfig
	supports   map[string]bool
	blocklists []Blocklist
	commenters []CommenterRow
	options    []EntityOption
	hasMore    bool
	blocked    bool // Vouch refuses: the commenter is blocked
	skip       int  // rows a bulk action cannot change (stale state)
	err        error
}

var _ CommentsService = (*fakeComments)(nil)

// record notes a call and its ids under the lock; callers hold f.mu.
func (f *fakeComments) record(name, actor string, ids ...string) {
	f.calls = append(f.calls, name)
	f.lastIDs = ids
	f.lastActor = actor
}

func (f *fakeComments) Supports(feature string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.supports[feature]
}

func (f *fakeComments) Settings(context.Context) (CommentSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings, f.err
}

func (f *fakeComments) SaveSettings(_ context.Context, s CommentSettings, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.record("save-settings", actor)
	f.settings = s
	return nil
}

func (f *fakeComments) SetEnabled(_ context.Context, on bool, actor string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	f.record("set-enabled", actor)
	was := f.settings.Enabled
	f.settings.Enabled = on
	return was, nil
}

func (f *fakeComments) Counts(context.Context) (CommentCounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts, f.err
}

func (f *fakeComments) Queue(_ context.Context, fl CommentFilter) (CommentPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastFilter = fl
	if f.err != nil {
		return CommentPage{}, f.err
	}
	rows := f.rows
	if fl.PerPage > 0 && len(rows) > fl.PerPage {
		rows = rows[:fl.PerPage]
	}
	return CommentPage{Rows: append([]CommentRow(nil), rows...), HasMore: f.hasMore}, nil
}

func (f *fakeComments) Comment(_ context.Context, id string) (CommentRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return CommentRow{}, f.err
	}
	for _, r := range f.rows {
		if r.ID == id {
			return r, nil
		}
	}
	return CommentRow{}, ErrCommentNotFound
}

func (f *fakeComments) Approve(_ context.Context, ids []string, actor string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.record("approve", actor, ids...)
	return max(len(ids)-f.skip, 0), nil
}

func (f *fakeComments) Vouch(_ context.Context, commenterID, actor string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	if f.blocked {
		return "", ErrCommentBlocked
	}
	f.record("vouch", actor, commenterID)
	return "known", nil
}

func (f *fakeComments) MarkSpam(_ context.Context, ids []string, teach SpamTeach, actor string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.record("spam", actor, ids...)
	f.lastTeach = teach
	return max(len(ids)-f.skip, 0), nil
}

func (f *fakeComments) Trash(_ context.Context, ids []string, actor string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.record("trash", actor, ids...)
	return max(len(ids)-f.skip, 0), nil
}

func (f *fakeComments) Delete(_ context.Context, ids []string, actor string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.record("delete", actor, ids...)
	return max(len(ids)-f.skip, 0), nil
}

func (f *fakeComments) Edit(_ context.Context, id, body, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.record("edit", actor, id)
	f.lastBody = body
	return nil
}

func (f *fakeComments) Reply(_ context.Context, parentID, body, actor, author string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	f.record("reply", actor, parentID)
	f.lastBody, f.lastAuthor = body, author
	return "reply-id", nil
}

func (f *fakeComments) EntityComments(context.Context, string) (EntityComments, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entity, f.err
}

func (f *fakeComments) SetEntityMode(_ context.Context, entityID, mode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.record("entity-mode", "", entityID)
	f.lastMode = mode
	return nil
}

func (f *fakeComments) SchemaMode(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.schemaMode, f.err
}

func (f *fakeComments) SetSchemaMode(_ context.Context, schemaID, mode string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.record("schema-mode", "", schemaID)
	f.lastMode = mode
	f.schemaMode = mode
	return nil
}

func (f *fakeComments) SchemaCommentConfigs(context.Context) ([]SchemaCommentConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SchemaCommentConfig(nil), f.types...), f.err
}

func (f *fakeComments) SetSchemaComments(_ context.Context, schemaID, order string, p CommentPolicy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	for i := range f.types {
		if f.types[i].ID != schemaID {
			continue
		}
		if f.types[i].Builtin && !f.supports["schema-mode-builtin"] {
			return ErrCommentsUnsupported
		}
		f.record("schema-comments", "", schemaID)
		f.types[i].Order, f.types[i].Policy = order, p
		return nil
	}
	return ErrCommentNotFound
}

func (f *fakeComments) EntityOptions(_ context.Context, limit int) ([]EntityOption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit > 0 && len(f.options) > limit {
		return f.options[:limit], f.err
	}
	return f.options, f.err
}

func (f *fakeComments) Commenters(context.Context, string, int) (CommenterPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return CommenterPage{Rows: append([]CommenterRow(nil), f.commenters...), HasMore: f.hasMore}, f.err
}

func (f *fakeComments) Block(_ context.Context, commenterID, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.record("block", actor, commenterID)
	return nil
}

func (f *fakeComments) RevokeDevices(_ context.Context, commenterID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.record("signout", "", commenterID)
	return nil
}

func (f *fakeComments) Blocklists(context.Context) ([]Blocklist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Blocklist(nil), f.blocklists...), f.err
}

func (f *fakeComments) SaveBlocklist(_ context.Context, kind string, _ []string, actor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.record("blocklist", actor, kind)
	return nil
}

func (f *fakeComments) ExportCommenter(_ context.Context, commenterID string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.record("export", "", commenterID)
	return []byte("{}"), nil
}

func (f *fakeComments) EraseCommenter(_ context.Context, commenterID string) (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, 0, f.err
	}
	f.record("erase", "", commenterID)
	return 1, 0, nil
}

// newCommentsRig is the owner rig with the fake comment service wired.
func newCommentsRig(t *testing.T, svc *fakeComments) *perfRig {
	t.Helper()
	p := newPerfRig(t, nil)
	p.a.deps.Comments = svc
	p.a.deps.BaseURL = func(*http.Request) string { return "https://example.com" }
	return p
}
