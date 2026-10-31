package admin

import (
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
)

// commonLocaleTags are the suggestions of the Add a locale field: languages on
// their own (they cover every region) and the usual language and region pairs.
// The field accepts any valid tag, so the list only saves typing.
const commonLocaleTags = "en en-GB en-US en-AU en-CA es es-ES es-MX fr fr-FR fr-CA de de-DE de-AT it it-IT pt pt-BR pt-PT nl nl-NL sv da nb fi pl cs hu ro el tr ru uk ar he hi id vi th ja ko zh zh-CN zh-TW"

// commonLocales is commonLocaleTags with their English names.
func commonLocales() []ui.Option {
	tags := strings.Fields(commonLocaleTags)
	out := make([]ui.Option, len(tags))
	for i, t := range tags {
		out[i] = ui.Option{Value: t, Label: localeDisplayName(t)}
	}
	return out
}
