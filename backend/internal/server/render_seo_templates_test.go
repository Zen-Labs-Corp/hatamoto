package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/core/internal/seo"
)

type seoRow struct {
	raw []byte
	err error
}

func (r seoRow) Scan(dst ...any) error {
	if r.err != nil {
		return r.err
	}
	*dst[0].(*[]byte) = r.raw
	return nil
}

type seoQuerier struct {
	row     seoRow
	queries int
}

func (q *seoQuerier) QueryRow(context.Context, string, ...any) pgx.Row { q.queries++; return q.row }
func (q *seoQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

const docTemplates = `{"types":{"doc":{"title":"%title% %sep% Docs","description":"Read %title% on %sitename%."}}}`

func seoPreviewHandlers(t *testing.T, stored string) *renderAdminHandlers {
	t.Helper()
	srv := &Server{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		seoStore: seo.NewStore(&seoQuerier{row: seoRow{raw: []byte(stored)}}),
	}
	h := newPreviewHandlers(t)
	h.seoSettings = srv.seoSettings
	return h
}

func seoPreviewBody(t *testing.T, attrs map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"schema": "doc", "name": "Install", "attributes": attrs})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSEOTemplatesDrivePreviewTitleAndDescription(t *testing.T) {
	h := seoPreviewHandlers(t, docTemplates)
	body := preview(t, h, seoPreviewBody(t, map[string]any{"meta_title": ""})).Body.String()
	for _, want := range []string{"<title>Install | Docs</title>", `content="Read Install on Draft Site."`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
}

func TestSEOHandWrittenMetaWinsOverTemplates(t *testing.T) {
	h := seoPreviewHandlers(t, docTemplates)
	body := preview(t, h, seoPreviewBody(t, map[string]any{"meta_title": "Mine", "meta_description": "My words"})).Body.String()
	for _, want := range []string{"<title>Mine | Draft Site</title>", `content="My words"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "Docs") {
		t.Error("template applied over a written title")
	}
}

func TestSEOMissingSettingsKeepLegacyTitle(t *testing.T) {
	h := seoPreviewHandlers(t, `{}`)
	body := preview(t, h, seoPreviewBody(t, map[string]any{})).Body.String()
	if !strings.Contains(body, "<title>Install | Draft Site</title>") {
		t.Errorf("title: %s", body)
	}
}

func TestApplyAttributeMetaFlagsWrittenValues(t *testing.T) {
	for _, tc := range []struct {
		attrs            string
		title, desc      bool
		wantTitleSetting string
	}{
		{`{"meta_title":"T","meta_description":"D"}`, true, true, "T"},
		{`{"meta_title":"  ","meta_description":" "}`, false, false, "Name"},
		{`{}`, false, false, "Name"},
	} {
		m := render.PageMeta{Title: "Name"}
		applyAttributeMeta(&m, json.RawMessage(tc.attrs), "https://site.test/")
		if m.TitleSet != tc.title || m.DescriptionSet != tc.desc || m.Title != tc.wantTitleSetting {
			t.Errorf("%s: got %+v", tc.attrs, m)
		}
	}
}

func TestSEOSettingsReadsTheCachedStore(t *testing.T) {
	q := &seoQuerier{row: seoRow{raw: []byte(docTemplates)}}
	srv := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil)), seoStore: seo.NewStore(q)}
	for range 3 {
		if got := srv.seoSettings(context.Background()); got == nil || got.Types["doc"].Title == "" {
			t.Fatalf("settings %+v", got)
		}
	}
	if q.queries != 1 {
		t.Fatalf("queries = %d, want one for three reads", q.queries)
	}
	if (&Server{}).seoSettings(context.Background()) != nil {
		t.Fatal("no store must give nil")
	}
}
