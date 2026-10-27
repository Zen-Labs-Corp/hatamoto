// Tiptap mark for internal links (port of extensions/entity-link.ts): the one
// "link" mark of every editor. Entity links store
//   <a href="/blog/seo" data-entity-id="abc" class="entity-link">SEO</a>
// so the slug-rename rewriter (backend/internal/linking/v2) can find them.
// Plain links render exactly like the base Link mark. It extends Link rather
// than registering a second mark: two link marks on one span serialise badly.
import Link from "@tiptap/extension-link";
import { siteLinkAttrs } from "@hm/editor/links";

const stored = (name) => ({
  default: null,
  parseHTML: (el) => el.getAttribute(name),
  renderHTML: (attrs) => (attrs[name] ? { [name]: String(attrs[name]) } : {}),
});

export const EntityLink = Link.extend({
  name: "link",

  addOptions() {
    return {
      ...this.parent?.(),
      // No blanket target/rel (Tiptap's default adds nofollow to every link, internal ones included).
      HTMLAttributes: { target: null, rel: null, class: null },
      siteRel: false, // apply siteLinkAttrs when rendering; off: attributes render as stored
      siteHosts: [],
      entityClass: "entity-link", // class of links carrying data-entity-id; null adds none
    };
  },

  renderHTML(props) {
    const HTMLAttributes = this.options.siteRel ? siteLinkAttrs(props.HTMLAttributes, this.options.siteHosts) : props.HTMLAttributes;
    return this.parent?.({ ...props, HTMLAttributes }) ?? ["a", HTMLAttributes, 0];
  },

  addAttributes() {
    return {
      ...this.parent?.(),
      // Read stored values; never inject a default on load.
      target: { default: null, parseHTML: (el) => el.getAttribute("target") },
      rel: { default: null, parseHTML: (el) => el.getAttribute("rel") },
      "data-entity-id": {
        default: null,
        parseHTML: (el) => el.getAttribute("data-entity-id"),
        renderHTML: (attrs) => {
          const id = attrs["data-entity-id"];
          if (!id) return {};
          const { entityClass } = this.options;
          return { "data-entity-id": String(id), ...(entityClass ? { class: entityClass } : {}) };
        },
      },
      "data-target-slug": stored("data-target-slug"),
      "data-target-schema": stored("data-target-schema"),
    };
  },
});
