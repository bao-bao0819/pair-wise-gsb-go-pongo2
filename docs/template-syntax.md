# Template Syntax

pongo2 uses Django-compatible template syntax. This document covers all syntax elements.

## Variables

Variables output values from the context:

```django
{{ variable }}
```

### Accessing Attributes

Access struct fields, map keys, and slice/array indices:

```django
{{ user.name }}          {# Struct field or map key #}
{{ user.Name }}          {# Go struct fields are case-sensitive #}
{{ items.0 }}            {# First element of slice/array #}
{{ dict.key }}           {# Map key access #}
{{ user["name"] }}       {# Subscript notation #}
{{ items[index] }}       {# Dynamic index #}
```

### Strict Undefined Mode

By default a missing variable, map key or struct field renders as an empty
string (Django/lenient behavior). Enabling strict undefined mode makes such
accesses produce a special *undefined* value instead:

```go
set := pongo2.NewSet("strict", loader)
set.SetStrictUndefined(true)
// or per template: tpl.Options.StrictUndefined = true
```

The undefined value records the full access path. Simply chaining more
attribute/key accesses only extends the path and never fails; an error is
raised only when the value is actually *consumed*:

- template output (`{{ ... }}`)
- arithmetic (`+`, `-`, `*`, `/`, `%`, `^`, unary `-`)
- comparisons (`==`, `!=`, `<`, etc.) and boolean tests (`if`, `not`)
- iteration (`{% for %}`)
- concrete function/macro/filter arguments

Error messages pinpoint where the chain broke and the path used, so a missing
intermediate value is distinguishable from a missing leaf:

```text
map key 'user.profile' does not exist (accessed as user.profile.name)
map key 'user.profile.name' does not exist
```

