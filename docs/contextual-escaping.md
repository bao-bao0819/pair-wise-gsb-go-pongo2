# Context-Aware Escaping

Ordinary autoescaping applies one HTML escaper to every `{{ variable }}`,
regardless of whether the value ends up in element text, inside an attribute,
in a URL, or in a JavaScript string. The same value needs different escaping
in those positions, and a value marked `safe` for HTML text is not safe inside
`href="..."` or a `<script>` string.

pongo2 therefore offers an **opt-in** context-aware escaping mode (modeled on
the idea behind Go's `html/template`). The default behavior is unchanged; you
enable the mode explicitly per `TemplateSet`.

## Enabling the mode

```go
set := pongo2.NewSet("mail-templates", loader)
set.SetContextualEscaping(true)

tpl, err := set.FromString(`<a href="{{ url }}">{{ label }}</a>`)
```

Call `SetContextualEscaping(true)` **before compiling templates** of the set.
When enabled, compilation runs a static analysis over the template and:

- assigns each output the escaper matching its exact context;
- rejects templates that place output in a context it cannot prove safe;
- protocol-checks URL attribute values at render time.

Compilation errors point at the template position that made the context
undecidable. Prefer fixing the template over working around the error.

## Supported contexts

Only the following, statically decidable output positions are supported:

| Position | Example | Escaping |
| --- | --- | --- |
| HTML element text | `<p>{{ x }}</p>` | HTML text escaping (`& < > " '`) |
| Quoted attribute (double or single) | `<input value="{{ x }}">` / `<a title='{{ x }}'>` | HTML attribute escaping |
| Quoted URL attribute | `<a href="{{ x }}">`, `<img src="{{ x }}">` | protocol check + attribute escaping |
| Quoted JS string in `<script>` | `<script>var a = "{{ x }}";</script>` | JavaScript string escaping |

URL attributes checked for protocol: `action`, `background`, `cite`, `data`,
`formaction`, `href`, `poster`, `src`, `xlink:href`.

Accepted URL values are relative references and the schemes `http`, `https`,
`ftp`, `ftps`, and `mailto`. A value whose scheme is not allowed (notably
`javascript:` and `data:`), regardless of casing or leading whitespace, is
replaced with the harmless marker `#ZgotmplZ`. This check runs even when the
value was passed through `safe`.

Executable JavaScript is recognized for `<script>` with no `type` attribute or
with a static JavaScript MIME type such as `text/javascript` or
`application/javascript`.

## Control structures and composition

The analysis follows template nodes and control flow:

- **`if`/`elif`/`else`, `ifequal`, `ifnotequal`, `ifchanged`**: all branches
  are analyzed from the same context and must merge into the same context.
  Every quote or script string opened in a branch must be closed in it.
- **`for`**: the loop body (and any `empty` branch) must return to its entry
  context. HTML elements started in surrounding text may span iterations, but
  quotes and script strings may not.
- **`include "..."`**: static includes participate in the analysis; the
  included template starts and ends in HTML text and is re-verified at each
  use site. Includes placed inside attributes or other non-text positions are
  rejected. Dynamic include names (`{% include var %}`) are rejected.
- **`extends`/`block`**: the effective (most-derived) block override is
  checked at the block tag's position in the ancestor template, so a child
  block cannot move output from HTML text into an attribute or script string.
- `{% with %}`, `{% filter %}`, `{% spaceless %}`, `{% macro %}` and
  `{% autoescape %}` are traversed normally.

`{% autoescape off %}` and the `safe` filter **do not disable** contextual
escaping. Context safety is positional: a value trusted as HTML is not
automatically trusted as a JavaScript string or a URL.

## What is explicitly rejected

The analyzer never guesses. Compilation fails for output in, or templates
that leave open, any of these positions:

- dynamic element/tag names (`<{{ tag }}>`);
- dynamic attribute names (`<a {{ name }}="...">`);
- unquoted attribute values (`<a title={{ x }}>`);
- attributes (or any quote) left open at a branch join or loop boundary;
- raw CSS, including `<style>` bodies and `style="..."` values;
- `<script>` with a non-JavaScript static `type` (e.g.
  `type="application/json"`) or a dynamic `type`;
- JavaScript outside a quoted single/double string: raw code, comments,
  regular expression literals, and template literals (`` `...` ``), including
  `${...}` interpolation;
- quotes, attributes, or `<script>` strings not closed before the end of the
  template;
- dynamic `{% include %}` names, recursive includes, and the `{% ssi %}` tag;
- custom or unknown tags the built-in analysis cannot see through.

In addition, only static element and attribute names are tracked. Keep
markup around variables conventional (lowercase ASCII tag/attribute names,
quoted values) so the context is unambiguous.

## Comparison with normal autoescaping

- Normal mode: one HTML escaper per output; `safe`/`autoescape off` bypass it;
  URLs are not protocol-checked.
- Contextual mode: position-specific escapers; `safe` is honored for
  nothing at the output position; URL values are protocol-checked; unsupported
  positions are compile-time errors instead of best-effort escaping.

Use contextual mode for templates that embed the same data in several
positions (for example HTML email bodies with tracked links) and want the
engine to enforce the distinction instead of relying on per-value filters.
