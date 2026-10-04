package pongo2

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// memoryLoader serves named in-memory templates, used for include/extends
// analysis tests without touching the file system.
type memoryLoader struct {
	templates map[string]string
}

func (l *memoryLoader) Abs(base, name string) string { return name }

func (l *memoryLoader) Get(path string) (io.Reader, error) {
	if src, ok := l.templates[path]; ok {
		return strings.NewReader(src), nil
	}
	return nil, errors.New("memory template not found: " + path)
}

func newContextualSet(t *testing.T, templates map[string]string) *TemplateSet {
	t.Helper()
	set := NewSet("contextual-test", &memoryLoader{templates: templates})
	set.SetContextualEscaping(true)
	return set
}

func TestContextualEscapingFourContexts(t *testing.T) {
	set := newContextualSet(t, nil)

	const v = `a"b'c<d>e:f`
	const src = `<p>{{ x }}</p>` +
		`<a title="{{ x }}">t</a>` +
		`<a href="/search?q={{ x }}">u</a>` +
		`<script>var s = "{{ x }}"; var t = '{{ x }}';</script>`

	tpl, err := set.FromString(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tpl.Execute(Context{"x": v})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	const want = `<p>a&quot;b&#39;c&lt;d&gt;e:f</p>` +
		`<a title="a&quot;b&#39;c&lt;d&gt;e:f">t</a>` +
		`<a href="/search?q=a&quot;b&#39;c&lt;d&gt;e:f">u</a>` +
		`<script>var s = "a\u0022b\u0027c\u003Cd\u003Ee:f"; var t = 'a\u0022b\u0027c\u003Cd\u003Ee:f';</script>`

	if out != want {
		t.Fatalf("escaping mismatch:\n got: %q\nwant: %q", out, want)
	}
}

func TestContextualEscapingCrossNodeAttribute(t *testing.T) {
	set := newContextualSet(t, nil)

	// The opening quote and the variable are separated by other template
	// nodes (comment + static HTML + if), so context must be tracked across
	// nodes, not within a single HTML token.
	const src = `{# leading comment #}` +
		`<a title="prefix-` +
		`{% if show %}{{ x }}{% else %}none{% endif %}` +
		`-suffix">x</a>`

	tpl, err := set.FromString(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tpl.Execute(Context{"x": `q"<r`, "show": true})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	const want = `<a title="prefix-q&quot;&lt;r-suffix">x</a>`
	if out != want {
		t.Fatalf("cross-node attribute:\n got: %q\nwant: %q", out, want)
	}
}

func TestContextualEscapingBranchMismatch(t *testing.T) {
	set := newContextualSet(t, nil)

	// One branch leaves an attribute open, the other closes it: merge state
	// differs and compilation must fail at the offending template position.
	const src = `<a title="{% if c %}{{ x }}{% else %}{{ x }}"{% endif %}>y</a>`

	_, err := set.FromString(src)
	if err == nil {
		t.Fatal("expected a branch-context merge error, got nil")
	}
	if !strings.Contains(err.Error(), "conditional branches end in different contexts") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestContextualEscapingLoopMustReturnToEntry(t *testing.T) {
	set := newContextualSet(t, nil)

	const src = `<a title="{% for i in items %}{{ i }}{% endfor %}">y</a>`
	_, err := set.FromString(src)
	if err == nil {
		t.Fatal("expected a loop context error, got nil")
	}
	if !strings.Contains(err.Error(), "loop body opens a context") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestContextualEscapingLoopBalanced(t *testing.T) {
	set := newContextualSet(t, nil)

	const src = `{% for i in items %}<li title="{{ i }}">{{ i }}</li>{% endfor %}`
	tpl, err := set.FromString(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tpl.Execute(Context{"items": []string{`a<b`, `c>d`}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	const want = `<li title="a&lt;b">a&lt;b</li><li title="c&gt;d">c&gt;d</li>`
	if out != want {
		t.Fatalf("balanced loop:\n got: %q\nwant: %q", out, want)
	}
}

func TestContextualEscapingNestedInclude(t *testing.T) {
	templates := map[string]string{
		"inner.html":  `<li title="{{ x }}">{{ x }}</li>`,
		"middle.html": `{% include "inner.html" %}`,
		"outer.html":  `<ul>{% include "middle.html" %}</ul>`,
	}
	set := newContextualSet(t, templates)

	tpl, err := set.FromFile("outer.html")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tpl.Execute(Context{"x": `a"b<c`})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	const want = `<ul><li title="a&quot;b&lt;c">a&quot;b&lt;c</li></ul>`
	if out != want {
		t.Fatalf("nested include:\n got: %q\nwant: %q", out, want)
	}
}

func TestContextualEscapingIncludeInAttributeRejected(t *testing.T) {
	templates := map[string]string{
		"partial.html": `{{ x }}`,
		"main.html":    `<a title="{% include "partial.html" %}">y</a>`,
	}
	set := newContextualSet(t, templates)

	_, err := set.FromFile("main.html")
	if err == nil {
		t.Fatal("expected include-in-attribute rejection, got nil")
	}
	if !strings.Contains(err.Error(), "include") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestContextualEscapingDynamicIncludeRejected(t *testing.T) {
	set := newContextualSet(t, map[string]string{})

	_, err := set.FromString(`{% include name %}`)
	if err == nil {
		t.Fatal("expected dynamic include rejection, got nil")
	}
	if !strings.Contains(err.Error(), "static") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestContextualEscapingURLProtocol(t *testing.T) {
	set := newContextualSet(t, nil)
	tpl := mustCompile(set, `<a href="{{ u }}">x</a>`)

	cases := []struct {
		in, want string
	}{
		{"https://example.com/a?b=c&d=e", `href="https://example.com/a?b=c&amp;d=e"`},
		{"/relative/path", `href="/relative/path"`},
		{"mailto:a@example.com", `href="mailto:a@example.com"`},
		{"javascript:alert(1)", `href="#ZgotmplZ"`},
		{"  JaVaScRiPt:alert(1)", `href="#ZgotmplZ"`},
		{"vbscript:msgbox(1)", `href="#ZgotmplZ"`},
		{"data:text/html,<script>x</script>", `href="#ZgotmplZ"`},
		{`a"b'c<d>e:f`, `href="a&quot;b&#39;c&lt;d&gt;e:f"`},
	}

	for _, tc := range cases {
		out, err := tpl.Execute(Context{"u": tc.in})
		if err != nil {
			t.Fatalf("execute %q: %v", tc.in, err)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("url %q:\n got  %q\nwant ...%s...", tc.in, out, tc.want)
		}
	}
}

func TestContextualEscapingSafeMarkerDoesNotBypass(t *testing.T) {
	set := newContextualSet(t, nil)

	tpl := mustCompile(set, `{{ x|safe }}<a title="{{ x|safe }}"></a><script>var s="{{ x|safe }}";</script>`)
	out, err := tpl.Execute(Context{"x": `<img src=x onerror=alert(1)>`})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out, "<img") {
		t.Fatalf("safe marker bypassed contextual escaping: %q", out)
	}
}

func TestContextualEscapingUnsupportedConstructs(t *testing.T) {
	set := newContextualSet(t, nil)

	cases := []struct {
		name, src, fragment string
	}{
		{"dynamic tag name", `<{{ x }}>a`, ""},
		{"dynamic attr name", `<a {{ x }}="v">a`, ""},
		{"unquoted attr", `<a title={{ x }}>a`, ""},
		{"unclosed attr quote", `<a title="{{ x }}>a`, ""},
		{"css content", `<style>.a{color:red}{{ x }}</style>`, ""},
		{"css style attribute", `<p style="color:red{{ x }}">p</p>`, ""},
		{"js code", `<script>var a = {{ x }};</script>`, ""},
		{"unclosed js string", `<script>var a = "{{ x }}"; var b = "oops;`, ""},
		{"js template literal", "<script>var a = `{{ x }}`;</script>", ""},
		{"non-js script", `<script type="application/json">{{ x }}</script>`, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := set.FromString(tc.src)
			if err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
			if !strings.Contains(err.Error(), "contextual escaping") {
				t.Fatalf("unexpected error for %s: %v", tc.name, err)
			}
		})
	}
}

func TestContextualEscapingExtendsBlocks(t *testing.T) {
	templates := map[string]string{
		"base.html": `<html><body><a title="{% block a %}default{% endblock %}">` +
			`{% block b %}<p>default</p>{% endblock %}</a></body></html>`,
		"child.html": `{% extends "base.html" %}` +
			`{% block a %}<script>var a="{{ x }}";{% endblock %}` +
			`{% block b %}<b>{{ x }}</b>{% endblock %}`,
	}
	set := newContextualSet(t, templates)

	// The child's block a override opens a <script> JS string at a position
	// where the parent is inside an HTML attribute: contexts do not match.
	_, err := set.FromFile("child.html")
	if err == nil {
		t.Fatal("expected block-context mismatch rejection for block a")
	}
	if !strings.Contains(err.Error(), "contextual escaping") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestContextualEscapingExtendsMatching(t *testing.T) {
	templates := map[string]string{
		"base.html":  `<html>{% block content %}<p>{{ x }}</p>{% endblock %}</html>`,
		"child.html": `{% extends "base.html" %}{% block content %}<b>{{ x }}</b>{% endblock %}`,
	}
	set := newContextualSet(t, templates)

	tpl, err := set.FromFile("child.html")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tpl.Execute(Context{"x": `<b>`})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	const want = `<html><b>&lt;b&gt;</b></html>`
	if out != want {
		t.Fatalf("extends:\n got: %q\nwant: %q", out, want)
	}
}

func TestContextualEscapingNormalModeUnchanged(t *testing.T) {
	set := NewSet("normal", &DummyLoader{})

	tpl, err := set.FromString(`<a href="{{ x }}">{{ x|safe }}</a><script>var s="{{ x }}";</script>`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tpl.Execute(Context{"x": "javascript:alert(1)"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Normal mode: no protocol checks, safe bypasses escaping, JS string gets
	// plain HTML escape only.
	if !strings.Contains(out, `href="javascript:alert(1)"`) {
		t.Fatalf("normal mode changed href behavior: %q", out)
	}
	if !strings.Contains(out, `javascript:alert(1)</a>`) {
		t.Fatalf("normal mode changed safe behavior: %q", out)
	}
}

func TestContextualEscapingSingleQuotedAndScriptResume(t *testing.T) {
	set := newContextualSet(t, nil)

	const src = `<a title='{{ x }}' href='{{ u }}'>l</a>` +
		`<script>var a='{{ x }}';</script>` +
		`<p>{{ x }}</p>`
	tpl, err := set.FromString(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tpl.Execute(Context{"x": `a'b"c`, "u": "javascript:x"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	const want = `<a title='a&#39;b&quot;c' href='#ZgotmplZ'>l</a>` +
		`<script>var a='a\u0027b\u0022c';</script>` +
		`<p>a&#39;b&quot;c</p>`
	if out != want {
		t.Fatalf("single quote/resume:\n got: %q\nwant: %q", out, want)
	}
}

func TestContextualEscapingElementAcrossIterations(t *testing.T) {
	set := newContextualSet(t, nil)

	// An element opened in the static prefix is continued inside the loop;
	// only quotes/script strings must close per iteration, tags may continue.
	const src = `<ul>{% for i in items %}<li data-v="{{ i }}">{{ i }}</li>{% endfor %}</ul>`
	tpl, err := set.FromString(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tpl.Execute(Context{"items": []string{"1", "2"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	const want = `<ul><li data-v="1">1</li><li data-v="2">2</li></ul>`
	if out != want {
		t.Fatalf("cross-iteration element:\n got: %q\nwant: %q", out, want)
	}
}

func TestContextualEscapingJSEscapeAtChunkBoundary(t *testing.T) {
	set := newContextualSet(t, nil)

	// The backslash at the chunk boundary escapes the quote in the next
	// chunk; the string therefore stays open through the first hole, then
	// really closes, leaving the second hole in JS code (rejected). Without
	// boundary-escape tracking the escaped quote would be mistaken for the
	// terminator and the first hole would be misclassified.
	const src = `<script>var a = "\{{ x }}\";{{ y }}</script>`
	_, err := set.FromString(src)
	if err == nil {
		t.Fatal("expected rejection for variable in JS code")
	}

	// A normal string ending right after a variable still works.
	const good = `<script>var a = "{{ x }}"; var b = 1;</script>`
	tpl, err := set.FromString(good)
	if err != nil {
		t.Fatalf("compile good: %v", err)
	}
	if _, err := tpl.Execute(Context{"x": "z"}); err != nil {
		t.Fatalf("execute good: %v", err)
	}
}

func TestContextualEscapingAutoescapeOffDoesNotBypass(t *testing.T) {
	set := newContextualSet(t, nil)

	tpl, err := set.FromString(`{% autoescape off %}{{ x }}{% endautoescape %}`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tpl.Execute(Context{"x": `<b>`})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != `&lt;b&gt;` {
		t.Fatalf("autoescape off bypassed contextual escaping: %q", out)
	}
}

func TestContextualEscapingFirstofAndCycle(t *testing.T) {
	set := newContextualSet(t, nil)

	tpl, err := set.FromString(`<p>{% firstof a b %}</p>` +
		`<a title="{% cycle x y %}">z</a>`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tpl.Execute(Context{"a": `<a>`, "b": "b", "x": `q"r`, "y": "s"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "<p>&lt;a&gt;</p>") {
		t.Fatalf("firstof not escaped: %q", out)
	}
	if !strings.Contains(out, `title="q&quot;r"`) {
		t.Fatalf("cycle not escaped in attribute: %q", out)
	}
}

func TestContextualEscapingScriptTypeAttributeOrder(t *testing.T) {
	set := newContextualSet(t, nil)

	// An attribute after a non-type quoted attribute must not inherit the
	// captured type; this script is executable JS.
	tpl, err := set.FromString(`<script data-x="module" type="text/javascript">var a="{{ x }}";</script>`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out, err := tpl.Execute(Context{"x": `a"b`})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `a\u0022b`) {
		t.Fatalf("JS string not escaped: %q", out)
	}

	// Quoted non-JS type still rejects variables.
	if _, err := set.FromString(`<script type="application/ld+json">{{ x }}</script>`); err == nil {
		t.Fatal("expected rejection for non-JS script type")
	}
}

func TestContextualEscapingErrorPosition(t *testing.T) {
	templates := map[string]string{
		// Variable sits in JS code on line 3 of the included template.
		"partial.html": "<div>\n<span>\n<script>var x = {{ bad }};</script>\n</span>\n</div>",
		"main.html":    "{% include \"partial.html\" %}",
	}
	set := newContextualSet(t, templates)

	_, err := set.FromFile("main.html")
	if err == nil {
		t.Fatal("expected a contextual error")
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if e.Line != 3 {
		t.Fatalf("expected error line 3, got line %d (err=%v)", e.Line, err)
	}
	if !strings.Contains(e.Error(), "JavaScript code outside a quoted string") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func mustCompile(set *TemplateSet, src string) *Template {
	tpl, err := set.FromString(src)
	if err != nil {
		panic(err)
	}
	return tpl
}
