# Spam protection

Every public form submission passes one spam pipeline on the server, whether it comes from the
JavaScript form or a plain browser post. Configure it once at **Settings > Forms > Spam protection**
(owners only). Each form then chooses whether it also needs the captcha.

## What runs, in order

1. **Honeypot.** A hidden field people leave empty. A filled field gets a fake success and nothing
   is stored. Unchanged.
2. **Captcha.** Only for forms with **Require the spam check (captcha)** switched on, and only when
   a provider and a readable secret key are set. A missing or failed check answers 422 with "Please
   complete the spam check and try again." and nothing is stored. A provider outage counts as a
   failed check.
3. **Minimum time to submit.** A submission faster than the setting (default 3 seconds) trips a rule.
4. **Content rules.** More links than allowed (default 2), a blocked word (not case sensitive; entries match whole words, so `casino` does not block
   `casinos`, while an entry that starts or ends with punctuation such as `.ru` or `@mail.ru`
   matches inside longer text), or a
   blocked script such as Cyrillic trips a rule.

The minimum time and the content rules apply to every form, with or without the captcha.

When a rule trips, **Save as Spam** (the default) stores the submission with the Spam status. The
visitor still sees the normal success message, but no notification email, confirmation email or
form hook fires. Find these in Submissions with the Spam filter, and change the status of any false positive.
**Refuse submission** answers 422 ("Your message could not be accepted.") and stores nothing.

## Providers

| Provider | Notes |
|---|---|
| Cloudflare Turnstile | Visible widget above the submit button. |
| Google reCAPTCHA v3 | Invisible. Scores below 0.5 fail. Version 2 (checkbox) is not supported. |
| hCaptcha | Visible widget above the submit button. |

Create a site in the provider's dashboard, paste the **site key** and **secret key** into the page,
and save. **Test connection** checks the keys as entered with the provider, before you save (a blank secret field tests the saved secret). Only the
chosen provider's addresses are added to the page's Content Security Policy, and only while a
provider is set.

The secret key is encrypted in the database with a key derived from `HATAMOTO_SECRET`. It is never
shown again, never written to the audit log and never sent to the browser. Leave the field blank
when saving to keep the stored key.

## Switch on, no keys

If a form requires the captcha but no provider keys are usable, submissions are accepted (the other
layers still run) and a warning appears on the form editor and on the Spam protection page. This
is deliberate: a misconfigured site keeps taking enquiries instead of silently dropping them.

## Operating notes

- **Rotating `HATAMOTO_SECRET`** makes the saved captcha secret unreadable. The site then behaves as
  if no keys were set and the page asks you to enter the secret key again. Nothing else breaks.
- **Page cache.** Saving the page purges the page cache, so the widget and site key appear at once.
  A CDN or browser that caches pages itself can serve the old Content Security Policy for up to
  5 minutes. A cached page from before the captcha was switched on has no widget, so the form is
  refused. The page reloads itself once to fetch the current version, keeping what the visitor
  typed (and the original timing token) in the tab's sessionStorage; if the form is still
  refused after that, the visitor sees the message and can reload by hand once the cache expires.
- **Timing token.** The page fetches a signed token when it loads, so cached HTML never contains
  one. A post without a token (JavaScript off, or a bot that skips it) skips only the minimum time
  check. The captcha and the content rules still apply, and a captcha form refuses it.
- **Replays.** The token is not single use. The per-IP submit rate limit covers this.
- **Without JavaScript** a captcha form cannot be submitted, because the check needs a script.
  Forms without the switch still work.

## Edge advice

This feature stops spam at the application. It does not replace edge protection. If you run behind
Cloudflare or another WAF, also turn on its bot and rate limiting rules for `POST /forms/*` and
`POST /api/v1/public/forms/*`. These settings are not managed by Hatamoto.
