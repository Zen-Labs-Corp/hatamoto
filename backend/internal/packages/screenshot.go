package packages

import (
	"bytes"
	"image"
	"path"
	"strings"

	_ "image/jpeg" // register decoders for DecodeConfig
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// Screenshot limits for the optional theme.json "screenshot" key.
const (
	MaxScreenshotBytes = 2 << 20
	MinShotW, MaxShotW = 640, 2560
	MinShotH, MaxShotH = 360, 1600
	MaxShotPixels      = 4_200_000
)

// shotExt maps an allowed file extension to its image format name. SVG is never allowed.
var shotExt = map[string]string{".png": "png", ".webp": "webp", ".jpg": "jpeg", ".jpeg": "jpeg"}

// ValidateScreenshot checks that rel names an allowed raster image under
// assets/ in files, within the size and dimension limits. Only the image
// header is read.
func ValidateScreenshot(rel string, files map[string][]byte) error {
	if err := checkName(rel, DefaultLimits); err != nil {
		return refuse("screenshot: %v", err)
	}
	if !strings.HasPrefix(rel, "assets/") {
		return refuse("screenshot %q must be under assets/", rel)
	}
	want, ok := shotExt[strings.ToLower(path.Ext(rel))]
	if !ok {
		return refuse("screenshot %q must be .png, .webp, .jpg or .jpeg", rel)
	}
	b, ok := files[rel]
	if !ok {
		return refuse("screenshot %q is not in the package", rel)
	}
	if len(b) > MaxScreenshotBytes {
		return refuse("screenshot is larger than %d bytes", MaxScreenshotBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return refuse("screenshot is not a readable image: %v", err)
	}
	if format != want {
		return refuse("screenshot is %s but named %s", format, path.Ext(rel))
	}
	if cfg.Width < MinShotW || cfg.Width > MaxShotW || cfg.Height < MinShotH || cfg.Height > MaxShotH ||
		cfg.Width*cfg.Height > MaxShotPixels {
		return refuse("screenshot is %dx%d, want %d-%d x %d-%d and at most %d pixels",
			cfg.Width, cfg.Height, MinShotW, MaxShotW, MinShotH, MaxShotH, MaxShotPixels)
	}
	return nil
}

// ScreenshotExt returns the normalised extension (".png", ".webp" or ".jpg") of rel.
func ScreenshotExt(rel string) string {
	switch shotExt[strings.ToLower(path.Ext(rel))] {
	case "png":
		return ".png"
	case "webp":
		return ".webp"
	case "jpeg":
		return ".jpg"
	}
	return ""
}
