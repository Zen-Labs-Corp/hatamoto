// Adds a passkey to the signed-in account. The page is the only place that needs
// JavaScript: it enables the button when the browser supports WebAuthn.
const b64uToBuf = (s) => {
  const pad = "=".repeat((4 - (s.length % 4)) % 4);
  const bin = atob((s + pad).replace(/-/g, "+").replace(/_/g, "/"));
  return Uint8Array.from(bin, (c) => c.charCodeAt(0)).buffer;
};

const bufToB64u = (buf) => {
  let bin = "";
  for (const b of new Uint8Array(buf)) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
};

const creationOptions = (o) => {
  const pk = o.publicKey ?? o;
  return {
    publicKey: {
      ...pk,
      challenge: b64uToBuf(pk.challenge),
      user: { ...pk.user, id: b64uToBuf(pk.user.id) },
      excludeCredentials: (pk.excludeCredentials ?? []).map((c) => ({ ...c, id: b64uToBuf(c.id) })),
    },
  };
};

const attestationJSON = (cred) => ({
  id: cred.id,
  rawId: bufToB64u(cred.rawId),
  type: cred.type,
  authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
  clientExtensionResults: cred.getClientExtensionResults(),
  response: {
    attestationObject: bufToB64u(cred.response.attestationObject),
    clientDataJSON: bufToB64u(cred.response.clientDataJSON),
    transports: cred.response.getTransports?.() ?? [],
  },
});

const post = (url, body) =>
  fetch(url, { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify(body ?? {}) });

const form = document.querySelector("[data-passkey-register]");
if (form && window.PublicKeyCredential) {
  const status = document.getElementById("passkey-status");
  const button = form.querySelector('button[type="submit"]');
  button.disabled = false;
  status.textContent = "";
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    button.disabled = true;
    status.textContent = "";
    try {
      const begin = await post(form.dataset.url + "/begin");
      if (!begin.ok) throw new Error((await begin.json()).error);
      const { ceremony, options } = await begin.json();
      const cred = await navigator.credentials.create(creationOptions(options));
      const name = form.elements.name.value;
      const fin = await post(form.dataset.url + "/finish?c=" + encodeURIComponent(ceremony) + "&name=" + encodeURIComponent(name), attestationJSON(cred));
      const out = await fin.json();
      if (!fin.ok) throw new Error(out.error);
      location.assign(out.redirect);
    } catch (err) {
      status.textContent = err?.name === "NotAllowedError" ? "Adding the passkey was cancelled." : err?.message || "That did not work. Try again.";
      button.disabled = false;
    }
  });
}
