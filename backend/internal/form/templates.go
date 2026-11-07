package form

import "github.com/google/uuid"

// Template is a starting point for a new form: its fields and settings, with
// neutral copy that any site can keep or edit.
type Template struct {
	Key, Name, Blurb string
	Build            func() CreateFormInput
}

// Templates lists the form templates in display order.
func Templates() []Template { return templates }

// TemplateByKey returns the template with the given key.
func TemplateByKey(key string) (Template, bool) {
	for _, t := range templates {
		if t.Key == key {
			return t, true
		}
	}
	return Template{}, false
}

func emailField() FormField {
	return FormField{Type: FieldTypeEmail, Label: "Email", Name: "email", Required: true, NameLocked: true}
}

func settings(submit, success, subject string) FormSettings {
	return FormSettings{
		SubmitButtonText: submit,
		SuccessMessage:   success,
		NotifySubject:    subject,
		NotifyReplyTo:    "email",
		StoreSubmissions: true,
	}
}

var templates = []Template{
	{
		Key: "blank", Name: "Blank", Blurb: "Start empty and add your own fields.",
		Build: func() CreateFormInput {
			return CreateFormInput{Fields: []FormField{}, Settings: FormSettings{StoreSubmissions: true}}
		},
	},
	{
		Key: "contact", Name: "Contact", Blurb: "Name, email and a message.",
		Build: func() CreateFormInput {
			return CreateFormInput{
				Fields: []FormField{
					{Type: FieldTypeText, Label: "Name", Name: "name", Required: true, Width: "half"},
					{Type: FieldTypeEmail, Label: "Email", Name: "email", Required: true, NameLocked: true, Width: "half"},
					{Type: FieldTypeTextarea, Label: "Message", Name: "message", Required: true},
				},
				Settings: settings("Send message", "Thank you, your message has been sent.", "New message from {{name}}"),
			}
		},
	},
	{
		Key: "quote", Name: "Quote request", Blurb: "Contact details and what the visitor needs.",
		Build: func() CreateFormInput {
			return CreateFormInput{
				Fields: []FormField{
					{Type: FieldTypeText, Label: "Name", Name: "name", Required: true, Width: "half"},
					{Type: FieldTypeEmail, Label: "Email", Name: "email", Required: true, NameLocked: true, Width: "half"},
					{Type: FieldTypePhone, Label: "Phone", Name: "phone", Width: "half"},
					{Type: FieldTypeText, Label: "Company", Name: "company", Width: "half"},
					{Type: FieldTypeSelect, Label: "Service", Name: "service", Options: []FieldOption{
						{Label: "Consultation", Value: "consultation"},
						{Label: "Project work", Value: "project"},
						{Label: "Ongoing support", Value: "support"},
						{Label: "Something else", Value: "other"},
					}},
					{Type: FieldTypeSelect, Label: "Budget", Name: "budget", Options: []FieldOption{
						{Label: "Under 1,000", Value: "under-1000"},
						{Label: "1,000 to 5,000", Value: "1000-5000"},
						{Label: "5,000 to 20,000", Value: "5000-20000"},
						{Label: "Over 20,000", Value: "over-20000"},
					}},
					{Type: FieldTypeTextarea, Label: "Project details", Name: "details", Required: true},
				},
				Settings: settings("Get my quote", "Thank you, we will send your quote soon.", "Quote request from {{name}}"),
			}
		},
	},
	{
		Key: "newsletter", Name: "Newsletter signup", Blurb: "Email address and consent.",
		Build: func() CreateFormInput {
			return CreateFormInput{
				Fields: []FormField{
					{Type: FieldTypeText, Label: "First name", Name: "first_name"},
					emailField(),
					{Type: FieldTypeConsent, Label: "I agree to receive emails", Name: "consent", Required: true, NameLocked: true},
				},
				Settings: settings("Subscribe", "Thank you for subscribing.", "New subscriber"),
			}
		},
	},
	{
		Key: "rsvp", Name: "Event RSVP", Blurb: "Attendance, guests and dietary needs.",
		Build: func() CreateFormInput {
			attending := uuid.NewString()
			return CreateFormInput{
				Fields: []FormField{
					{Type: FieldTypeText, Label: "Name", Name: "name", Required: true, Width: "half"},
					{Type: FieldTypeEmail, Label: "Email", Name: "email", Required: true, NameLocked: true, Width: "half"},
					{ID: attending, Type: FieldTypeRadio, Label: "Will you attend?", Name: "attending", Required: true, Options: []FieldOption{
						{Label: "Yes", Value: "yes"},
						{Label: "No", Value: "no"},
					}},
					{Type: FieldTypeNumber, Label: "Number of guests", Name: "guests", Width: "half",
						Logic: &FieldLogic{FieldID: attending, Op: "is", Value: "yes"}},
					{Type: FieldTypeText, Label: "Dietary requirements", Name: "dietary", Width: "half",
						Logic: &FieldLogic{FieldID: attending, Op: "is", Value: "yes"}},
					{Type: FieldTypeTextarea, Label: "Notes", Name: "notes"},
				},
				Settings: settings("Send RSVP", "Thank you, your reply has been recorded.", "RSVP from {{name}}"),
			}
		},
	},
	{
		Key: "job", Name: "Job application", Blurb: "Applicant details and a short pitch.",
		Build: func() CreateFormInput {
			return CreateFormInput{
				Fields: []FormField{
					{Type: FieldTypeText, Label: "Full name", Name: "name", Required: true, Width: "half"},
					{Type: FieldTypeEmail, Label: "Email", Name: "email", Required: true, NameLocked: true, Width: "half"},
					{Type: FieldTypePhone, Label: "Phone", Name: "phone", Width: "half"},
					{Type: FieldTypeText, Label: "Position", Name: "position", Required: true, Width: "half"},
					{Type: FieldTypeURL, Label: "Portfolio or profile link", Name: "link"},
					{Type: FieldTypeTextarea, Label: "Why are you a good fit?", Name: "cover_letter", Required: true},
				},
				Settings: settings("Apply", "Thank you, we have received your application.", "Application from {{name}}"),
			}
		},
	},
}
