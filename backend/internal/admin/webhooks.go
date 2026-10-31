package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/webhook"
)

const webhooksList = "/settings/webhooks"

const (
	webhooksDeliveries   = webhooksList + "/deliveries"
	webhookDeliveriesPer = 50
	answerClip           = 80
)

type webhooksData struct {
	Table ui.Table
	// DeliveryOff shows the notice that nothing is sent on this server.
	DeliveryOff bool
	// PluginLeft shows the notice that a plugin named webhooks is still installed.
	PluginLeft bool
	Form       webhookForm
	Secret     *ui.Secret
	// SecretGone: the URL carried a secret handle that is used up, expired or not the user's.
	SecretGone bool
	Actions    []ui.Button
}

// webhookFormPage drives pages/settings/webhooknew: the create and edit page,
// also the 422 re-render of a failed submission.
type webhookFormPage struct {
	Form                               webhookForm
	Summary                            []ui.FieldError
	Action, Title, Description, Submit string
}

// webhookForm is what hm/form-webhook shows. Numbers stay strings so a typo is
// shown back as typed and 0 is still a value.
type webhookForm struct {
	Name, URL, Headers              string
	Active, AllowHTTP               bool
	MaxRetries, RetryDelay, Timeout string
	EventOptions                    []scopeOption
	Errs                            map[string]string
}

func newWebhookForm(sub webhook.Subscription, events []string) webhookForm {
	f := webhookForm{
		Name: sub.Name, URL: sub.URL, Active: sub.Active, AllowHTTP: sub.AllowHTTP,
		MaxRetries: strconv.Itoa(sub.MaxRetries), RetryDelay: strconv.Itoa(sub.RetryDelaySeconds), Timeout: strconv.Itoa(sub.TimeoutSeconds),
		Errs: map[string]string{},
	}
	names := make([]string, 0, len(sub.Headers))
	for k := range sub.Headers {
		names = append(names, k)
	}
	slices.Sort(names)
	var lines []string
	for _, k := range names {
		lines = append(lines, k+": "+sub.Headers[k])
	}
	f.Headers = strings.Join(lines, "\n")
	for _, e := range webhook.ValidEvents() {
		f.EventOptions = append(f.EventOptions, scopeOption{Value: e, Label: e, Checked: slices.Contains(events, e)})
	}
	return f
}

func defaultWebhookForm() webhookForm {
	var sub webhook.Subscription
	sub.ApplyDefaults()
	return newWebhookForm(sub, nil)
}

func (a *Admin) mountWebhooks(owner *httpx.Group) {
	owner.HandleFunc("GET /settings/webhooks", a.handleWebhooks)
	owner.HandleFunc("GET /settings/webhooks/new", a.handleWebhookNew)
	owner.HandleFunc("GET /settings/webhooks/deliveries", a.handleWebhookDeliveries)
	owner.HandleFunc("POST /settings/webhooks/deliveries/{id}/retry", a.handleWebhookRetry)
	owner.HandleFunc("POST /settings/webhooks", a.handleWebhookCreate)
	owner.HandleFunc("GET /settings/webhooks/{id}/edit", a.handleWebhookEdit)
	owner.HandleFunc("POST /settings/webhooks/{id}", a.handleWebhookUpdate)
	owner.HandleFunc("GET /settings/webhooks/{id}/{action}", a.handleWebhookConfirm)
	owner.HandleFunc("POST /settings/webhooks/{id}/pause", a.handleWebhookPause)
	owner.HandleFunc("POST /settings/webhooks/{id}/resume", a.handleWebhookResume)
	owner.HandleFunc("POST /settings/webhooks/{id}/test", a.handleWebhookTest)
	owner.HandleFunc("POST /settings/webhooks/{id}/new-secret", a.handleWebhookNewSecret)
	owner.HandleFunc("POST /settings/webhooks/{id}/delete", a.handleWebhookDelete)
}

func (a *Admin) webhooksUnavailable(w http.ResponseWriter, r *http.Request) bool {
	if a.deps.Webhooks != nil {
		return false
	}
	a.Error(w, r, http.StatusNotFound, "Not found", "Webhooks are not available on this server.")
	return true
}

