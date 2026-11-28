// Backup encryption key card: reveal the Generate button, make an age key pair
// in the page and show the private key once. The server renders the controls
// hidden. The private key only ever lives in the unnamed textarea, so it is
// never part of the form post: Save sends the public key alone.
import { generateKeyPair, keyFileText } from "@hm/backupkeylogic";

function init(form) {
  const gen = form.querySelector("[data-hm-backupkey-generate]");
  const reveal = form.querySelector("[data-hm-backupkey-reveal]");
  const priv = form.querySelector("#backupkey-private");
  const saved = form.querySelector("[data-hm-backupkey-saved]");
  const pub = form.querySelector("#backupkey-public");
  const status = form.querySelector("[data-hm-backupkey-status]");
  if (!gen || !reveal || !priv || !saved || !pub) return;

  let pending = false;
  const say = (t) => {
    if (status) status.textContent = t;
  };
  const clear = () => {
    priv.value = "";
    reveal.hidden = true;
    pending = false;
  };

  gen.hidden = false;
  gen.addEventListener("click", () => {
    const pair = generateKeyPair();
    pub.value = pair.publicKey;
    priv.value = keyFileText(pair, new Date().toISOString());
    reveal.hidden = false;
    pending = true;
    const mode = form.querySelector('input[name="mode"][value="generate"]');
    if (mode) mode.checked = true;
    say("Save the private key in your password manager, then press I saved the private key.");
    priv.focus();
    priv.select();
  });
  saved.addEventListener("click", () => {
    clear();
    say("Now press Save to turn on encryption.");
  });
  form.addEventListener("submit", (e) => {
    if (!pending) return;
    e.preventDefault();
    say("Save the private key first. It is shown only once.");
    saved.focus();
  });
  window.addEventListener("beforeunload", (e) => {
    if (pending) e.preventDefault();
  });
  window.addEventListener("pagehide", clear);
}

const root = document.querySelector("[data-hm-backupkey]");
if (root) init(root);
