# Contextual Escaping

By default pongo2 applies one uniform HTML escape to every variable output.
That is correct for element text, but the same value placed in an attribute,
a URL, or a JavaScript string has different breakout rules. Putting one
variable in all four places (as mail backends often do) cannot be made safe
with HTML escaping alone.

Contextual escaping is an **opt-in** `TemplateSet` mode that tracks, at
compile time, the exact place each variable lands in and applies the matching
escape rules. When a place cannot be determined statically the template is
**rejected at compile time** — the engine never guesses.

## Enabling it

```go
set := pongo2.NewSet("mail", loader)
if err := set.SetContextualEscaping(true); err != nil {
    // It must be enabled before the first template is compiled (like
    // BanTag/BanFilter).
    log.Fatal(err)
}
tpl, err := set.FromFile("mail.html")
```

Normal sets keep the existing behavior; `{% autoescape %}`, `|safe|`, and
`SetAutoescape` are unaffected there. Use `MustSetContextualEscaping(true)` if
panic-on-error is acceptable.

## Supported contexts

Only these statically decidable contexts accept variables:

| Context | Example | Escaping applied |
| --- | --- | --- |
| HTML element text | `<p>{{ x }}</p>` | `& < > " '` entity encoding |
| Quoted attribute value | `<input value="{{ x }}">`, `title='{{ x }}'` | same entity encoding |
| Quoted URL attribute | `<a href="{{ x }}">`, `<img src=...>` | entity encoding **plus a runtime protocol allowlist check** |
| Single/double quoted JS string | `<script>var s = "{{ x }}";</script>` | JavaScript `\uXXXX` string escaping |

Tracked across template nodes, tags, and nested templates:

- Quoted attribute and JS-string context survives intervening `{{ ... }}`
  tags (the scanner walks the whole literal token stream, not one tag).
- `if`/`elif`/`else`: every branch is analyzed from the same entry state and
  all reachable branch ends (including a missing `else`) must converge on
  exactly the same context.
- `for`: the loop body runs zero or more times and must return to the context
  it started in; the `empty` clause must converge too.
- Static `{% include "..." %}` participates fully: the included content is
  analyzed from the include site's context (recursively, for nested
  includes), so the same partial gets text/attribute/URL/JS escaping that
  matches each include site.
- `{% extends %}` / `{% block %}`: block overrides and parent defaults are
  analyzed at the position where the block appears in the executed root
  template, and must converge with the parent block's context.

URL attributes whose values are checked: `href`, `src`, `action`,
`formaction`, `cite`, `background`, `longdesc`, `profile`, `usemap`,
`classid`, `codebase`, `manifest`, `poster`, `archive`. Relative,
root-relative (`/x`, `//host/x`), anchor (`#`), query (`?`) and
scheme-relative URLs are always allowed. Absolute URLs must use an allowlisted
protocol (`http`, `https`, `ftp`, `ftps`, `mailto`, `tel`, `news`, `sms`,
`irc`, `git`, `ssh`, `feed`, `xmpp`, `gopher`, `nntp`, `steam`, `magnet`,
`webcal`, `file`, `cid`, `mid`, `prospero`). Notably `javascript:`,
`vbscript:`, and `data:` are rejected, as are control characters and leading
whitespace obfuscation such as `"  javascript:..."`. A rejected URL is a
**runtime error** (the value is data-dependent), located at the variable.

## Explicitly unsupported (compile error)

When the context cannot be proven, compilation fails with an error that points
at the responsible template line rather than emitting potentially unsafe
output:

- Variables outside quotes inside tags: dynamic tag names (`<{{ t }}>`),
  dynamic attribute names (`<input {{ n }}>`), and **unquoted** attribute
  values (`<input value={{ x }}>`).
- Event handler attributes (`on*`) and inline `style="..."` — their value
  language is JavaScript/CSS, not an HTML attribute.
- `<style>` element bodies (CSS context).
- JavaScript other than single/double quoted strings: variables in script
  code, template literals (backticks), comments, or in a `<script>` that
  contains a `/` outside quoted strings (regex/division cannot be
  disambiguated without a JS parser, so the remainder of that script is
  rejected for variables).
- HTML comments and `<! ... >` declarations containing variables.
- A variable directly following a backslash at the end of a literal chunk in
  a JS string (`"...\{{ x }}"`).
- Dynamic includes (`{% include name %}`), including templates that
  themselves use `extends`, the deprecated `{% ssi %}`, and the
  `{% filter %}` block tag (its output structure is not statically known).
- Unknown/custom tags are rejected in this mode unless they only emit static
  or numeric content, because their output context cannot be accounted for.
- Templates ending inside an open tag, attribute value, comment, script, or
  style element.

## About `|safe` and macros

Contextual escaping is strict: **`|safe` does not bypass it**. A value marked
safe is still entity-encoded in text/attributes, JS-escaped in JS strings,
and protocol-checked in URL attributes. Filters that themselves emit a whole
HTML document (such as `json_script`) are not context-aware and should not be
placed inside a foreign context.

A `{% macro %}` body is analyzed as HTML-text-neutral and its return value is
escaped again at the call site, so macros render safely in every supported
context.
