package mcpserver

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/entity"
)

// versionRow is one stored version in a list: no attributes, so a long
// history stays small. entities_version_get returns them.
type versionRow struct {
	Version       int        `json:"version"`
	CreatedAt     time.Time  `json:"created_at"`
	ChangedBy     string     `json:"changed_by,omitempty"`
	ChangeSummary string     `json:"change_summary,omitempty"`
	LiveAt        *time.Time `json:"live_at,omitempty"`
}

// versionDoc is one stored version with its attributes exactly as stored.
type versionDoc struct {
	versionRow
	Attributes jsontext.Value `json:"attributes"`
}

// whoFunc resolves the person behind a version to a display name, once per
// person per call. No person gives "" (the field is omitted); a person the
// lookup cannot name gives "Unknown user".
func (s *Server) whoFunc(ctx context.Context) func(*uuid.UUID) string {
	seen := map[uuid.UUID]string{}
	return func(id *uuid.UUID) string {
		if id == nil {
			return ""
		}
		if name, ok := seen[*id]; ok {
			return name
		}
		name := "Unknown user"
		if s.d.UserName != nil {
			if n, ok := s.d.UserName(ctx, *id); ok && n != "" {
				name = n
			}
		}
		seen[*id] = name
		return name
	}
}

func (s *Server) versionRowOf(v *entity.EntityVersion, who func(*uuid.UUID) string) versionRow {
	row := versionRow{Version: v.Version, CreatedAt: v.CreatedAt, ChangedBy: who(v.ChangedBy), LiveAt: v.LiveAt}
	if v.ChangeSummary != nil {
		row.ChangeSummary = *v.ChangeSummary
	}
	return row
}

func (s *Server) registerVersions() {
	type versionsIn struct {
		ID    string `json:"id" jsonschema:"entity id (UUID)"`
		Page  *int   `json:"page,omitempty" jsonschema:"page number, 1-based (default 1)"`
		Limit *int   `json:"limit,omitempty" jsonschema:"versions per page, default 20, max 100 (larger values are clamped)"`
	}
	register(s, toolSpec{name: "entities_versions", description: "List an entity's stored earlier versions, newest first, without their attributes (read one with entities_version_get). current_version is the entity's live version: pass it as expected_version to entities_restore.", scope: readScope, action: authz.ActionEntityRead, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in versionsIn) (any, error) {
			id, err := parseID("id", in.ID)
			if err != nil {
				return nil, err
			}
			page, limit := 1, 20
			if in.Page != nil {
				if *in.Page < 1 {
					return nil, userErr("page must be at least 1")
				}
				page = *in.Page
			}
			if in.Limit != nil {
				if *in.Limit < 1 {
					return nil, userErr("limit must be at least 1")
				}
				limit = min(*in.Limit, maxListLen)
			}
			e, err := s.d.Entities.GetEntity(ctx, id)
			if err != nil {
				return nil, err
			}
			res, err := s.d.Entities.ListVersionsPage(ctx, id, entity.OffsetPage{Page: page, Limit: limit})
			if err != nil {
				return nil, err
			}
			who := s.whoFunc(ctx)
			rows := make([]versionRow, len(res.Items))
			for i, v := range res.Items {
				rows[i] = s.versionRowOf(v, who)
			}
			return object{"versions": rows, "current_version": e.Version, "total": res.Total, "page": res.Page, "limit": res.Limit, "total_pages": res.TotalPages}, nil
		})

	type versionGetIn struct {
		ID      string `json:"id" jsonschema:"entity id (UUID)"`
		Version int    `json:"version" jsonschema:"version number to read (from entities_versions)"`
	}
	register(s, toolSpec{name: "entities_version_get", description: "Read one stored version of an entity with its attributes exactly as stored (page_doc included).", scope: readScope, action: authz.ActionEntityRead, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in versionGetIn) (any, error) {
			id, err := parseID("id", in.ID)
			if err != nil {
				return nil, err
			}
			if in.Version < 1 {
				return nil, userErr("version must be at least 1")
			}
			v, err := s.d.Entities.GetVersion(ctx, id, in.Version)
			if err != nil {
				return nil, err
			}
			return versionDoc{versionRow: s.versionRowOf(v, s.whoFunc(ctx)), Attributes: v.Attributes}, nil
		})

	type restoreIn struct {
		ID              string `json:"id" jsonschema:"entity id (UUID)"`
		Version         int    `json:"version" jsonschema:"the stored version to restore (from entities_versions)"`
		ExpectedVersion *int   `json:"expected_version,omitempty" jsonschema:"the entity's current_version; when set, a stale version is refused and nothing is stored"`
	}
	register(s, toolSpec{name: "entities_restore", description: "Restore an entity's attributes from a stored version. This stores a new version (history is kept) and follows the same review and policy rules as an update: a published entry may be held for review.", scope: writeScope, action: authz.ActionEntityVersionRestore, write: true, destructive: true},
		func(ctx context.Context, key *hmauth.APIKey, in restoreIn) (any, error) {
			id, err := parseID("id", in.ID)
			if err != nil {
				return nil, err
			}
			e, err := s.d.Entities.RestoreVersionIf(ctx, id, key.CreatedBy, in.Version, in.ExpectedVersion,
				entity.RestoreSummaryTag("[mcp:"+key.Name+"]"))
			if res, ok := pendingReview(id, err); ok {
				return res, nil
			}
			var vc *entity.VersionConflictError
			if errors.As(err, &vc) {
				if in.ExpectedVersion != nil {
					return nil, codedErr(codeConflict, "stale version %d; current version is %d (re-read with entities_get)", *in.ExpectedVersion, vc.Current)
				}
				return nil, codedErr(codeConflict, "the entry changed while restoring; current version is %d (re-read with entities_get)", vc.Current)
			}
			if err != nil {
				return nil, err
			}
			return s.summary(ctx, e, true), nil
		})
}
