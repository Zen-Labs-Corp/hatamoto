package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"sort"

	"hatamoto.dev/core/internal/bizprofile"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

// profileWrite is a validated business-profile part of a settings PATCH.
type profileWrite struct {
	// site is the new site-wide profile ("{}" clears); nil leaves it unchanged.
	site json.RawMessage
	// cards maps a locale tag to its new card; a nil value deletes the card.
	cards map[string]json.RawMessage
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
func decodeProfileStrict(raw json.RawMessage) (p bizprofile.Profile, path string, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&p); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			path = typeErr.Field
		}
		return bizprofile.Profile{}, path, err
	}
	return p, "", nil
}

// addProfileErrors validates one decoded profile and records its failures under prefix.
func addProfileErrors(fields map[string]string, prefix string, raw json.RawMessage) (bizprofile.Profile, bool) {
	p, path, err := decodeProfileStrict(raw)
	if err != nil {
		key := prefix
		if path != "" {
			key += "." + path
		}
		fields[key] = "Not a valid business profile: " + err.Error()
		return p, false
	}
	errs := bizprofile.Validate(p)
	for path, msg := range errs {
		fields[prefix+"."+path] = msg
	}
	return p, len(errs) == 0
}

// prepareProfiles validates the profile fields of a PATCH against the locale
// list the site will have afterwards. It returns the first-level problem as
// fields (422) or, for a null site-wide profile, a message (400).
func prepareProfiles(in *siteSettingsPatch, locales []siteconfig.LocaleEntry) (*profileWrite, map[string]string, string) {
	w := &profileWrite{}
	fields := map[string]string{}
	if in.BusinessProfile != nil {
		if string(bytes.TrimSpace(in.BusinessProfile)) == "null" {
			return nil, nil, "business_profile cannot be null; send {} to clear"
		}
		if p, ok := addProfileErrors(fields, "business_profile", in.BusinessProfile); ok {
			w.site = profileJSON(p)
		}
	}
	for rawTag, raw := range in.LocaleProfiles {
		prefix := "locale_profiles." + rawTag
		tag, err := entity.NormalizeLocale(rawTag)
		if err != nil || !hasLocale(locales, tag) {
			fields[prefix] = "That locale is not on your site."
			continue
		}
		prefix = "locale_profiles." + tag
		if string(bytes.TrimSpace(raw)) == "null" {
			addCard(w, tag, nil)
			continue
		}
		p, ok := addProfileErrors(fields, prefix, raw)
		if !ok {
			continue
		}
		var card json.RawMessage
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

func addCard(w *profileWrite, tag string, card json.RawMessage) {
	if w.cards == nil {
		w.cards = map[string]json.RawMessage{}
	}
	w.cards[tag] = card
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
func profileJSON(p bizprofile.Profile) json.RawMessage {
	if reflect.DeepEqual(p, bizprofile.Profile{}) {
		return json.RawMessage("{}")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return json.RawMessage("{}")
	}
	return raw
}
