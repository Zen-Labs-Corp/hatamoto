---
title: Translate theme strings
nav_title: Theme strings
description: Declare the words your theme prints in theme.json, read them in templates and let the site owner translate them for each locale.
next: template-funcs.md, package-format.md
---

A theme prints some words of its own: a search label, "Next", a navigation name. If they are typed into the templates, the site shows them in one language only. In this guide you move them into theme strings, so the owner can change each one for every locale of the site without editing the theme.

## Declare the strings

Add a `strings` object to `theme.json`. Each key is a name you choose and each value is the default text, in the language the theme is written in.

```json
{
  "name": "my-theme",
  "version": "1.0.0",
  "strings": {
    "read_more": "Read more",
    "next": "Next",
    "n_results": "{n} results"
  }
}
```

A key is a lower-case letter followed by up to 63 lower-case letters, digits or underscores. A text is 1 to 300 characters of plain text: no `<`, `>` or `&`, and no control characters. A theme holds at most 500 strings. A package with a broken `strings` block is refused when it is installed, and `hatamoto theme check` reports it. The full rules are in [Package format](package-format.md#the-manifest).

## Use them in templates

Print a string with `t`, passing the page data first:

```gotemplate
<a class="more" href="{{.URL}}">{{t . "read_more"}}</a>
<p>{{t . "n_results" .Pagination.Total}}</p>
```

`{n}` in a text is replaced by the first argument after the key. No other placeholder is read. The result is plain text and is escaped where it prints, so it is safe in an attribute such as `aria-label`. Put markup around a string in the template, not inside it.

A partial called with `dict` has no page data of its own. Pass the strings in, then use the dict as the first argument:

```gotemplate
{{template "pager" dict "Next" .Pagination.NextURL "Strings" .Strings}}
```

```gotemplate
{{define "pager"}}<a href="{{.Next}}">{{t . "next"}}</a>{{end}}
```

A key that is not declared prints as the key itself, so a typo is easy to spot. Section templates can call `{{t . "key"}}` directly.

## How the owner translates them

The owner opens **Theme > Strings** (`<admin>/theme/strings`, owners only). On a site with more than one locale there is a tab for each locale; pick one. The page lists every string of the active theme with its key, what it is for, the theme's default text and a field for the chosen locale. A blank field means "use the default". Saving writes only the filled fields and clears the page cache.

The text shown on a public page is the first of these that exists:

1. the owner's text for the page's locale;
2. the owner's text for the site's default locale;
3. the theme's own text from `theme.json`.

A theme that extends another theme inherits its strings: a layer's declaration wins over the same key in a layer below it, and the built-in theme is the last layer.

The owner's text is kept per theme and locale. Switching themes does not delete it, and it comes back if the theme is activated again. Keys the active theme no longer declares are ignored.

## What is not translated

Theme strings cover the words in your templates. They do not cover text that does not come from the theme: the page content, the consent banner and its buttons, the words a widget prints, form labels (set in the form) and the script `hm.js`. Each of those has its own setting or is part of the content.

## Related pages

- [Template functions](template-funcs.md#t) for the `t` signature.
- [Layout](layout.md#view-data) for `.Strings` in the view data.
- [Package format](package-format.md#the-manifest) for the `strings` rules.