func (a *Admin) webhookActions(s webhook.ListedSubscription) []ui.Action {
	base := a.adminPath + webhooksList + "/" + s.ID
	toggle := ui.Action{Label: "Pause", Post: base + "/pause"}
	if !s.Active {
		toggle = ui.Action{Label: "Resume", Post: base + "/resume"}
	}
	return []ui.Action{
		{Label: "Edit", Href: base + "/edit"},
		toggle,
		{Label: "Send test", Post: base + "/test"},
		{Label: "New secret", Href: base + "/new-secret", Post: base + "/new-secret", ConfirmLabel: "Create new secret",
			Confirm: "The old secret for " + s.Name + " stops working at once. Receivers must be updated to the new secret."},
		{Label: "Delete", Href: base + "/delete", Post: base + "/delete", ConfirmLabel: "Delete webhook", Danger: true,
			Confirm: "Delete " + s.Name + "? Its delivery log goes with it. This cannot be undone."},
	}
}

func (a *Admin) webhooksTable(r *http.Request, subs []webhook.ListedSubscription) ui.Table {
	t := ui.Table{
		ID: "webhooks", Caption: "Webhooks", BaseURL: a.adminPath + webhooksList, CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "name", Label: "Name"}, {Key: "url", Label: "Address"}, {Key: "events", Label: "Events"}, {Key: "status", Label: "Status"}},
		Empty:   ui.Empty{Icon: "send", Title: "No webhooks", Body: "Add a webhook to tell another system when your content changes."},
	}
	for _, s := range subs {
		status := ui.Badge{Label: "Active", Kind: "success"}
		if !s.Active {
			status = ui.Badge{Label: "Paused", Kind: "muted"}
		}
		address := ui.Cell{Text: s.URL, Clip: true}
		if strings.HasPrefix(strings.ToLower(s.URL), "http://") {
			address.Tag = &ui.Badge{Label: "not encrypted", Kind: "warning"}
		}
		t.Rows = append(t.Rows, ui.Row{ID: s.ID, Label: s.Name, Cells: []ui.Cell{
			{Text: s.Name}, address, {Text: fmt.Sprintf("%d %s", len(s.Events), plural(len(s.Events), "event", "events"))}, {Badge: &status},
		}, Actions: a.webhookActions(s)})
	}
	return t
}

func (a *Admin) handleWebhooks(w http.ResponseWriter, r *http.Request) {
	if a.webhooksUnavailable(w, r) {
		return
	}
	subs, err := a.deps.Webhooks.List(r.Context())
	if err != nil {
		a.logger().Error("list webhooks failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The webhooks could not be loaded.")
		return
	}
	d := webhooksData{Table: a.webhooksTable(r, subs), DeliveryOff: !a.deps.WebhookDeliveryOn, Form: defaultWebhookForm()}
	d.PluginLeft = a.deps.WebhooksPluginLeft != nil && a.deps.WebhooksPluginLeft(r.Context())
	if id := createdID(r); id != "" {
		d.Table.Rows = pinFirst(d.Table.Rows, id)
		d.Table.CreatedID = id
		if e, ok := a.secrets.take(userFrom(r.Context()).ID, r.URL.Query().Get("secret")); ok {
			b := e.banner("Signing secret for "+e.name, a.adminPath+webhooksList)
			b.Note = "Copy it now and give it to the receiver."
			d.Secret = &b
		} else if r.URL.Query().Get("secret") != "" {
			d.SecretGone = true
		}
	}
	d.Actions = []ui.Button{{Label: "New webhook", Variant: "primary", Icon: "plus", Href: a.adminPath + webhooksList + "/new", Attrs: map[string]string{"data-hm-open": "new-webhook"}}}
	v := a.NewView(w, r, "Webhooks")
	v.Section, v.Tab = "webhooks", "subscriptions"
	v.Data = d
	a.Render(w, r, http.StatusOK, "settings/webhooks", v)
}

func (a *Admin) renderWebhookForm(w http.ResponseWriter, r *http.Request, status int, p webhookFormPage, crumb string) {
	v := a.NewView(w, r, crumb)
	v.Width = "narrow"
	v.Section, v.Tab = "webhooks", "subscriptions"
	v.Crumbs = a.areaCrumbs("subscriptions", Crumb{Label: crumb})
	v.Data = p
	a.Render(w, r, status, "settings/webhooknew", v)
}

func (a *Admin) newWebhookPage(f webhookForm) webhookFormPage {
	return webhookFormPage{Form: f, Action: a.adminPath + webhooksList, Title: "New webhook", Description: "The signing secret is shown once. Keep it somewhere safe.", Submit: "Create webhook"}
}

