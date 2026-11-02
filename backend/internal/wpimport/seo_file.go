package wpimport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// MaxSEOFileBytes caps an uploaded SEO plugin settings file.
const MaxSEOFileBytes = 1 << 20

// ErrSEOFileFormat means the file is not a supported plugin export.
var ErrSEOFileFormat = errors.New("unsupported settings file")

var errSEOFormat = fmt.Errorf("%w: expected a Rank Math settings .json, a Yoast export or a TSF settings .json", ErrSEOFileFormat)

// ParseSEOFile detects and parses a plugin export (data only, never executed).
// Values is a flat map of the plugin's own option names (all sections merged).
// The raw bytes are not kept.
func ParseSEOFile(r io.Reader) (*SEOFile, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxSEOFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read settings file: %w", err)
	}
	if len(data) > MaxSEOFileBytes {
		return nil, errors.New("settings file is larger than 1 MiB")
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return parseSEOJSON(trimmed)
	}
	return parseYoastINI(trimmed)
}

func parseSEOJSON(data []byte) (*SEOFile, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, errSEOFormat
	}
	if wrapped, ok := root["autodescription-site-settings"].(map[string]any); ok {
		return &SEOFile{Plugin: "tsf", Values: flattenSEO(wrapped)}, nil
	}
	_, hasModules := root["modules"]
	settings, hasSettings := root["settings"].(map[string]any)
	if hasModules || hasSettings {
		vals := map[string]any{}
		// Rank Math exports keep the sections at the top level next to
		// "modules"; some tools nest them under "settings".
		for _, holder := range []map[string]any{settings, root} {
			for _, sec := range []string{"general", "titles", "sitemap"} {
				if m, ok := holder[sec].(map[string]any); ok {
					for k, v := range flattenSEO(m) {
						vals[k] = v
					}
				}
			}
		}
		if len(vals) == 0 {
			return nil, errSEOFormat
		}
		return &SEOFile{Plugin: "rankmath", Values: vals}, nil
	}
	hasPT := false
	for k := range root {
		if strings.HasPrefix(k, "pt_") {
			hasPT = true
		}
	}
	_, sep := root["title_separator"]
	if hasPT {
		return &SEOFile{Plugin: "rankmath", Values: flattenSEO(root)}, nil
	}
	_, site := root["site_title"]
	_, home := root["homepage_title"]
	if sep && (site || home) && !hasPT {
		return &SEOFile{Plugin: "tsf", Values: flattenSEO(root)}, nil
	}
	return nil, errSEOFormat
}

// flattenSEO keeps scalars, turns scalar arrays into []string and leaves
// nested objects as map[string]any.
func flattenSEO(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case []any:
			ss := make([]string, 0, len(t))
			scalar := true
			for _, e := range t {
				switch s := e.(type) {
				case string:
					ss = append(ss, s)
				case json.Number:
					ss = append(ss, s.String())
				case bool:
					ss = append(ss, fmt.Sprint(s))
				default:
					scalar = false
				}
			}
			if scalar {
				out[k] = ss
			} else {
				out[k] = t
			}
		default:
			out[k] = v
		}
	}
	return out
}

var yoastSections = map[string]bool{"wpseo": true, "wpseo_titles": true, "wpseo_social": true}

// parseYoastINI hand-parses the Yoast "Export settings" block.
func parseYoastINI(data []byte) (*SEOFile, error) {
	vals := map[string]any{}
	found, keep := false, false
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), MaxSEOFileBytes)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := line[1 : len(line)-1]
			keep = yoastSections[name]
			found = found || strings.HasPrefix(name, "wpseo")
			continue
		}
		if !keep {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		var val any = v
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			val = v[1 : len(v)-1]
		} else if v == "true" || v == "false" {
			val = v == "true"
		}
		if base, isArr := strings.CutSuffix(k, "[]"); isArr {
			s, _ := vals[base].([]string)
			vals[base] = append(s, fmt.Sprint(val))
			continue
		}
		vals[k] = val
	}
	if sc.Err() != nil || !found {
		return nil, errSEOFormat
	}
	return &SEOFile{Plugin: "yoast", Values: vals}, nil
}
