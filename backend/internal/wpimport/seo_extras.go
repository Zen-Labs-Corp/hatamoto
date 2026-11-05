package wpimport

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/siteconfig"
)

// Options of the settings files that hold the organization's name and logo.
var seoKGOptions = map[string][2]string{
	"rankmath": {"knowledgegraph_name", "knowledgegraph_logo"},
	"yoast":    {"company_name", "company_logo"},
	"tsf":      {"knowledge_name", "knowledge_logo_url"},
}

var (
	rankMathSitemapRE = regexp.MustCompile(`^pt_(.+)_sitemap$`)
	yoastSitemapRE    = regexp.MustCompile(`^post_types-(.+)-not_in_sitemap$`)
)

// seoExtras handles what lives outside the SEO settings document: the
// per-type sitemap exclusion (a schema setting) and the organization details
// (site configuration). On a site with content it only lists rows.
func (r *Runner) seoExtras(ctx context.Context, file *SEOFile, live *seoLive, res *SEOResult) {
	existing := r.state.Mode == ModeExisting
	r.seoSitemapExcludes(ctx, file, existing, res)
	r.seoOrganization(ctx, file, live, existing, res)
}

// sitemapExcludedTypes lists the WordPress post types the file keeps out of the sitemap.
func sitemapExcludedTypes(file *SEOFile) []string {
	if file == nil {
		return nil
	}
	var re *regexp.Regexp
	switch file.Plugin {
	case "rankmath":
		re = rankMathSitemapRE
	case "yoast":
		re = yoastSitemapRE
	default:
		return nil
	}
	var out []string
	for k, v := range file.Values {
		m := re.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		if (file.Plugin == "rankmath" && strings.EqualFold(valStr(v), "off")) || (file.Plugin == "yoast" && truthy(v)) {
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// schemaSitemap reads the sitemap keys of a schema's settings.
func schemaSitemap(settings jsontext.Value) (exclude bool, priority *float64, changeFreq string) {
	var s struct {
		Sitemap struct {
			Exclude    bool     `json:"exclude"`
			Priority   *float64 `json:"priority"`
			ChangeFreq string   `json:"changefreq"`
		} `json:"sitemap"`
	}
	if jsonx.Unmarshal(settings, &s, jsonx.StoredLoose) != nil {
		return false, nil, ""
	}
	return s.Sitemap.Exclude, s.Sitemap.Priority, s.Sitemap.ChangeFreq
}

func (r *Runner) seoSitemapExcludes(ctx context.Context, file *SEOFile, existing bool, res *SEOResult) {
	done := map[string]bool{}
	for _, wpType := range sitemapExcludedTypes(file) {
		slug, ok := r.slugOf(wpType)
		if !ok || done[slug] {
			continue
		}
		done[slug] = true
		sc, err := r.schema(ctx, slug)
		if err != nil {
			r.warn(fmt.Sprintf("sitemap setting of %q not imported: %v", slug, err))
			continue
		}
		excluded, prio, freq := schemaSitemap(sc.Settings)
		if excluded {
			continue
		}
		key, label := "sitemap.type."+slug, "Keep "+slug+" out of the sitemap"
		if existing {
			res.Would = append(res.Would, SEORow{Key: key, Label: label, Current: "No", Value: "Yes"})
			continue
		}
		patch := entity.SchemaSEOPatch{Sitemap: &entity.SchemaSitemapPatch{Exclude: true, Priority: prio, ChangeFreq: freq}}
		err = r.d.DB.WithTx(ctx, func(tx pgx.Tx) error { return r.d.Entities.PatchSchemaSEOTx(ctx, tx, sc.ID, patch) })
		var se *entity.SettingsError
		switch {
		case errors.As(err, &se):
			res.NotMapped = append(res.NotMapped, label+": "+se.Msg)
		case err != nil:
			r.warn(fmt.Sprintf("sitemap setting of %q not imported: %v", slug, err))
		default:
			r.d.Entities.SchemaChanged()
			delete(r.schemas, slug)
			res.Applied = append(res.Applied, SEORow{Key: key, Label: label, Value: "Yes"})
		}
	}
}

// seoOrganization maps the organization name, logo and profile links. Name and
// logo are only written where the site has none yet; links are added to the
// ones already there.
func (r *Runner) seoOrganization(ctx context.Context, file *SEOFile, live *seoLive, existing bool, res *SEOResult) {
	var name, logo string
	var sameAs []string
	if live != nil {
		name, logo, sameAs = strings.TrimSpace(live.OrgName), strings.TrimSpace(live.OrgLogo), live.SameAs
	}
	if file != nil {
		if keys, ok := seoKGOptions[file.Plugin]; ok {
			if v := valStr(file.Values[keys[0]]); v != "" {
				name = v
			}
			if v := valStr(file.Values[keys[1]]); v != "" {
				logo = v
			}
		}
	}
	if !isWebURL(logo) {
		logo = ""
	} else if r.isSourceUpload(logo) {
		res.NotMapped = append(res.NotMapped, "Organization logo: upload it in the site settings")
		logo = ""
	}
	if name == "" && logo == "" && len(sameAs) == 0 {
		return
	}
	cur, err := r.d.Site.Get(ctx)
	if err != nil {
		r.warn("organization details not imported: " + err.Error())
		return
	}

	var in siteconfig.UpdateInput
	var rows []SEORow
	wouldRows := func(key, label, current, value string) {
		if existing && value != "" && value != current {
			res.Would = append(res.Would, SEORow{Key: key, Label: label, Current: current, Value: value})
		}
	}
	if name != "" {
		wouldRows("kg.name", "Organization name", cur.CompanyName, name)
		// The settings phase already copied the WordPress title into the name:
		// that is a placeholder the organization name may replace.
		if !existing && (cur.CompanyName == "" || cur.CompanyName == strings.TrimSpace(r.dump.Site.Title)) && name != cur.CompanyName {
			in.CompanyName = &name
			rows = append(rows, SEORow{Key: "kg.name", Label: "Organization name", Value: name})
		}
	}
	if logo != "" {
		wouldRows("kg.logo", "Organization logo", cur.LogoURL, logo)
		if !existing && cur.LogoURL == "" {
			in.LogoURL = &logo
			rows = append(rows, SEORow{Key: "kg.logo", Label: "Organization logo", Value: logo})
		}
	}
	if len(sameAs) > 0 {
		merged, before, added, err := mergeSameAs(cur.BusinessProfile, sameAs)
		switch {
		case err != nil:
			res.NotMapped = append(res.NotMapped, "Organization profile links: "+err.Error())
		case len(added) > 0 && existing:
			res.Would = append(res.Would, SEORow{Key: "kg.same_as", Label: "Organization profile links", Current: strings.Join(before, ", "), Value: strings.Join(append(append([]string(nil), before...), added...), ", ")})
		case len(added) > 0:
			in.BusinessProfile = merged
			rows = append(rows, SEORow{Key: "kg.same_as", Label: "Organization profile links", Value: strings.Join(append(append([]string(nil), before...), added...), ", ")})
		}
	}
	if len(rows) == 0 {
		return
	}
	if _, err := r.d.Site.Update(ctx, in); err != nil {
		res.NotMapped = append(res.NotMapped, "Organization details: "+err.Error())
		return
	}
	res.Applied = append(res.Applied, rows...)
}

func isWebURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// mergeSameAs adds the https links in add to the profile's "sameas" list and
// returns the new profile JSON, the list before and the links that were new.
// Every other key of the profile is kept as it is.
func mergeSameAs(profile jsontext.Value, add []string) (merged jsontext.Value, before, added []string, err error) {
	obj := map[string]jsontext.Value{}
	if t := strings.TrimSpace(string(profile)); t != "" && t != "null" {
		if err := jsonx.Unmarshal(profile, &obj, jsonx.StoredLoose); err != nil {
			return nil, nil, nil, errors.New("the stored business profile is not readable")
		}
	}
	if raw, ok := obj["sameas"]; ok {
		_ = jsonx.Unmarshal(raw, &before, jsonx.StoredLoose)
	}
	have := map[string]bool{}
	for _, s := range before {
		have[strings.TrimRight(s, "/")] = true
	}
	for _, s := range add {
		s = strings.TrimSpace(s)
		if u, perr := url.Parse(s); perr != nil || u.Scheme != "https" || u.Host == "" || have[strings.TrimRight(s, "/")] {
			continue
		}
		have[strings.TrimRight(s, "/")] = true
		added = append(added, s)
	}
	if len(added) == 0 {
		return profile, before, nil, nil
	}
	all, _ := jsonx.Marshal(append(append([]string(nil), before...), added...))
	obj["sameas"] = all
	merged, err = jsonx.Marshal(obj)
	return merged, before, added, err
}