func (a *Admin) editWebhookPage(id string, f webhookForm) webhookFormPage {
	return webhookFormPage{Form: f, Action: a.adminPath + webhooksList + "/" + id, Title: "Edit webhook", Description: "The signing secret cannot be shown again. Use New secret on the list to replace it.", Submit: "Save webhook"}
}

// handleWebhookNew is the page behind the header's New webhook link: what a
// browser without JavaScript sees instead of the dialog.
func (a *Admin) handleWebhookNew(w http.ResponseWriter, r *http.Request) {
	if a.webhooksUnavailable(w, r) {
		return
	}
	a.renderWebhookForm(w, r, http.StatusOK, a.newWebhookPage(defaultWebhookForm()), "New webhook")
}

// webhookInput reads the form. Problems that belong to one field (a number, a
// header line) land in f.Errs; the webhook package judges the rest.
func webhookInput(r *http.Request) (sub webhook.Subscription, f webhookForm) {
	_ = r.ParseForm() // PostForm is empty until parsed; a bad body just reads as no fields
	events := r.PostForm["events"]
	f = webhookForm{
		Name: strings.TrimSpace(r.PostFormValue("name")), URL: strings.TrimSpace(r.PostFormValue("url")), Headers: r.PostFormValue("headers"),
		Active: r.PostFormValue("active") != "", AllowHTTP: r.PostFormValue("allow_http") != "",
		MaxRetries: strings.TrimSpace(r.PostFormValue("max_retries")), RetryDelay: strings.TrimSpace(r.PostFormValue("retry_delay_seconds")), Timeout: strings.TrimSpace(r.PostFormValue("timeout_seconds")),
		Errs: map[string]string{},
	}
	f.EventOptions = newWebhookForm(webhook.Subscription{}, events).EventOptions
	number := func(key, raw string) int {
		n, err := strconv.Atoi(raw)
		if err != nil {
			f.Errs[key] = "Enter a whole number"
		}
		return n
	}
	sub = webhook.Subscription{
		Name: f.Name, URL: f.URL, Events: events, Active: f.Active, AllowHTTP: f.AllowHTTP,
		MaxRetries: number("max_retries", f.MaxRetries), RetryDelaySeconds: number("retry_delay_seconds", f.RetryDelay), TimeoutSeconds: number("timeout_seconds", f.Timeout),
		Headers: map[string]string{},
	}
	for i, line := range strings.Split(strings.ReplaceAll(f.Headers, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if name = strings.TrimSpace(name); !ok || name == "" {
			f.Errs["headers"] = fmt.Sprintf("Line %d: write the header as Name: value", i+1)
			continue
		}
		sub.Headers[name] = strings.TrimSpace(value)
	}
	return sub, f
}

// webhookFieldFor maps a webhook.ValidationError message to the field it is about.
func webhookFieldFor(msg string) (key, id, label string) {
	switch {
	case strings.HasPrefix(msg, "name"):
		return "name", "webhook-name", "Name"
	case strings.HasPrefix(msg, "url"):
		return "url", "webhook-url", "Address"
	case strings.Contains(msg, "event"):
		return "events", "event-0", "Events"
	case strings.HasPrefix(msg, "max_retries"):
		return "max_retries", "webhook-max-retries", "Retries"
	case strings.HasPrefix(msg, "retry_delay"):
		return "retry_delay_seconds", "webhook-retry-delay", "First retry after"
	case strings.HasPrefix(msg, "timeout"):
		return "timeout_seconds", "webhook-timeout", "Timeout"
	case strings.Contains(msg, "header"):
		return "headers", "webhook-headers", "Custom headers"
	}
	return "name", "webhook-name", "Webhook"
}

// webhookProblems collects the form's field errors, plus err when it is a
// validation error, into the summary and the per-field messages.
func webhookProblems(f *webhookForm, err error) []ui.FieldError {
	var summary []ui.FieldError
	labels := map[string][2]string{
		"max_retries": {"webhook-max-retries", "Retries"}, "retry_delay_seconds": {"webhook-retry-delay", "First retry after"},
		"timeout_seconds": {"webhook-timeout", "Timeout"}, "headers": {"webhook-headers", "Custom headers"},
	}
	for _, k := range []string{"max_retries", "retry_delay_seconds", "timeout_seconds", "headers"} {
		if msg := f.Errs[k]; msg != "" {
			summary = append(summary, ui.FieldError{ID: labels[k][0], Label: labels[k][1], Message: msg})
		}
	}
	var ve *webhook.ValidationError
	if len(summary) == 0 && errors.As(err, &ve) {
		key, id, label := webhookFieldFor(ve.Msg)
		f.Errs[key] = ve.Msg
		summary = append(summary, ui.FieldError{ID: id, Label: label, Message: ve.Msg})
	}
	return summary
}

func (a *Admin) handleWebhookCreate(w http.ResponseWriter, r *http.Request) {
	if a.webhooksUnavailable(w, r) {
		return
	}
	in, f := webhookInput(r)
	// Defaults for what the form does not carry; every field it does carry is set above.
	var sub webhook.Subscription
	sub.ApplyDefaults()
	sub.Name, sub.URL, sub.Events, sub.Headers, sub.Active, sub.AllowHTTP = in.Name, in.URL, in.Events, in.Headers, in.Active, in.AllowHTTP
	sub.MaxRetries, sub.RetryDelaySeconds, sub.TimeoutSeconds = in.MaxRetries, in.RetryDelaySeconds, in.TimeoutSeconds
	var err error
	if len(f.Errs) == 0 {
		var secret string
		var created webhook.Subscription
		if created, secret, err = a.deps.Webhooks.Create(r.Context(), sub); err == nil {
			a.SetFlash(w, r, "success", "Webhook created.")
			token := a.secrets.putEntry(secretEntry{user: userFrom(r.Context()).ID, name: created.Name, key: secret, kind: "webhook"})
			http.Redirect(w, r, createdRedirect(a.adminPath+webhooksList, created.ID)+"&secret="+token, http.StatusSeeOther)
			return
		}
	}
	if summary := webhookProblems(&f, err); len(summary) > 0 {
		p := a.newWebhookPage(f)
		p.Summary = summary
		a.renderWebhookForm(w, r, http.StatusUnprocessableEntity, p, "New webhook")
		return
	}
	a.logger().Error("create webhook failed", "err", err)
	a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The webhook could not be created.")
}

func (a *Admin) handleWebhookEdit(w http.ResponseWriter, r *http.Request) {
	if a.webhooksUnavailable(w, r) {
		return
	}
	id := r.PathValue("id")
	sub, err := a.deps.Webhooks.Get(r.Context(), id)
	if errors.Is(err, webhook.ErrNotFound) {
		a.Error(w, r, http.StatusNotFound, "Not found", "That webhook does not exist.")
		return
	}
	if err != nil {
		a.logger().Error("get webhook failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The webhook could not be loaded.")
		return
	}
	a.renderWebhookForm(w, r, http.StatusOK, a.editWebhookPage(id, newWebhookForm(*sub, sub.Events)), "Edit "+sub.Name)
}

func (a *Admin) handleWebhookUpdate(w http.ResponseWriter, r *http.Request) {
	if a.webhooksUnavailable(w, r) {
		return
	}
	id := r.PathValue("id")
	in, f := webhookInput(r)
	var err error
	if len(f.Errs) == 0 {
		err = a.deps.Webhooks.Update(r.Context(), id, webhook.SubscriptionPatch{
			Name: &in.Name, URL: &in.URL, Events: &in.Events, Headers: &in.Headers, Active: &in.Active, AllowHTTP: &in.AllowHTTP,
			MaxRetries: &in.MaxRetries, RetryDelaySeconds: &in.RetryDelaySeconds, TimeoutSeconds: &in.TimeoutSeconds,
		})
		if err == nil {
			a.SetFlash(w, r, "success", "Webhook saved.")
			http.Redirect(w, r, a.adminPath+webhooksList, http.StatusSeeOther)
			return
		}
		if errors.Is(err, webhook.ErrNotFound) {
			a.Error(w, r, http.StatusNotFound, "Not found", "That webhook does not exist.")
			return
		}
	}
	if summary := webhookProblems(&f, err); len(summary) > 0 {
		p := a.editWebhookPage(id, f)
		p.Summary = summary
		a.renderWebhookForm(w, r, http.StatusUnprocessableEntity, p, "Edit webhook")
		return
	}
	a.logger().Error("update webhook failed", "err", err)
	a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The webhook could not be saved.")
}

// webhookFailed answers a store error on a row action: 404 for an unknown
// webhook, 500 otherwise. It reports whether it wrote a response.
func (a *Admin) webhookFailed(w http.ResponseWriter, r *http.Request, err error, what string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, webhook.ErrNotFound):
		a.Error(w, r, http.StatusNotFound, "Not found", "That webhook does not exist.")
	default:
		a.logger().Error(what+" failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The change could not be made.")
	}
	return true
}

