package admin

import (
	"net/url"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/email"
	"hatamoto.dev/core/internal/mailsettings"
)

// emailFormEcho is what an Email POST carried, kept to redisplay after a
// failed save. Values are echoed as typed; the password never is.
type emailFormEcho struct {
	Posted      bool
	Provider    string
	SESRegion   string
	Host        string
	Port        string
	Security    string
	Username    string
	FromAddress string
	FromName    string
	// KeepPassword: the password field was blank and the saved password stays
	// (host and username unchanged). Pass it on to Check and Save.
	KeepPassword bool
}

// validateEmail reads the SMTP form shared by Settings > Email and the setup
// wizard. The password is write-only: blank keeps the saved one only when the
// host and username are unchanged and the saved password is readable;
// otherwise it must be typed again. A server that needs no sign-in (username
// and password both blank) is allowed.
func validateEmail(form url.Values, stored EmailSettingsView, passwordReadable bool) (EmailInput, emailFormEcho, []ui.FieldError) {
	var errs []ui.FieldError
	bad := func(id, label, msg string) { errs = append(errs, ui.FieldError{ID: id, Label: label, Message: msg}) }
	typed := func(k string) string { return strings.TrimSpace(form.Get(k)) }

	echo := emailFormEcho{
		Posted:      true,
		Provider:    typed("provider"),
		SESRegion:   typed("ses-region"),
		Host:        typed("host"),
		Port:        typed("port"),
		Security:    typed("security"),
		Username:    typed("username"),
		FromAddress: typed("from"),
		FromName:    typed("from-name"),
	}
	in := EmailInput{Provider: echo.Provider, Username: echo.Username, FromAddress: echo.FromAddress, FromName: echo.FromName}
	password := form.Get("password")

	// Provider decides the host; a preset's port and security default to
	// 587 / STARTTLS when left blank.
	preset := false
	switch in.Provider {
	case mailsettings.ProviderCustom:
		// Lower-cased and without a trailing dot, so the loopback rule for
		// None matches what smtp.PlainAuth later compares.
		in.Host = strings.ToLower(strings.TrimSuffix(echo.Host, "."))
		if in.Host == "" {
			bad("host", "Host", "Enter the SMTP host, for example smtp.example.com.")
		} else if len(in.Host) > mailsettings.MaxHostLen || strings.ContainsAny(in.Host, " \t/@") {
			bad("host", "Host", "Enter just the host name, without spaces, slashes or an @.")
		}
	case mailsettings.ProviderSES:
		preset = true
		if echo.SESRegion == "" {
			echo.SESRegion = mailsettings.DefaultSESRegion
		}
		in.SESRegion = echo.SESRegion
		if !contains(mailsettings.SESRegions, in.SESRegion) {
			bad("ses-region", "Region", "Choose one of the listed regions.")
		} else {
			in.Host = mailsettings.SESHost(in.SESRegion)
		}
	case mailsettings.ProviderGmail:
		preset = true
		in.Host = mailsettings.GmailHost
	default:
		bad("provider", "Provider", "Choose one of the listed providers.")
	}

	portText := echo.Port
	if portText == "" && preset {
		portText = "587"
	}
	if n, err := strconv.Atoi(portText); err != nil || n < 1 || n > 65535 {
		bad("port", "Port", "Enter a port from 1 to 65535, usually 587 or 465.")
	} else {
		in.Port = n
	}

	secText := echo.Security
	if secText == "" && preset {
		secText = string(email.SecurityStarttls)
	}
	switch sec := email.Security(secText); sec {
	case email.SecurityStarttls, email.SecurityTLS:
		in.Security = sec
	case email.SecurityNone:
		in.Security = sec
		if in.Host != "" && !mailsettings.IsLoopbackHost(in.Host) {
			bad("security", "Security", "None is only allowed for localhost, 127.0.0.1 or ::1. Use STARTTLS or TLS (SSL) for any other host.")
		}
	default:
		bad("security", "Security", "Choose STARTTLS, TLS (SSL) or None.")
	}

	if len(in.Username) > mailsettings.MaxUsernameLen {
		bad("username", "Username", "The username is too long.")
	} else if _, ok := email.SanitizeHeader(in.Username); !ok {
		bad("username", "Username", "The username cannot contain a line break.")
	}

	switch {
	case password != "":
		in.Password = password
		if in.Username == "" {
			bad("username", "Username", "Enter the username that goes with this password.")
		}
	case in.Username == "":
		// No sign-in at all.
	case passwordReadable && stored.PasswordSaved && in.Host == stored.Host && in.Username == stored.Username:
		echo.KeepPassword = true
	default:
		bad("password", "Password", "Enter the password for this host and username.")
	}

	switch {
	case in.FromAddress == "" || !strings.Contains(in.FromAddress, "@") || len(in.FromAddress) > mailsettings.MaxAddressLen:
		bad("from", "From address", "Enter the address mail is sent from, for example hello@example.com.")
	default:
		if _, ok := email.SanitizeHeader(in.FromAddress); !ok {
			bad("from", "From address", "The address cannot contain a line break.")
		}
	}
	if len(in.FromName) > mailsettings.MaxFromNameLen {
		bad("from-name", "From name", "The name is too long.")
	} else if _, ok := email.SanitizeHeader(in.FromName); !ok {
		bad("from-name", "From name", "The name cannot contain a line break.")
	}

	return in, echo, errs
}
