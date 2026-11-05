package server

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"reflect"
	"sort"
	"strings"

	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/siteconfig"
)

// profileWrite is a validated business-profile part of a settings PATCH.
type profileWrite struct {
	// site is the new site-wide profile ("{}" clears); nil leaves it unchanged.
	site jsontext.Value
	// cards maps a locale tag to its new card; a nil value deletes the card.
	cards map[string]jsontext.Value
}

// sortedTags returns the card tags in write order.
func (w *profileWrite) sortedTags() []string {
	tags := make([]string, 0, len(w.cards))
	for tag := range w.cards {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

// decodeProfileStrict decodes one profile, rejecting unknown keys so a typo
// cannot be dropped silently. The returned path is the offending field when
// the JSON names one.
func decodeProfileStrict(raw jsontext.Value) (p bizprofile.Profile, path string, err error) {
	if err = jsonx.Unmarshal(raw, &p, jsonx.StrictNoUnknown); err != nil {
		if !jsonx.IsUnknownMember(err) {
			path = dottedPath(jsonx.ErrorPointer(err))
		}
		return bizprofile.Profile{}, path, err
	}
	return p, "", nil
}

// dottedPath turns a JSON Pointer (/a/b/0) into the dotted field path a.b.0.
func dottedPath(ptr string) string {
	parts := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	return strings.Join(parts, ".")
}

// addProfileErrors validates one decoded profile with validate and records its
// failures under prefix.
func addProfileErrors(fields map[string]string, prefix string, raw jsontext.Value, validate func(bizprofile.Profile) map[string]string) (bizprofile.Profile, bool) {
	p, path, err := decodeProfileStrict(raw)
	if err != nil {
		key := prefix
		if path != "" {
			key += "." + path
		}
		fields[key] = "Not a valid business profile: " + err.Error()
		return p, false
	}
	errs := validate(p)
	for path, msg := range errs {
		fields[prefix+"."+path] = msg
	}
	return p, len(errs) == 0
}

// defaultLocaleRule is the 422 message for a write to the default locale's tag:
// that locale's business is business_profile.
const defaultLocaleRule = "The default locale's business is business_profile; write it there."

// prepareProfiles validates the profile fields of a PATCH against the locale
// list the site will have afterwards. def is the default locale the site will
// have: its business is business_profile (validated as the default business),
// so locale_profiles takes no card for it, except null to delete a stored
// leftover. A stored card of a locale that is no longer listed can still be
// deleted with null. It returns the first-level problem as fields (422) or,
// for a null business_profile, a message (400).
func prepareProfiles(in *siteSettingsPatch, def string, locales []siteconfig.LocaleEntry, stored map[string]bool) (*profileWrite, map[string]string, string) {
	w := &profileWrite{}
	fields := map[string]string{}
	if in.BusinessProfile != nil {
		if string(bytes.TrimSpace(in.BusinessProfile)) == "null" {
			return nil, nil, "business_profile cannot be null; send {} to clear"
		}
		if p, ok := addProfileErrors(fields, "business_profile", in.BusinessProfile, bizprofile.ValidateDefault); ok {
			w.site = profileJSON(p)
		}
	}
	def = normalisedTag(def)
	for rawTag, raw := range in.LocaleProfiles {
		prefix := "locale_profiles." + rawTag
		tag, err := entity.NormalizeLocale(rawTag)
		isNull := string(bytes.TrimSpace(raw)) == "null"
		if err == nil && tag == def && !(isNull && stored[tag]) {
			fields["locale_profiles."+tag] = defaultLocaleRule
			continue
		}
		if err != nil || !(hasLocale(locales, tag) || (isNull && stored[tag])) {
			fields[prefix] = "That locale is not on your site."
			continue
		}
		prefix = "locale_profiles." + tag
		if isNull {
			addCard(w, tag, nil)
			continue
		}
		p, ok := addProfileErrors(fields, prefix, raw, bizprofile.Validate)
		if !ok {
			continue
		}
		var card jsontext.Value
		if !reflect.DeepEqual(p, bizprofile.Profile{}) {
			card = profileJSON(p)
		}
		addCard(w, tag, card)
	}
	if len(fields) > 0 {
		return nil, fields, ""
	}
	return w, nil, ""
}

func addCard(w *profileWrite, tag string, card jsontext.Value) {
	if w.cards == nil {
		w.cards = map[string]jsontext.Value{}
	}
	w.cards[tag] = card
}

// normalisedTag is tag in the form entity.NormalizeLocale returns, or tag
// itself when it is not a valid tag.
func normalisedTag(tag string) string {
	if n, err := entity.NormalizeLocale(tag); err == nil {
		return n
	}
	return tag
}

// hasLocale reports whether tag is on a multi-locale site's list.
func hasLocale(locales []siteconfig.LocaleEntry, tag string) bool {
	if len(locales) < 2 {
		return false
	}
	for _, e := range locales {
		if e.Tag == tag {
			return true
		}
	}
	return false
}

// profilesFrom decodes the stored profile and cards for the response; a stored
// value that does not parse is logged by the caller and answered as empty.
func profilesFrom(cfg *siteconfig.Config) (bizprofile.Profile, map[string]bizprofile.Profile, error) {
	site, siteErr := bizprofile.Parse(cfg.BusinessProfile)
	cards, cardErr := bizprofile.ParseLocales(cfg.LocaleProfiles)
	if cards == nil || cardErr != nil {
		cards = map[string]bizprofile.Profile{}
	}
	return site, cards, errors.Join(siteErr, cardErr)
}

// profileJSON marshals a profile; an empty one is "{}" (the nested address and
// rating structs would otherwise print as empty objects).
func profileJSON(p bizprofile.Profile) jsontext.Value {
	if reflect.DeepEqual(p, bizprofile.Profile{}) {
		return jsontext.Value("{}")
	}
	raw, err := jsonx.Marshal(p)
	if err != nil {
		return jsontext.Value("{}")
	}
	return raw
}