// handleWebhookConfirm is the page behind the row actions' confirmation
// dialog: what a browser without JavaScript sees.
func (a *Admin) handleWebhookConfirm(w http.ResponseWriter, r *http.Request) {
	if a.webhooksUnavailable(w, r) {
		return
	}
	id, action := r.PathValue("id"), r.PathValue("action")
	if action != "new-secret" && action != "delete" {
		a.Error(w, r, http.StatusNotFound, "Not found", "That page does not exist.")
		return
	}
	sub, err := a.deps.Webhooks.Get(r.Context(), id)
	if a.webhookFailed(w, r, err, "get webhook") {
		return
	}
	d := confirmData{Post: a.adminPath + webhooksList + "/" + id + "/" + action, Back: a.adminPath + webhooksList}
	if action == "delete" {
		d.Title, d.Label, d.Danger = "Delete "+sub.Name+"?", "Delete webhook", true
		d.Message = "Its delivery log goes with it. This cannot be undone."
	} else {
		d.Title, d.Label = "Create a new secret for "+sub.Name+"?", "Create new secret"
		d.Message = "The old secret stops working at once. Receivers must be updated to the new secret."
	}
	a.renderConfirm(w, r, d)
}

func (a *Admin) handleWebhookPause(w http.ResponseWriter, r *http.Request) {
	a.setWebhookActive(w, r, false, "Webhook paused. Nothing is sent to it until you resume it.")
}

