package form

// fileKind says how a stored upload is checked beyond its sniffed type.
type fileKind int

const (
	kindBinary fileKind = iota // sniffed type must be one of mimes
	kindText                   // text/plain and no NUL byte
	kindOOXML                  // application/zip holding [Content_Types].xml
)

type fileType struct {
	mimes []string
	kind  fileKind
	// mime is the type stored and served for the extension.
	mime string
}

// fileTypes is the allow-list of extensions a file field can accept. It
// deliberately leaves out anything a browser would run or render inline
// (svg, html, xml, js, exe): an upload is only ever served as an attachment,
// but the table is the first line of defence.
var fileTypes = map[string]fileType{
	"pdf":  {mimes: []string{"application/pdf"}, kind: kindBinary, mime: "application/pdf"},
	"png":  {mimes: []string{"image/png"}, kind: kindBinary, mime: "image/png"},
	"jpg":  {mimes: []string{"image/jpeg"}, kind: kindBinary, mime: "image/jpeg"},
	"jpeg": {mimes: []string{"image/jpeg"}, kind: kindBinary, mime: "image/jpeg"},
	"gif":  {mimes: []string{"image/gif"}, kind: kindBinary, mime: "image/gif"},
	"webp": {mimes: []string{"image/webp"}, kind: kindBinary, mime: "image/webp"},
	"zip":  {mimes: []string{"application/zip"}, kind: kindBinary, mime: "application/zip"},
	"txt":  {kind: kindText, mime: "text/plain"},
	"csv":  {kind: kindText, mime: "text/csv"},
	"md":   {kind: kindText, mime: "text/markdown"},
	"json": {kind: kindText, mime: "application/json"},
	"docx": {kind: kindOOXML, mime: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	"xlsx": {kind: kindOOXML, mime: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
	"pptx": {kind: kindOOXML, mime: "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
}

// ValidAcceptExt reports whether ext (lowercase, no dot) may appear in a file
// field's accept list.
func ValidAcceptExt(ext string) bool {
	_, ok := fileTypes[ext]
	return ok
}

// AcceptExts lists the allowed extensions in a stable order, for the designer
// and for messages.
func AcceptExts() []string {
	return []string{"pdf", "png", "jpg", "jpeg", "gif", "webp", "txt", "csv", "md", "json", "docx", "xlsx", "pptx", "zip"}
}
