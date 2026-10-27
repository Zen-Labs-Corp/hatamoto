package admin

import (
	"fmt"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/starter"
	"hatamoto.dev/core/internal/siteconfig"
)

func uuidOf(s string) (uuid.UUID, error) { return uuid.Parse(s) }

func siteUpdateColour(c string) siteconfig.UpdateInput {
	return siteconfig.UpdateInput{PrimaryColor: &c}
}

func siteUpdateDone() siteconfig.UpdateInput { return siteconfig.UpdateInput{SetupCompleted: true} }

func starterSummary(r starter.Result) string {
	if r.Total() == 0 {
		return "The starter content was already there. Nothing was added."
	}
	return fmt.Sprintf("Starter content installed: %d pages, %d posts, %d menus and a contact form.", r.Pages, r.Posts, r.Menus)
}