func (a *Admin) handleWebhookResume(w http.ResponseWriter, r *http.Request) {
	a.setWebhookActive(w, r, true, "Webhook resumed.")
}

func (a *Admin) setWebhookActive(w http.ResponseWriter, r *http.Request, active bool, msg string) {
	if a.webhooksUnavailable(w, r) {
		return
	}
	if a.webhookFailed(w, r, a.deps.Webhooks.SetActive(r.Context(), r.PathValue("id"), active), "set webhook active") {
		return
	}
	a.SetFlash(w, r, "success", msg)
	http.Redirect(w, r, a.adminPath+webhooksList, http.StatusSeeOther)
}

// handleWebhookTest sends one test delivery now and says how the receiver
// answered. It is a POST: a GET must never call a receiver.
func (a *Admin) handleWebhookTest(w http.ResponseWriter, r *http.Request) {
	if a.webhooksUnavailable(w, r) {
		return
	}
	row, err := a.deps.Webhooks.SendTest(r.Context(), r.PathValue("id"))
	if a.webhookFailed(w, r, err, "send webhook test") {
		return
	}
	switch {
	case row.Status == "delivered" && row.ResponseStatus != nil:
		a.SetFlash(w, r, "success", fmt.Sprintf("Test sent (delivery %s): answer HTTP %d.", row.ID, *row.ResponseStatus))
	case row.Error == "subscription removed or paused":
		a.SetFlash(w, r, "error", "Test not sent: the webhook is paused. Resume it first.")
	default:
		a.SetFlash(w, r, "error", fmt.Sprintf("Test failed (delivery %s): ", row.ID)+fullStop(row.Error))
	}
	http.Redirect(w, r, a.adminPath+webhooksList, http.StatusSeeOther)
}

func (a *Admin) handleWebhookNewSecret(w http.ResponseWriter, r *http.Request) {
	if a.webhooksUnavailable(w, r) {
		return
	}
	id := r.PathValue("id")
	sub, err := a.deps.Webhooks.Get(r.Context(), id)
	if a.webhookFailed(w, r, err, "get webhook") {
		return
	}
	secret, err := a.deps.Webhooks.RegenerateSecret(r.Context(), id)
	if a.webhookFailed(w, r, err, "regenerate webhook secret") {
		return
	}
	a.SetFlash(w, r, "success", "New secret created. The old one no longer works.")
	token := a.secrets.putEntry(secretEntry{user: userFrom(r.Context()).ID, name: sub.Name, key: secret, kind: "webhook"})
	http.Redirect(w, r, createdRedirect(a.adminPath+webhooksList, id)+"&secret="+token, http.StatusSeeOther)
}

func (a *Admin) handleWebhookDelete(w http.ResponseWriter, r *http.Request) {
	if a.webhooksUnavailable(w, r) {
		return
	}
	if a.webhookFailed(w, r, a.deps.Webhooks.Delete(r.Context(), r.PathValue("id")), "delete webhook") {
		return
	}
	a.SetFlash(w, r, "success", "Webhook deleted.")
	http.Redirect(w, r, a.adminPath+webhooksList, http.StatusSeeOther)
}

