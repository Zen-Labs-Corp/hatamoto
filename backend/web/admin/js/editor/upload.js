// Images pasted or dropped into the text are uploaded to the media library
// (POST <admin>/media/upload?json=1, the same service the library uses) with a
// progress bar where the image will go; then the picture is inserted and its
// description field takes focus (alt text prompt).
import { adminURL } from "@hm/adminpath";
import { Extension } from "@tiptap/core";
import { Plugin, PluginKey } from "@tiptap/pm/state";
import { Decoration, DecorationSet } from "@tiptap/pm/view";

const uploadURL = () => adminURL("/media/upload?json=1");

/** The image files of a clipboard or drop. */
export function imageFiles(dataTransfer) {
  return Array.from(dataTransfer?.files ?? []).filter((f) => /^image\/(png|jpe?g|gif|webp|avif)$/i.test(f.type));
}

/**
 * Uploads one file with progress. Resolves {url, name}; rejects with an Error
 * whose message is a sentence for the writer. xhr and csrf are injectable.
 */
export function uploadImage(file, { csrf = "", onProgress = () => {}, url = uploadURL(), XHR = globalThis.XMLHttpRequest } = {}) {
  return new Promise((resolve, reject) => {
    const x = new XHR();
    x.open("POST", url);
    x.setRequestHeader("X-CSRF-Token", csrf);
    x.setRequestHeader("Accept", "application/json");
    x.upload.addEventListener("progress", (e) => e.lengthComputable && onProgress(Math.round((e.loaded / e.total) * 100)));
    x.addEventListener("error", () => reject(new Error(`${file.name}: the upload failed. Check your connection and try again.`)));
    x.addEventListener("abort", () => reject(new Error(`${file.name}: the upload was cancelled.`)));
    x.addEventListener("load", () => {
      let body = null;
      try {
        body = JSON.parse(x.responseText);
      } catch {
        /* not JSON: a proxy or an expired session */
      }
      const first = body?.files?.[0];
      if (x.status >= 200 && x.status < 300 && first?.url) resolve({ url: first.url, name: first.name || file.name });
      else reject(new Error(body?.errors?.[0] || `${file.name}: the upload failed (${x.status}). Sign in again if your session ended.`));
    });
    const form = new FormData();
    form.append("file", file, file.name);
    x.send(form);
  });
}

const key = new PluginKey("hmUpload");
let seq = 0;

/** options: {csrf: () => token, onError(message), onBusy(n)}. */
export const ImageUpload = Extension.create({
  name: "imageUpload",
  addOptions: () => ({ csrf: () => "", onError: () => {}, onBusy: () => {}, uploader: uploadImage }),
  addProseMirrorPlugins() {
    const editor = this.editor;
    const opts = this.options;
    const bars = new Map(); // id -> progress element
    let busy = 0;
    const setBusy = (d) => opts.onBusy((busy += d));

    const placeholder = (id, name) => {
      const wrap = document.createElement("div");
      wrap.className = "hm-be__upload";
      wrap.setAttribute("contenteditable", "false");
      const bar = document.createElement("progress");
      bar.max = 100;
      bar.value = 0;
      bar.setAttribute("aria-label", `Uploading ${name}`);
      const text = document.createElement("span");
      text.className = "hm-muted";
      text.textContent = `Uploading ${name}…`;
      wrap.append(text, bar);
      bars.set(id, bar);
      return wrap;
    };
    const find = (state, id) => key.getState(state)?.find(null, null, (s) => s.id === id)[0]?.from ?? null;

    function start(view, files, pos) {
      for (const file of files) {
        const id = `up-${++seq}`;
        view.dispatch(view.state.tr.setMeta(key, { add: { id, pos, name: file.name } }));
        setBusy(1);
        opts
          .uploader(file, { csrf: opts.csrf(), onProgress: (n) => (bars.get(id).value = n) })
          .then(({ url, name }) => {
            const at = find(view.state, id);
            bars.delete(id);
            if (at === null) return; // the placeholder was deleted: the writer moved on
            const tr = view.state.tr.setMeta(key, { remove: id });
            const node = view.state.schema.nodes.image.create({ src: url, alt: "", caption: "", title: null });
            view.dispatch(tr.insert(at, node));
            // alt text prompt: the description field of the new picture
            const focusAlt = () => Array.from(view.dom.querySelectorAll(".hm-fig")).find((f) => f.querySelector("img")?.getAttribute("src") === url)?.querySelector(".hm-fig__alt")?.focus();
            focusAlt();
            setTimeout(focusAlt, 50); // the view may re-take focus while it settles
          })
          .catch((err) => {
            bars.delete(id);
            view.dispatch(view.state.tr.setMeta(key, { remove: id }));
            opts.onError(err.message);
          })
          .finally(() => setBusy(-1));
      }
    }

    return [
      new Plugin({
        key,
        state: {
          init: () => DecorationSet.empty,
          apply(tr, set) {
            set = set.map(tr.mapping, tr.doc);
            const m = tr.getMeta(key);
            if (m?.add) set = set.add(tr.doc, [Decoration.widget(m.add.pos, () => placeholder(m.add.id, m.add.name), { id: m.add.id, side: -1 })]);
            if (m?.remove) set = set.remove(set.find(null, null, (s) => s.id === m.remove));
            return set;
          },
        },
        props: {
          decorations: (state) => key.getState(state),
          handlePaste(view, e) {
            const files = imageFiles(e.clipboardData);
            // Word and Docs put a picture file next to their html: the html wins when it has text.
            if (files.length === 0 || (e.clipboardData.getData("text/html") && e.clipboardData.getData("text/plain").trim())) return false;
            e.preventDefault();
            start(view, files, view.state.selection.from);
            return true;
          },
          handleDrop(view, e) {
            const files = imageFiles(e.dataTransfer);
            if (files.length === 0) return false;
            e.preventDefault();
            const at = view.posAtCoords({ left: e.clientX, top: e.clientY });
            start(view, files, at?.pos ?? view.state.selection.from);
            return true;
          },
        },
      }),
    ];
  },
  addCommands() {
    return {};
  },
});

/** Starts uploads of files at the caret (the file chooser of "Upload image"). */
export function uploadFilesAt(editor, files) {
  const view = editor.view;
  const e = new Event("paste", { cancelable: true });
  e.clipboardData = { files, getData: () => "" };
  view.someProp("handlePaste", (f) => f(view, e));
}
