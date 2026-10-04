package pongo2

import (
	"strings"
	"testing"
)

// Nested static includes are analyzed from the include site: the same partial
// included in text and in an attribute must receive the matching escaping.
func TestContextualNestedIncludes(t *testing.T) {
	files := map[string]string{
		"inner.html": `{{ v }}`,
		"mid.html":   `[{{ v }}|{% include "inner.html" %}]`,
		"main.html": `<p>{% include "mid.html" %}</p>` +
			`<input value="{% include "inner.html" %}">` +
			`<a href="{% include "inner.html" %}">x</a>` +
			`<script>var s = "{{ v }}{% include "inner.html" %}";</script>`,
	}
	set := newContextualSet(t, files)

	out, err := renderSet(set, "main.html", Context{"v": contextualTestValue})
	if err != nil {
		t.Fatalf("nested includes failed: %v", err)
	}

	want := `<p>[a&#39;&quot;b&lt;c&gt;:d&amp;e|a&#39;&quot;b&lt;c&gt;:d&amp;e]</p>` +
		`<input value="a&#39;&quot;b&lt;c&gt;:d&amp;e">` +
		`<a href="a&#39;&quot;b&lt;c&gt;:d&amp;e">x</a>` +
		`<script>var s = "a\u0027\u0022b\u003Cc\u003E:d\u0026ea\u0027\u0022b\u003Cc\u003E:d\u0026e";</script>`
	if out != want {
		t.Fatalf("nested include contexts mismatch:\n got: %q\nwant: %q", out, want)
	}
}

// Extends composition participates: a child block is analyzed at the context
// where the parent places the block, and parent defaults are analyzed too.
func TestContextualExtendsBlocks(t *testing.T) {
	files := map[string]string{
		"base.html": `<p>{% block text %}default{% endblock %}</p>` +
			`<input value="{% block attr %}x{% endblock %}">` +
			`<script>var s = "{{ v }}{% block js %}jsdefault{% endblock %}";</script>`,
		"child.html": `{% extends "base.html" %}` +
			`{% block text %}{{ v }}{% endblock %}` +
			`{% block attr %}{{ v }}{% endblock %}` +
			`{% block js %}{{ v }}{% endblock %}`,
	}
	set := newContextualSet(t, files)

	out, err := renderSet(set, "child.html", Context{"v": contextualTestValue})
	if err != nil {
		t.Fatalf("extends render failed: %v", err)
	}
	if !strings.Contains(out, "<p>a&#39;&quot;b&lt;c&gt;:d&amp;e</p>") {
		t.Fatalf("text block not escaped in parent context: %q", out)
	}
	if !strings.Contains(out, `value="a&#39;&quot;b&lt;c&gt;:d&amp;e"`) {
		t.Fatalf("attribute block not escaped in parent context: %q", out)
	}
	if !strings.Contains(out, `var s = "a\u0027\u0022b\u003Cc\u003E:d\u0026e`) {
		t.Fatalf("script block not escaped in parent context: %q", out)
	}
}

// A child override that does not converge with the parent block's context is
// rejected instead of silently mis-escaped.
func TestContextualExtendsMismatchRejected(t *testing.T) {
	files := map[string]string{
		"base.html":  `<input value="{% block attr %}x{% endblock %}">`,
		"child.html": `{% extends "base.html" %}{% block attr %}{{ v }}"{% endblock %}`,
	}
	set := newContextualSet(t, files)
	_, err := renderSet(set, "child.html", Context{"v": contextualTestValue})
	if err == nil || !strings.Contains(err.Error(), "do not converge") {
		t.Fatalf("expected non-converging block rejection, got: %v", err)
	}
}

func TestContextualDangerousURL(t *testing.T) {
	safe := []string{
		"/relative/path?x=1",
		"https://example.com/a",
		"mailto:a@example.com",
		"#anchor",
		"?query=1",
		"//example.com/protocol-relative",
		"page.html",
		"tel:+1234",
	}
	dangerous := []string{
		"javascript:alert(1)",
		"  javascript:alert(1)",
		"JaVaScRiPt:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox(1)",
		"java\x00script:alert(1)",
	}

	set := newContextualSet(t, map[string]string{
		"main.html": `<a href="{{ u }}">link</a>`,
	})

	for _, u := range safe {
		out, err := renderSet(set, "main.html", Context{"u": u})
		if err != nil {
			t.Fatalf("safe URL %q rejected: %v", u, err)
		}
		if !strings.Contains(out, `href="`+htmlEscapeReplacer.Replace(u)+`"`) {
			t.Fatalf("safe URL %q rendered incorrectly: %q", u, out)
		}
	}

	for _, u := range dangerous {
		_, err := renderSet(set, "main.html", Context{"u": u})
		if err == nil {
			t.Fatalf("dangerous URL %q accepted", u)
		}
		if !strings.Contains(err.Error(), "contextual escaping") {
			t.Fatalf("dangerous URL %q produced wrong error: %v", u, err)
		}
	}
}

func TestContextualUnsupportedContextsRejected(t *testing.T) {
	cases := map[string]string{
		"dynamic tag name":   `<{{ tag }}>x</>`,
		"dynamic attr name":  `<input {{ name }}="v">`,
		"unquoted attr":      `<input value={{ v }}>`,
		"style attr":         `<input style="{{ v }}">`,
		"event handler":      `<a onclick="{{ v }}">x</a>`,
		"comment":            `<!-- {{ v }} -->`,
		"css":                `<style>{{ v }}</style>`,
		"script code":        `<script>var x = {{ v }};</script>`,
		"script slash":       `<script>var a = 1/2; var s = "{{ v }}";</script>`,
		"dynamic include":    `{% include tpl %}`,
		"ssi":                `{% ssi "x.txt" %}`,
		"filter block":       `{% filter upper %}{{ v }}{% endfilter %}`,
		"trailing backslash": `<script>var s = "\{{ v }}";</script>`,
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			set := newContextualSet(t, map[string]string{"main.html": src, "x.txt": "!"})
			_, err := renderSet(set, "main.html", Context{
				"v":    contextualTestValue,
				"tag":  "div",
				"name": "title",
				"tpl":  "other.html",
			})
			if err == nil {
				t.Fatalf("expected %q to be rejected", name)
			}
			if e, ok := err.(*Error); !ok || e.Sender != "contextual" {
				t.Fatalf("%q: expected contextual compile error with location, got: %v", name, err)
			}
		})
	}
}
