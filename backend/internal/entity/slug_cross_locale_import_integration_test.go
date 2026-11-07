//go:build integration

package entity

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func crossLocaleImportRig(t *testing.T) (*Service, func(key, slug, locale string) ImportItem) {
	t.Helper()
	svc, schemaID, _ := localeFixture(t, `{"locale_neutral":true}`)
	useLocales(t, svc, "en", "fr")
	schema, err := svc.GetSchema(context.Background(), schemaID)
	if err != nil {
		t.Fatal(err)
	}
	pfx := "xl-" + uuid.NewString()[:8]
	item := func(key, slug, locale string) ImportItem {
		return ImportItem{Key: pfx + key, SchemaSlug: schema.Slug, Name: slug, Slug: slug, Locale: locale, Attributes: jsontext.Value(`{}`)}
	}
	return svc, item
}

func importOne(t *testing.T, svc *Service, dry bool, items ...ImportItem) *ImportResult {
	t.Helper()
	res, err := svc.Import(context.Background(), nil, ImportBatch{Source: "test", DryRun: dry, Items: items})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestImportUpsert_CrossLocaleSlugRefused(t *testing.T) {
	svc, item := crossLocaleImportRig(t)
	if res := importOne(t, svc, false, item("en", "about", "en"), item("other", "other", "en")); res.Failed != 0 {
		t.Fatalf("seed: %+v", res)
	}

	// Insert branch: an all-languages row (no locale on a flagged type) over en.
	for _, dry := range []bool{false, true} {
		res := importOne(t, svc, dry, item("und", "about", ""))
		if res.Failed != 1 || !errors.Is(res.Items[0].Err, ErrSlugAlreadyExists) {
			t.Errorf("insert (dry=%v) = %+v, want ErrSlugAlreadyExists", dry, res.Items[0])
		}
	}
	// Language-specific fr beside en stays allowed.
	if res := importOne(t, svc, false, item("fr", "about", "fr")); res.Failed != 0 {
		t.Errorf("fr beside en: %+v", res.Items[0])
	}

	// Update branch: the und row exists under another slug; moving it onto
	// "about" lands beside en/fr rows.
	if res := importOne(t, svc, false, item("shared", "shared", "")); res.Failed != 0 {
		t.Fatalf("und seed: %+v", res.Items[0])
	}
	for _, dry := range []bool{false, true} {
		res := importOne(t, svc, dry, item("shared", "about", ""))
		if res.Failed != 1 || !errors.Is(res.Items[0].Err, ErrSlugAlreadyExists) {
			t.Errorf("update (dry=%v) = %+v, want ErrSlugAlreadyExists", dry, res.Items[0])
		}
	}
	// Re-importing a row unchanged is not a change.
	if res := importOne(t, svc, false, item("shared", "shared", "")); res.Failed != 0 {
		t.Errorf("unchanged re-import: %+v", res.Items[0])
	}
}

func TestImportDryRun_CrossLocaleInFileDuplicate(t *testing.T) {
	svc, item := crossLocaleImportRig(t)
	res := importOne(t, svc, true, item("a", "dup", ""), item("b", "dup", "en"))
	if res.Created != 1 || res.Failed != 1 || !errors.Is(res.Items[1].Err, ErrSlugAlreadyExists) {
		t.Fatalf("und then en = %+v", res)
	}
	res = importOne(t, svc, true, item("a", "dup", "en"), item("b", "dup", ""))
	if res.Created != 1 || res.Failed != 1 || !errors.Is(res.Items[1].Err, ErrSlugAlreadyExists) {
		t.Fatalf("en then und = %+v", res)
	}
	res = importOne(t, svc, true, item("a", "dup", "en"), item("b", "dup", "fr"))
	if res.Created != 2 || res.Failed != 0 {
		t.Fatalf("en then fr = %+v", res)
	}
}
