// Form: field logic (mirrors form.Visible), inline errors, page URL for hidden fields.
for (const form of document.querySelectorAll("form[data-hm-form-logic]")) {
  const rules = [...form.querySelectorAll("[data-hm-logic-field]")];
  const byName = {};
  for (const el of rules) {
    const name = el.querySelector("[name]")?.name;
    if (name) byName[name] = el;
  }
  const controls = (name) => {
    const found = form.elements[name];
    return found instanceof RadioNodeList ? [...found] : found ? [found] : [];
  };
  const values = (name) => {
    const out = controls(name)
      .filter((i) => !/^(checkbox|radio)$/.test(i.type) || i.checked)
      .map((i) => i.value.trim());
    return out.length ? out : [""];
  };
  const test = (op, got, want) => {
    const filled = got.some(Boolean);
    if (op === "filled") return filled;
    if (op === "empty") return !filled;
    if (op === "contains") return got.some((v) => v.includes(want));
    return got.includes(want) === (op === "is");
  };
  const shown = (el, seen) => {
    if (!el) return true;
    if (seen.has(el)) return false;
    seen.add(el);
    const src = el.dataset.hmLogicField;
    const got = shown(byName[src], seen) ? values(src) : [""];
    return test(el.dataset.hmLogicOp, got, el.dataset.hmLogicValue);
  };
  const run = () => {
    for (const el of rules) {
      const show = shown(el, new Set());
      el.hidden = !show;
      for (const i of el.querySelectorAll("input,select,textarea")) i.disabled = !show;
    }
  };
  form.addEventListener("input", run);
  form.addEventListener("change", run);
  run();
  for (const [n, v] of [["hm_page", location.href], ["hm_ref", document.referrer]]) {
    Object.assign(form.elements[n] || form.appendChild(document.createElement("input")), { type: "hidden", name: n, value: v });
  }
  const slots = () => form.querySelectorAll("[data-hm-error-for]");
  const clear = () => {
    for (const slot of slots()) slot.hidden = true;
    for (const i of form.querySelectorAll("[aria-invalid]")) i.removeAttribute("aria-invalid");
  };
  const fail = (fields) => {
    let first;
    for (const slot of slots()) {
      const text = fields[slot.dataset.hmErrorFor];
      if (!text) continue;
      slot.textContent = text;
      slot.hidden = false;
      const input = controls(slot.dataset.hmErrorFor)[0];
      input?.setAttribute("aria-invalid", "true");
      input?.setAttribute("aria-describedby", slot.id);
      first ||= input;
    }
    first?.focus();
    return first;
  };
  const slug = /^\/forms\/([^/]+)\/submit$/.exec(form.getAttribute("action"))?.[1];
  if (slug && !form.querySelector("[data-hm-captcha]")) {
    form.noValidate = true;
    form.addEventListener("submit", async (e) => {
      if (form.elements.hm_website?.value) return;
      e.preventDefault();
      clear();
      const d = new FormData(form);
      const res = await fetch("/api/v1/public/forms/" + slug + "/submit", {
        method: "POST",
        headers: { "X-Page-URL": location.href },
        body: form.querySelector("[type=file]") ? d : new URLSearchParams(d),
      }).catch(() => null);
      const body = (await res?.json().catch(() => null)) || {};
      if (!res || (!res.ok && !(body.fields && fail(body.fields)))) return form.submit();
      if (!res.ok) return;
      if (body.redirect_url && /^https?:$/.test(new URL(body.redirect_url, location.href).protocol)) return location.assign(body.redirect_url);
      if (body.submission_id) {
        document.dispatchEvent(new CustomEvent("hm:conversion", { detail: { id: body.submission_id, form: slug, value: null } }));
      }
      const ok = document.createElement("p");
      ok.className = "hm-form__success";
      ok.role = "status";
      ok.textContent = body.message || "Thank you, your message has been sent.";
      form.replaceWith(ok);
    });
  }
}
