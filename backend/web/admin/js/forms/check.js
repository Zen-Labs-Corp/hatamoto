// "Form check": what a form usually needs before it goes live, as a short list of
// {id, label, ok, hint}. Pure, no imports; a hint says what to do when ok is false.

const isInput = (f) => f.type !== "heading" && f.type !== "paragraph" && f.type !== "divider";

export function formCheck(doc) {
  const fields = doc.fields ?? [];
  const s = doc.settings ?? {};
  const names = fields.filter((f) => isInput(f) && f.name).map((f) => f.name);
  const dup = names.length !== new Set(names).size;
  return [
    { id: "email", label: "Has an email field", ok: fields.some((f) => f.type === "email"), hint: "Add an Email field so you can reply." },
    { id: "spam", label: "Spam protection is on", ok: !!s.enable_captcha, hint: "Turn on spam protection in Settings." },
    { id: "notify", label: "Someone is notified", ok: typeof s.notify_email === "string" && s.notify_email.trim() !== "", hint: "Enter an address to notify in Settings." },
    { id: "names", label: "Every field name is unique", ok: !dup, hint: "Two fields share a name; rename one." },
    { id: "replyto", label: "Reply-To is an email field", ok: !s.notify_reply_to || fields.some((f) => f.type === "email" && f.name === s.notify_reply_to), hint: "The Reply-To field was renamed or removed; choose it again in Settings." },
    // Stored answers are personal data: when they are kept, ask for consent.
    { id: "consent", label: "Asks for consent when answers are stored", ok: !!s.store_off || fields.some((f) => f.type === "consent"), hint: "Add a Consent field: answers are stored." },
  ];
}