func deliveryStatus(status string) ui.Badge {
	switch status {
	case "delivered":
		return ui.Badge{Label: "Delivered", Kind: "success"}
	case "failed":
		return ui.Badge{Label: "Failed", Kind: "danger"}
	}
	return ui.Badge{Label: "Pending", Kind: "warning"}
}

// deliveryAnswer is what the receiver said: its HTTP status, else the error.
func deliveryAnswer(d webhook.DeliveryRow) string {
	switch {
	case d.ResponseStatus != nil:
		status := fmt.Sprintf("HTTP %d", *d.ResponseStatus)
		// The deliverer's own message for a non-2xx answer only repeats the status.
		if d.Error == "" || d.Error == "receiver answered "+status {
			return status
		}
		return status + ": " + d.Error
	case d.Error != "":
		return d.Error
	}
	return "Not sent yet"
}

func clipAnswer(s string) string {
	if r := []rune(s); len(r) > answerClip {
		return string(r[:answerClip]) + "…"
	}
	return s
}

// deliveriesTable is the read-only log; only a failed row has an action (Retry).
func (a *Admin) deliveriesTable(r *http.Request, rows []webhook.DeliveryRow, page, total int, q url.Values) ui.Table {
	t := ui.Table{
		ID: "webhook-deliveries", Caption: "Delivery log", BaseURL: a.adminPath + webhooksDeliveries, CSRF: csrfToken(r.Context()),
		Columns: []ui.Col{{Key: "when", Label: "When"}, {Key: "event", Label: "Event"}, {Key: "status", Label: "Status"}, {Key: "attempts", Label: "Attempts", Align: "right"}, {Key: "answer", Label: "Answer"}},
		Page:    ui.Pagination{Page: page, PerPage: webhookDeliveriesPer, Total: total, BaseURL: a.adminPath + webhooksDeliveries, Query: q},
		Empty:   ui.Empty{Icon: "send", Title: "No deliveries yet", Body: "Deliveries appear here when a webhook is sent."},
	}
	for _, d := range rows {
		status := deliveryStatus(d.Status)
		row := ui.Row{ID: d.ID, Label: d.Event + " at " + fmtDateTime(d.CreatedAt.UTC()), Cells: []ui.Cell{
			{Text: fmtDateTime(d.CreatedAt.UTC()), Nowrap: true}, {Text: d.Event}, {Badge: &status},
			{Text: fmt.Sprintf("%d/%d", d.Attempts, d.MaxAttempts)}, {Text: clipAnswer(deliveryAnswer(d)), Clip: true},
		}}
		if d.Status == "failed" {
			row.Actions = []ui.Action{{Label: "Retry", Post: a.adminPath + webhooksDeliveries + "/" + d.ID + "/retry"}}
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func (a *Admin) handleWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	if a.webhooksUnavailable(w, r) {
		return
	}
	sub := r.URL.Query().Get("subscription")
	page := pageParam(r)
	rows, total, err := a.deps.Webhooks.ListDeliveries(r.Context(), sub, webhookDeliveriesPer, (page-1)*webhookDeliveriesPer)
	if err != nil {
		a.logger().Error("list webhook deliveries failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The delivery log could not be loaded.")
		return
	}
	q := url.Values{}
	if sub != "" {
		q.Set("subscription", sub)
	}
	v := a.NewView(w, r, "Delivery log")
	v.Section, v.Tab = "webhooks", "deliveries"
	v.Data = webhooksData{Table: a.deliveriesTable(r, rows, page, total, q), DeliveryOff: !a.deps.WebhookDeliveryOn}
	a.Render(w, r, http.StatusOK, "settings/webhookdeliveries", v)
}

func (a *Admin) handleWebhookRetry(w http.ResponseWriter, r *http.Request) {
	if a.webhooksUnavailable(w, r) {
		return
	}
	retried, err := a.deps.Webhooks.RetryDelivery(r.Context(), r.PathValue("id"))
	if a.webhookFailed(w, r, err, "retry webhook delivery") {
		return
	}
	if retried {
		a.SetFlash(w, r, "success", "Queued for another attempt.")
	} else {
		a.SetFlash(w, r, "error", "Only failed deliveries can be retried.")
	}
	http.Redirect(w, r, a.adminPath+webhooksDeliveries, http.StatusSeeOther)
}
