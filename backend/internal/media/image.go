package media

import (
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/draw"
)

// resizeImage scales an image to the specified dimensions.
// If crop is true, the image is cropped to a centered square first.
func resizeImage(src image.Image, width, height int, crop bool) image.Image {
	srcBounds := src.Bounds()
	srcWidth := srcBounds.Dx()
	srcHeight := srcBounds.Dy()

	var croppedSrc image.Image = src

	if crop {
		// Crop to center square
		var cropRect image.Rectangle
		if srcWidth > srcHeight {
			offset := (srcWidth - srcHeight) / 2
			cropRect = image.Rect(offset, 0, offset+srcHeight, srcHeight)
		} else {
			offset := (srcHeight - srcWidth) / 2
			cropRect = image.Rect(0, offset, srcWidth, offset+srcWidth)
		}

		// Create cropped image
		cropped := image.NewRGBA(image.Rect(0, 0, cropRect.Dx(), cropRect.Dy()))
		draw.Copy(cropped, image.Point{}, src, cropRect, draw.Src, nil)
		croppedSrc = cropped
	}

	// Create destination image
	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	// Scale using CatmullRom for high quality
	draw.CatmullRom.Scale(dst, dst.Bounds(), croppedSrc, croppedSrc.Bounds(), draw.Over, nil)

	return dst
}

// encodeImage writes img to w in the format for mimeType.
func encodeImage(w io.Writer, img image.Image, mimeType string) error {
	switch mimeType {
	case "image/jpeg":
		return jpeg.Encode(w, img, &jpeg.Options{Quality: 85})
	case "image/png":
		return png.Encode(w, img)
	case "image/webp":
		// WebP encoding requires cgo, so resized WebP copies are JPEG; the
		// caller names them by VariantExt, which says .jpg.
		return jpeg.Encode(w, img, &jpeg.Options{Quality: 85})
	default:
		return fmt.Errorf("unsupported image format: %s", mimeType)
	}
}
