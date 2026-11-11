// Core's camera for a plugin page that holds the camera grant. The plugin
// page only asks (the camera.capture bridge op); this runs in the trusted admin
// page: a modal shows a live preview, a person presses Capture, and the plugin
// gets ONE JPEG. The camera is stopped on every way out, never left running.
//
//   capture(dialog) -> {ok:true, image:"data:image/jpeg;base64,...", width, height}
//                    | {ok:false, error:"cancelled"|"denied"|"unavailable"}
export const MAX_SIDE = 1280;
export const MAX_BYTES = 1000000;
const QUALITIES = [0.85, 0.7, 0.55, 0.4];

// fit scales (w, h) down so the longest side is at most max; never up.
export function fit(w, h, max = MAX_SIDE) {
  const k = Math.min(1, max / Math.max(w, h));
  return { width: Math.round(w * k), height: Math.round(h * k) };
}

export function errorCode(err) {
  const n = err?.name;
  return n === "NotAllowedError" || n === "SecurityError" ? "denied" : "unavailable";
}

// encode draws the frame and lowers quality, then size, until the JPEG fits MAX_BYTES.
function encode(video) {
  let { width, height } = fit(video.videoWidth, video.videoHeight);
  for (;;) {
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    canvas.getContext("2d").drawImage(video, 0, 0, width, height);
    let image = "";
    for (const q of QUALITIES) {
      image = canvas.toDataURL("image/jpeg", q);
      if (((image.length - image.indexOf(",") - 1) * 3) / 4 <= MAX_BYTES) return { ok: true, image, width, height };
    }
    if (width <= 16 || height <= 16) return { ok: true, image, width, height };
    width = Math.max(1, Math.round(width * 0.75));
    height = Math.max(1, Math.round(height * 0.75));
  }
}

// A click on Capture counts only this long after the preview goes live, so a
// plugin page cannot land a click the person never meant.
export const CLICK_GUARD_MS = 500;
let busy = false; // one capture at a time, held until its cleanup has run

export async function capture(dlg) {
  if (!navigator.mediaDevices?.getUserMedia || !dlg) return { ok: false, error: "unavailable" };
  if (busy || dlg.open) return { ok: false, error: "cancelled" };
  busy = true;
  const video = dlg.querySelector("[data-hm-camera-video]");
  const status = dlg.querySelector("[data-hm-camera-status]");
  const button = dlg.querySelector("[data-hm-camera-capture]");
  let stream = null;
  let onClick;
  let onClose;
  let onLive;
  let timer;
  const say = (text) => {
    if (!status) return;
    status.textContent = text;
    status.hidden = !text;
  };
  say("");
  if (button) button.disabled = true;
  try {
    // The dialog opens first so the person sees why the browser asks.
    dlg.showModal();
    const done = new Promise((resolve) => {
      onClick = (e) => {
        e.preventDefault(); // the footer form is method=dialog; keep the dialog until the frame is read
        if (video.videoWidth && !button?.disabled) resolve("capture");
      };
      onLive = () => {
        clearTimeout(timer);
        timer = setTimeout(() => {
          if (button) button.disabled = false;
        }, CLICK_GUARD_MS);
      };
      video.addEventListener("loadeddata", onLive);
      onClose = () => resolve("cancelled");
      button?.addEventListener("click", onClick);
      dlg.addEventListener("close", onClose);
    });
    try {
      stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" }, audio: false });
    } catch (err) {
      const error = errorCode(err);
      say(error === "denied" ? "Camera access was blocked. Allow it in the browser to use it here." : "No camera is available.");
      // Keep the dialog open so the person can read why; Capture stays disabled.
      await done;
      return { ok: false, error };
    }
    video.srcObject = stream;
    if (!dlg.open) return { ok: false, error: "cancelled" };
    return (await done) === "capture" ? encode(video) : { ok: false, error: "cancelled" };
  } finally {
    clearTimeout(timer);
    stream?.getTracks().forEach((t) => t.stop());
    if (video) {
      video.srcObject = null;
      video.removeEventListener("loadeddata", onLive);
    }
    if (button) button.disabled = false;
    button?.removeEventListener("click", onClick);
    dlg.removeEventListener("close", onClose);
    if (dlg.open) dlg.close();
    busy = false;
  }
}