Explicit `nil`, `false`, `0` and empty strings remain defined values. Handle
an undefined value intentionally with the [`default` filter](filters.md), the
[`defined` operator](#testing-for-definition-the-defined-operator), or by
accepting a `*pongo2.Value` in a host function and testing `v.IsUndefined()` /
`v.UndefinedPath()`. Short-circuiting `and`/`or` never evaluates the skipped
branch, and a genuine error returned by a host function is never turned into
an undefined value.

### Calling Methods

Call methods on objects:

```django
{{ user.GetFullName() }}
{{ items.Len() }}
{{ now.Format("2006-01-02") }}
```

Methods can receive arguments:

```django
{{ math.Add(1, 2) }}
{{ string.Replace("hello", "h", "j") }}
```

**Method requirements:**
- Must be exported (capitalized)
- Must return 1 or 2 values
- If 2 values, second must be `error`
- Can accept `*pongo2.Value` or concrete types
- Can optionally accept `*pongo2.ExecutionContext` as first parameter

### Filters

Filters modify variable output:

```django
{{ name|upper }}                    {# JOHN #}
{{ name|lower }}                    {# john #}
{{ text|truncatewords:5 }}          {# First five words... #}
{{ price|floatformat:2 }}           {# 19.99 #}
{{ list|join:", " }}                {# apple, banana, cherry #}
```

Chain multiple filters:

```django
{{ name|lower|capfirst }}           {# John #}
{{ html|striptags|truncatechars:100 }}
```

## Expressions

pongo2 supports C-like expressions in templates.

### Arithmetic Operators

```django
{{ 1 + 2 }}                         {# 3 #}
{{ 10 - 3 }}                        {# 7 #}
{{ 4 * 5 }}                         {# 20 #}
{{ 20 / 4 }}                        {# 5 #}
{{ 17 % 5 }}                        {# 2 (modulo) #}
{{ 2 ^ 10 }}                        {# 1024 (power) #}
```

### Comparison Operators

```django
{{ x == y }}
{{ x != y }}
{{ x < y }}
{{ x > y }}
{{ x <= y }}
{{ x >= y }}
```

Also supports `<>` as alternative to `!=`.

### Logical Operators

```django
{{ x and y }}
{{ x or y }}
{{ not x }}
{{ x && y }}                        {# Alternative syntax #}
{{ x || y }}                        {# Alternative syntax #}
{{ !x }}                            {# Alternative syntax #}
```

### The `in` Operator

Check if a value is contained in another:

```django
{% if "a" in "abc" %}yes{% endif %}             {# String contains #}
{% if key in dict %}yes{% endif %}              {# Map contains key #}
{% if item in list %}yes{% endif %}             {# Slice contains #}
{% if !(key in dict) %}not found{% endif %}     {# Negation #}
```

### Testing for Definition: the `defined` Operator

Use `defined` to check whether a value exists without consuming it. This is
mainly useful together with [strict undefined mode](#strict-undefined-mode),
but works in normal mode too. Every value that is actually present — including
explicit `nil`, `false`, `0` and `""` — counts as defined; only a missing root
variable, map key or struct field is undefined.

```django
{% if defined user.profile.name %}{{ user.profile.name }}{% endif %}
{% if user.profile.name is defined %}...{% endif %}
{% if user.profile.name is not defined %}...{% endif %}
{% if not defined user.profile.name %}...{% endif %}
```

`defined` is evaluated without descending into the access chain, so a missing
intermediate value is reported as "not defined" instead of raising an error.
A bare `{{ defined }}` still resolves as an ordinary variable named `defined`
for backwards compatibility; the operator form always has an operand.

### Grouping with Parentheses

```django
{{ (1 + 2) * 3 }}                   {# 9 #}
{% if (a or b) and c %}...{% endif %}
```

### Array Literals

Define inline arrays:

```django
{% for item in [1, 2, 3] %}{{ item }}{% endfor %}
{% for name in ["Alice", "Bob", "Charlie"] %}...{% endfor %}
```

## Tags

Tags control template logic and are enclosed in `{% %}`:

```django
{% if condition %}
  Content shown if true
{% endif %}
```

### Block Tags

Many tags have opening and closing parts:

```django
{% if condition %}
  ...
{% elif other_condition %}
  ...
{% else %}
  ...
{% endif %}

{% for item in items %}
  ...
{% empty %}
  No items found.
{% endfor %}
```

## Comments

### Single-line Comments

```django
{# This is a comment and won't appear in output #}
```

### Multi-line Comments

```django
{% comment %}
  This entire block is a comment.
  It can span multiple lines.
  Useful for temporarily disabling template code.
{% endcomment %}
```

## Whitespace Control

### TrimBlocks and LStripBlocks

Control whitespace around block tags:

```go
set := pongo2.NewSet("name", pongo2.DefaultLoader)
set.Options.TrimBlocks = true   // Remove first newline after block tags
set.Options.LStripBlocks = true // Strip leading whitespace from block tags
```

### Manual Whitespace Control

Use `-` to trim whitespace:

```django
{%- if condition -%}   {# Trims whitespace on both sides #}
  content
{%- endif -%}

{{- variable -}}        {# Trims whitespace around variable #}
```

### Spaceless Tag

Remove whitespace between HTML tags:

```django
{% spaceless %}
  <ul>
    <li>Item 1</li>
    <li>Item 2</li>
  </ul>
{% endspaceless %}
```

Output: `<ul><li>Item 1</li><li>Item 2</li></ul>`

## Escaping

### Automatic HTML Escaping

By default, pongo2 escapes HTML special characters:

```django
{{ "<script>alert('xss')</script>" }}
{# Output: &lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt; #}
```

### Disabling Escaping

```django
{# For a single variable #}
{{ trusted_html|safe }}

{# For a block #}
{% autoescape off %}
  {{ raw_html }}
{% endautoescape %}
```

### JavaScript Escaping

For JavaScript contexts:

```django
<script>
var data = "{{ user_input|escapejs }}";
</script>
```

## Literals

### Strings

```django
{{ "hello world" }}
{{ 'single quotes work too' }}
```

### Numbers

```django
{{ 42 }}
{{ 3.14 }}
{{ -17 }}
{{ -2.5 }}
```

### Booleans

```django
{{ true }}
{{ false }}
```

## Differences from Django

### forloop Variables

In pongo2, forloop variables are capitalized:

```django
{% for item in items %}
  {{ forloop.Counter }}      {# 1-indexed counter #}
  {{ forloop.Counter0 }}     {# 0-indexed counter #}
  {{ forloop.Revcounter }}   {# Reverse counter #}
  {{ forloop.Revcounter0 }}  {# Reverse 0-indexed #}
  {{ forloop.First }}        {# True on first iteration #}
  {{ forloop.Last }}         {# True on last iteration #}
  {{ forloop.Parentloop }}   {# Access parent loop #}
{% endfor %}
```

### Date/Time Format

pongo2 uses Go's time format, not Python's:

```django
{{ now|date:"2006-01-02" }}
{{ now|time:"15:04:05" }}
{{ now|date:"Monday, January 2, 2006" }}
```

Reference: https://golang.org/pkg/time/#Time.Format

### stringformat Filter

Uses Go's `fmt.Sprintf` format:

```django
{{ 3.14159|stringformat:"%.2f" }}   {# 3.14 #}
{{ 42|stringformat:"%05d" }}         {# 00042 #}
```

## Reserved Keywords

The following are reserved keywords:

- `in`
- `and`
- `or`
- `not`
- `true`
- `false`
- `as`
- `export`
