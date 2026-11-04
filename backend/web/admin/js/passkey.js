// Passkey sign-in. Progressive enhancement: the button is hidden in the HTML and
// shown only when the browser can do WebAuthn; the password and code forms work without it.
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

const requestOptions = (o) => {
  const pk = o.publicKey ?? o;
  return {
    publicKey: {
      ...pk,
      challenge: b64uToBuf(pk.challenge),
      allowCredentials: (pk.allowCredentials ?? []).map((c) => ({ ...c, id: b64uToBuf(c.id) })),
    },
  };
};

const assertionJSON = (cred) => ({
  id: cred.id,
  rawId: bufToB64u(cred.rawId),
  type: cred.type,
  authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
  clientExtensionResults: cred.getClientExtensionResults(),
  response: {
    authenticatorData: bufToB64u(cred.response.authenticatorData),
    clientDataJSON: bufToB64u(cred.response.clientDataJSON),
    signature: bufToB64u(cred.response.signature),
    userHandle: cred.response.userHandle ? bufToB64u(cred.response.userHandle) : undefined,
  },
});

const base = document.querySelector('meta[name="admin-base"]')?.content ?? "/admin";
const postJSON = (path, body) =>
  fetch(base + path, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: body === undefined ? "{}" : JSON.stringify(body),
  });

async function signIn(button, status) {
  const next = button.dataset.next ?? "";
  const q = next ? "?next=" + encodeURIComponent(next) : "";
  try {
    const begin = await postJSON("/login/passkey/begin" + q);
    if (!begin.ok) throw new Error((await begin.json()).error);
    const { ceremony, options } = await begin.json();
    const cred = await navigator.credentials.get(requestOptions(options));
    const fin = await postJSON("/login/passkey/finish?c=" + encodeURIComponent(ceremony) + (next ? "&next=" + encodeURIComponent(next) : ""), assertionJSON(cred));
    const out = await fin.json();
    if (!fin.ok) throw new Error(out.error);
    location.assign(out.redirect);
  } catch (e) {
    status.textContent = e?.name === "NotAllowedError" ? "Passkey sign-in was cancelled." : (e?.message || "That did not work. Check your details and try again.");
    button.disabled = false;
  }
}

if (window.PublicKeyCredential) {
  const status = document.getElementById("passkey-status");
  for (const button of document.querySelectorAll("[data-passkey]")) {
    button.hidden = false;
    button.addEventListener("click", () => {
      button.disabled = true;
      if (status) status.textContent = "";
      signIn(button, status ?? { textContent: "" });
    });
  }
}
