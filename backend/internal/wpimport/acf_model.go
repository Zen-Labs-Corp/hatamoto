package wpimport

// Id-map kinds of the ACF and Contact Form 7 import. They are free text in
// hatamoto_import_map, so they need no migration.
const (
	KindACFField = "acf_field"
	KindForm     = "form"
)

// ACFGroup is an Advanced Custom Fields field group of the source site.
type ACFGroup struct {
	ID    string
	Key   string
	Title string
	// Types are the post types the group is shown on (a page template rule
	// counts as "page").
	Types []string
	// OtherLocations are rule parameters the import does not map, such as
	// options pages, taxonomies or users.
	OtherLocations []string
	Fields         []ACFField
	// Guessed is true when the group was inferred from meta values because the
	// export carried no field group definitions.
	Guessed bool
}

// ACFField is one field of a group, a sub field of a repeater or group, or a
// sub field of a flexible content layout.
type ACFField struct {
	ID    string
	Key   string // field_<13 hex>
	Name  string // meta key
	Label string
	Type  string
	Order int
	// Choices are the options of select, radio, checkbox and button group.
	Choices  []ACFChoice
	Multiple bool
	// PostTypes limits relationship and post object fields.
	PostTypes []string
	Sub       []ACFField
	Layouts   []ACFLayout
}

// ACFChoice is one option of a choice field.
type ACFChoice struct{ Value, Label string }

// ACFLayout is one layout of a flexible content field.
type ACFLayout struct {
	Key, Name, Label string
	Sub              []ACFField
}

// CF7Form is a Contact Form 7 form of the source site.
type CF7Form struct {
	ID       string
	Hash     string // first 7 characters of the CF7 hash, as shortcodes use it
	Slug     string
	Title    string
	Template string // the form markup with [tags]
	HasMail  bool   // mail settings were present (never imported)
}

// ACFResult is what the ACF schema and value steps did.
type ACFResult struct {
	Groups             int              `json:"groups"`
	FieldsAdded        int              `json:"fields_added,omitzero"`
	FieldsExisting     int              `json:"fields_existing,omitzero"`
	RelationsChanged   int              `json:"relations_changed,omitzero"`
	RelationsUnchanged int              `json:"relations_unchanged,omitzero"`
	TypesGuessed       bool             `json:"types_guessed,omitzero"`
	Fields             []ACFFieldResult `json:"fields,omitempty"`
}

// ACFFieldResult is the report line of one top-level ACF field.
type ACFFieldResult struct {
	Group  string `json:"group"`
	Label  string `json:"label"`
	Type   string `json:"type"`
	Target string `json:"target,omitempty"` // schema slug
	Status string `json:"status"`           // mapped, partly or skipped
	Note   string `json:"note,omitempty"`
	Items  int    `json:"items,omitzero"`
	Rows   int    `json:"rows,omitzero"`
	Links  int    `json:"links,omitzero"`
}

// FormsResult is what the forms step did.
type FormsResult struct {
	Found           int          `json:"found"`
	Created         int          `json:"created,omitzero"`
	AlreadyImported int          `json:"already_imported,omitzero"`
	MailNotImported bool         `json:"mail_not_imported,omitzero"`
	Forms           []FormResult `json:"forms,omitempty"`
}

// FormResult is the report line of one Contact Form 7 form.
type FormResult struct {
	Title   string   `json:"title"`
	Slug    string   `json:"slug,omitempty"`
	Status  string   `json:"status"` // imported, partly, skipped or already
	Note    string   `json:"note,omitempty"`
	Fields  int      `json:"fields,omitzero"`
	Dropped []string `json:"dropped,omitempty"`
}
