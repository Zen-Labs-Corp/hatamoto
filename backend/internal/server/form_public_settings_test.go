package server

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/form"
)

func TestPublicFormSettingsOmitsInternalFields(t *testing.T) {
	rep := "rep@example.test"
	got := publicFormSettings(form.FormSettings{
		SubmitButtonText:    "Go",
		SuccessMessage:      "Thanks",
		EnableCaptcha:       true,
		NotifyEmail:         &rep,
		ConfirmationEmail:   true,
		ConfirmationMessage: "internal body",
		StoreSubmissions:    true,
	})
	raw, _ := json.Marshal(got)
	for _, banned := range []string{"notify_email", "rep@example.test", "confirmation_message", "store_submissions", "confirmation_email"} {
		if strings.Contains(string(raw), banned) {
			t.Fatalf("public settings leak %q: %s", banned, raw)
		}
	}
	if got["submit_button_text"] != "Go" || got["success_message"] != "Thanks" || got["enable_captcha"] != true {
		t.Fatalf("public settings lost renderer fields: %s", raw)
	}
}
