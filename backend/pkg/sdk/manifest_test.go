package sdk

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestManifestValidate(t *testing.T) {
	ok := Manifest{Name: "hello-world", Version: "0.1.0", SDK: "^0.5"}
	tests := []struct {
		name    string
		mutate  func(m *Manifest)
		wantErr string
	}{
		{"valid", func(m *Manifest) {}, ""},
		{"explicit limits", func(m *Manifest) {
			m.Limits = Limits{MemoryPages: 64, MaxInstances: 1, HookTimeoutsMS: map[string]uint64{"entity.published": 10}}
		}, ""},
		{"empty name", func(m *Manifest) { m.Name = "" }, "invalid plugin name"},
		{"uppercase name", func(m *Manifest) { m.Name = "Hello" }, "invalid plugin name"},
		{"bad version", func(m *Manifest) { m.Version = "nope" }, "version"},
		{"bad range", func(m *Manifest) { m.SDK = "" }, "sdk range"},
		{"too few pages", func(m *Manifest) { m.Limits.MemoryPages = 8 }, "memory_pages"},
		{"too many pages", func(m *Manifest) { m.Limits.MemoryPages = MaxMemoryPages + 1 }, "memory_pages"},
		{"hook timeouts", func(m *Manifest) {
			m.Cron, m.Funcs = []CronJob{{Name: "sync", Every: "5m"}}, []string{"badge"}
			m.Limits.HookTimeoutsMS = map[string]uint64{"render.page": 500, "entity.created": 1, "http_handle": 10000, "on_cron_sync": 5, "func_badge": 20}
		}, ""},
		{"zero hook timeout rejected", func(m *Manifest) { m.Limits.HookTimeoutsMS = map[string]uint64{"render.page": 0} }, "is 0"},
		{"hook timeout too long", func(m *Manifest) { m.Limits.HookTimeoutsMS = map[string]uint64{"render.page": MaxTimeoutMS + 1} }, "hook_timeouts_ms"},
		{"unknown hook timeout", func(m *Manifest) { m.Limits.HookTimeoutsMS = map[string]uint64{"render.nothing": 5} }, "unknown hook"},
		{"cron timeout without the job", func(m *Manifest) { m.Limits.HookTimeoutsMS = map[string]uint64{"on_cron_sync": 5} }, "unknown hook"},
		{"too many instances", func(m *Manifest) { m.Limits.MaxInstances = 99 }, "max_instances"},
		{"empty host", func(m *Manifest) { m.AllowedHosts = []string{""} }, "allowed_hosts"},
		{"permissions", func(m *Manifest) { m.Permissions = []string{PermKV, PermDB, PermHTTPFetch} }, ""},
		{"wildcard host with its permission", func(m *Manifest) {
			m.AllowedHosts, m.Permissions = []string{"*"}, []string{PermHTTPFetch, PermHTTPFetchAny}
		}, ""},
		{"wildcard host without http.fetch.any", func(m *Manifest) {
			m.AllowedHosts, m.Permissions = []string{"*"}, []string{PermHTTPFetch}
		}, "http.fetch.any"},
		{"http.fetch.any without http.fetch", func(m *Manifest) { m.Permissions = []string{PermHTTPFetchAny} }, "needs"},
		{"unknown permission", func(m *Manifest) { m.Permissions = []string{"root"} }, "unknown permission"},
		{"duplicate permission", func(m *Manifest) { m.Permissions = []string{PermKV, PermKV} }, "duplicate permission"},
		{"routes", func(m *Manifest) {
			m.Routes = []Route{{Method: "GET", Path: "/a"}, {Method: "POST", Path: "/a", Roles: []string{"editor"}}, {Method: "GET", Path: "/f/{rest...}", Public: true}}
		}, ""},
		{"route bad method", func(m *Manifest) { m.Routes = []Route{{Method: "TRACE", Path: "/a"}} }, "method"},
		{"route bad path", func(m *Manifest) { m.Routes = []Route{{Method: "GET", Path: "a"}} }, "must start with /"},
		{"route bad pattern", func(m *Manifest) { m.Routes = []Route{{Method: "GET", Path: "/{x"}} }, "routes"},
		{"route duplicate", func(m *Manifest) { m.Routes = []Route{{Method: "GET", Path: "/a"}, {Method: "GET", Path: "/a"}} }, "routes"},
		{"route bad role", func(m *Manifest) { m.Routes = []Route{{Method: "GET", Path: "/a", Roles: []string{"root"}}} }, "role"},
		{"public route with roles", func(m *Manifest) {
			m.Routes = []Route{{Method: "GET", Path: "/a", Public: true, Roles: []string{"owner"}}}
		}, "public route has no roles"},
		{"cron ok", func(m *Manifest) { m.Cron = []CronJob{{Name: "sync", Every: "1m"}, {Name: "daily", Every: "24h"}} }, ""},
		{"cron too fast", func(m *Manifest) { m.Cron = []CronJob{{Name: "sync", Every: "59s"}} }, "minimum"},
		{"cron bad duration", func(m *Manifest) { m.Cron = []CronJob{{Name: "sync", Every: "often"}} }, "every"},
		{"cron bad name", func(m *Manifest) { m.Cron = []CronJob{{Name: "Sync-1", Every: "5m"}} }, "cron name"},
		{"cron duplicate", func(m *Manifest) { m.Cron = []CronJob{{Name: "a", Every: "5m"}, {Name: "a", Every: "6m"}} }, "duplicate cron"},
		{"funcs", func(m *Manifest) { m.Funcs = []string{"badge", "price_fmt"} }, ""},
		{"bad func", func(m *Manifest) { m.Funcs = []string{"Bad"} }, "func"},
		{"cookies", func(m *Manifest) { m.Cookies = []string{"unlock", "ab_test-1"} }, ""},
		{"builder sources", func(m *Manifest) {
			m.Permissions = []string{PermBuilderData}
			m.BuilderSources = []BuilderSource{{Name: "temp", Label: "Temperature", Kind: "value", Class: "text"}, {Name: "forecast", Label: "Forecast", Kind: "items"}, {Name: "link", Label: "Link", Kind: "value", Class: "url"}}
			m.Limits.HookTimeoutsMS = map[string]uint64{"builder.data": 300}
		}, ""},
		{"too many builder sources", func(m *Manifest) {
			m.Permissions = []string{PermBuilderData}
			for i := 0; i <= MaxBuilderSources; i++ {
				m.BuilderSources = append(m.BuilderSources, BuilderSource{Name: "s" + string(rune('a'+i)), Label: "L", Kind: "items"})
			}
		}, "more than 8"},
		{"builder source bad name", func(m *Manifest) {
			m.Permissions, m.BuilderSources = []string{PermBuilderData}, []BuilderSource{{Name: "Bad", Label: "L", Kind: "items"}}
		}, "builder source name"},
		{"builder source duplicate", func(m *Manifest) {
			m.Permissions, m.BuilderSources = []string{PermBuilderData}, []BuilderSource{{Name: "a", Label: "L", Kind: "items"}, {Name: "a", Label: "L", Kind: "items"}}
		}, "duplicate builder source"},
		{"builder items with class", func(m *Manifest) {
			m.Permissions, m.BuilderSources = []string{PermBuilderData}, []BuilderSource{{Name: "a", Label: "L", Kind: "items", Class: "text"}}
		}, "value sources only"},
		{"builder value bad class", func(m *Manifest) {
			m.Permissions, m.BuilderSources = []string{PermBuilderData}, []BuilderSource{{Name: "a", Label: "L", Kind: "value", Class: "html"}}
		}, "class"},
		{"builder source unknown kind", func(m *Manifest) {
			m.Permissions, m.BuilderSources = []string{PermBuilderData}, []BuilderSource{{Name: "a", Label: "L", Kind: "list"}}
		}, "kind"},
		{"builder source empty label", func(m *Manifest) {
			m.Permissions, m.BuilderSources = []string{PermBuilderData}, []BuilderSource{{Name: "a", Kind: "items"}}
		}, "label"},
		{"builder source without permission", func(m *Manifest) {
			m.BuilderSources = []BuilderSource{{Name: "a", Label: "L", Kind: "items"}}
		}, "builder.data"},
		{"bad cookie", func(m *Manifest) { m.Cookies = []string{"a b"} }, "cookie"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := ok
			tt.mutate(&m)
			err := m.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestCompatible(t *testing.T) {
	for rng, want := range map[string]bool{"^0.5": true, "^0.4": false, ">=0.5 <1": true, "^0.8": true, "^0.9": true, "^0.10": true, "^0.11": true, "^0.12": true, "^0.13": true, "^0.14": true, "^0.15": true, "^0.16": true, "^0.17": false} {
		got, err := Manifest{SDK: rng}.Compatible()
		if err != nil || got != want {
			t.Errorf("Compatible(%s) = %v,%v want %v", rng, got, err, want)
		}
	}
}

// Slim render.page blocks are for plugins built for 0.9 or later, which must
// include a range such as ^0.10 that does not contain 0.9.0 itself.
func TestFullPageBlocksByRange(t *testing.T) {
	for rng, full := range map[string]bool{"^0.8": true, ">=0.5 <0.9": true, "^0.9": false, "^0.10": false, ">=0.5 <1": false} {
		if got := (Manifest{SDK: rng}).FullPageBlocks(); got != full {
			t.Errorf("FullPageBlocks(%s) = %v, want %v", rng, got, full)
		}
	}
}

func TestExportNames(t *testing.T) {
	if got := EntityPublished.Export(); got != "on_entity_published" {
		t.Fatal(got)
	}
	if got := JSONLDEnrich.Export(); got != "filter_jsonld_enrich" {
		t.Fatal(got)
	}
	if got := EntityBeforeSave.Export(); got != "filter_entity_before_save" {
		t.Fatal(got)
	}
}

func TestManifestUIValidation(t *testing.T) {
	base := Manifest{Name: "ui-plugin", Version: "1.0.0", SDK: "^0.7"}
	for name, ui := range map[string]*AdminUI{
		"ok":          {Title: "Leads"},
		"ok entry":    {Title: "Leads", Entry: "app/main.html", Roles: []string{"owner", "editor"}},
		"no title":    {},
		"long title":  {Title: strings.Repeat("x", 41)},
		"traversal":   {Title: "x", Entry: "../x.html"},
		"absolute":    {Title: "x", Entry: "/x.html"},
		"not html":    {Title: "x", Entry: "x.js"},
		"viewer role": {Title: "x", Roles: []string{"viewer"}},
	} {
		m := base
		m.UI = ui
		err := m.Validate()
		if want := strings.HasPrefix(name, "ok"); (err == nil) != want {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestHookTimeout(t *testing.T) {
	m := Manifest{Limits: Limits{HookTimeoutsMS: map[string]uint64{"render.page": 40, "entity.published": 7, "func_x": 9, "on_cron_a": 3}}}
	ms := time.Millisecond
	for export, want := range map[string]time.Duration{
		"filter_jsonld_enrich":      250 * ms, // filter default
		"filter_render_page":        40 * ms,  // by hook name
		"on_entity_created":         10 * time.Second,
		"on_entity_published":       7 * ms,
		"http_handle":               10 * time.Second,
		"func_y":                    250 * ms,
		"func_x":                    9 * ms, // by export name
		"on_cron_a":                 3 * ms,
		"on_cron_b":                 10 * time.Second,
		"filter_sitemap_urls":       250 * ms,
		"filter_entity_before_save": 250 * ms,
	} {
		if got := m.HookTimeout(export); got != want {
			t.Errorf("%s: %s, want %s", export, got, want)
		}
	}
}

func TestBuilderDataHook(t *testing.T) {
	if got := BuilderData.Export(); got != "filter_builder_data" {
		t.Fatal(got)
	}
	m := Manifest{Limits: Limits{HookTimeoutsMS: map[string]uint64{"builder.data": 40}}}
	if got := (Manifest{}).HookTimeout("filter_builder_data"); got != 250*time.Millisecond {
		t.Errorf("default = %s", got)
	}
	if got := m.HookTimeout("filter_builder_data"); got != 40*time.Millisecond {
		t.Errorf("override = %s", got)
	}
	if !ValidPermission(PermBuilderData) {
		t.Error("builder.data permission unknown")
	}
}

func TestBuilderDataRoundTrip(t *testing.T) {
	in := BuilderDataRequest{
		Path: "/a", Language: "en", SchemaSlug: "page", EntityID: "e1",
		Requests: []BuilderDataQuery{{ID: "r1", Source: "ext.demo.forecast", Limit: 3}},
		Results: []BuilderDataResult{{ID: "r1", Value: "v", Error: "x", Items: []BuilderDataItem{{
			Title: "T1", URL: "/u1", Excerpt: "X1", Image: "/i1.png", Category: "K1", Author: "A1", AuthorURL: "/a1", PublishedAt: "2026-01-02",
			Attrs: map[string]string{"k": "v"},
		}}}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out BuilderDataRequest
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip: %+v != %+v", out, in)
	}
	for _, key := range []string{`"schema_slug"`, `"entity_id"`, `"author_url"`, `"published_at"`, `"attrs"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("missing %s in %s", key, b)
		}
	}
}

// Validate does not tie a permission to a minimum sdk range: a plugin may ask
// for site.scripts while declaring an older range.
func TestValidateAcceptsSiteScriptsUnderOldRange(t *testing.T) {
	m := Manifest{Name: "hello-world", Version: "0.1.0", SDK: "^0.11", Permissions: []string{PermSiteScripts}}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
