package siteconfig

import (
	"encoding/json/jsontext"
	"fmt"

	"hatamoto.dev/core/internal/jsonx"
)

// protectEmailKey is the theme settings key that holds the email protection
// switch; every other key of the document is left as stored.
const protectEmailKey = "protectEmail"

// ParseProtectEmail reads the protectEmail key of the theme settings JSON.
// Absent or malformed data means on, the default.
func ParseProtectEmail(themeExtra []byte) bool {
	var top map[string]jsontext.Value
	if jsonx.Unmarshal(themeExtra, &top, jsonx.StoredLoose) != nil || len(top[protectEmailKey]) == 0 {
		return true
	}
	var on bool
	if jsonx.Unmarshal(top[protectEmailKey], &on, jsonx.StoredLoose) != nil {
		return true
	}
	return on
}

// MergeProtectEmail sets the protectEmail key and keeps every other key.
func MergeProtectEmail(themeExtra []byte, on bool) ([]byte, error) {
	top := map[string]jsontext.Value{}
	if len(themeExtra) > 0 && string(themeExtra) != "null" {
		if err := jsonx.Unmarshal(themeExtra, &top, jsonx.StoredLoose); err != nil {
			return nil, fmt.Errorf("theme settings: %w", err)
		}
	}
	raw, err := jsonx.Marshal(on)
	if err != nil {
		return nil, err
	}
	top[protectEmailKey] = raw
	return jsonx.Marshal(top)
}
