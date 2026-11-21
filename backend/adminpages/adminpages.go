package adminpages

import (
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/adminpagestate"
)

// Page is one owner-only Settings page.
//
//   - ID is lowercase letters, digits and dashes (^[a-z][a-z0-9-]{0,31}$) and
//     becomes the URL <admin>/settings/modules/<ID>.
//   - Label and Desc name the page in the Settings navigation.
//   - Icon is a core icon name; empty or unknown falls back to "puzzle".
//   - Templates holds pages/<name>.html files.
//   - Routes needs a GET route with an empty Path (the page itself).
type Page = adminpagestate.Page

// Route is one route of a page. Method is GET or POST; Path is empty or starts
// with "/" and is relative to the page URL. A Sudo route needs a recently
// confirmed password, like core's own sensitive actions.
type Route = adminpagestate.Route

// HandlerFunc handles one route. A returned error or a panic renders core's
// error page with status 500.
type HandlerFunc = adminpagestate.HandlerFunc

// Context is passed to a HandlerFunc. Only core implements it, so methods may
// be added in a minor release.
type Context = adminpagestate.Context

// User is the signed-in admin user.
type User = adminpagestate.User

// Banner is one owner banner. Kind is "danger", "warning" or "info"; Href is
// relative to the admin prefix and starts with "/".
type Banner = adminpagestate.Banner

// BannerFunc returns the banners to show now. Core gives it a short deadline.
type BannerFunc = adminpagestate.BannerFunc

// DangerConfirm describes a danger-confirm card; see Context.CheckDanger.
type DangerConfirm = ui.DangerConfirm

// DangerText is the sentence of a danger-confirm card.
type DangerText = ui.DangerText

// DangerPart is one part of a DangerText.
type DangerPart = ui.DangerPart

// ConfirmHidden is a hidden field carried through a danger-confirm form.
type ConfirmHidden = ui.ConfirmHidden

// Register adds an owner-only Settings page. Call it from an init function.
// It panics after app.Main started, on an invalid or duplicate ID, an empty
// label, nil templates, a missing GET route with an empty path, a nil handler,
// a method other than GET or POST, or an invalid route path.
func Register(p Page) { adminpagestate.Register(p) }

// RegisterBanner adds an owner banner provider. Call it from an init function.
// It panics after app.Main started, on an invalid or duplicate name, or with a
// nil func.
func RegisterBanner(name string, fn BannerFunc) { adminpagestate.RegisterBanner(name, fn) }
