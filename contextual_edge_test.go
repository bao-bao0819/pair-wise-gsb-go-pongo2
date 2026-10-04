package pongo2

import (
	"strings"
	"testing"
)

func TestContextualSingleQuotedAttrAndSelfClose(t *testing.T) {
	set := newContextualSet(t, map[string]string{
		"main.html": `<input value='{{ v }}'/><br/><img src='/x.png' alt='{{ v }}'/>`,
	})
	out, err := renderSet(set, "main.html", Context{"v": contextualTestValue})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !strings.Contains(out, `value='a&#39;&quot;b&lt;c&gt;:d&amp;e'`) {
		t.Fatalf("single-quoted attribute escaping wrong: %q", out)
	}
	if strings.Count(out, `alt='`) != 1 || !strings.Contains(out, "/>") {
		t.Fatalf("self-closing tags mishandled: %q", out)
	}
}

// A macro body is HTML-neutral; when its (safe) return value is inserted into
// a JS string, the outer context must still escape it.
func TestContextualMacroInJS(t *testing.T) {
	set := newContextualSet(t, map[string]string{
		"main.html": `{% macro m(x) %}<b>{{ x }}</b>{% endmacro %}` +
			`<script>var s = "{{ m(v) }}";</script>`,
	})
	out, err := renderSet(set, "main.html", Context{"v": contextualTestValue})
	if err != nil {
		t.Fatalf("macro in js failed: %v", err)
	}
	if strings.Contains(out, "<b>") {
		t.Fatalf("macro output must be escaped in JS string: %q", out)
	}
	if !strings.Contains(out, `\u003Cb\u003E`) {
		t.Fatalf("macro output not JS-escaped: %q", out)
	}
}

// Branches and loops inside an included partial are analyzed from the
// include site and must obey the surrounding context.
func TestContextualIncludeWithBranchAndLoop(t *testing.T) {
	files := map[string]string{
		"partial.html": `{% if x %}{{ v }}{% else %}{{ v }}{% endif %}` +
			`{% for i in items %}<i>{{ i }}</i>{% endfor %}`,
		"main.html": `<input value="{% include "partial.html" %}">`,
	}
	set := newContextualSet(t, files)
	out, err := renderSet(set, "main.html", Context{
		"x":     true,
		"v":     contextualTestValue,
		"items": []string{"p", "q"},
	})
	if err != nil {
		t.Fatalf("include with control flow failed: %v", err)
	}
	if !strings.Contains(out, `value="a&#39;&quot;b&lt;c&gt;:d&amp;e<i>p</i><i>q</i>"`) {
		t.Fatalf("included branches/loops not escaped at include site: %q", out)
	}
}

// Dynamic include names cannot be analyzed and are rejected even when the
// filename would resolve statically at runtime.
func TestContextualDynamicIncludeRejected(t *testing.T) {
	set := newContextualSet(t, map[string]string{
		"main.html":  `{% include name %}`,
		"other.html": `{{ v }}`,
	})
	_, err := renderSet(set, "main.html", Context{"name": "other.html", "v": "x"})
	if err == nil || !strings.Contains(err.Error(), "dynamic") {
		t.Fatalf("dynamic include must be rejected, got: %v", err)
	}
}

// The default (non-contextual) mode keeps its existing uniform behavior.
func TestNormalModeUnchanged(t *testing.T) {
	set := NewSet("normal", &memLoader{files: map[string]string{}})
	tpl, err := set.FromString(`<a href="{{ u }}"><script>var s = "{{ u }}";</script></a>`)
	if err != nil {
		t.Fatalf("normal parse failed: %v", err)
	}
	out, err := tpl.Execute(Context{"u": `a'"b<c>:d&`})
	if err != nil {
		t.Fatalf("normal render failed: %v", err)
	}
	want := `<a href="a&#39;&quot;b&lt;c&gt;:d&amp;"><script>var s = "a&#39;&quot;b&lt;c&gt;:d&amp;";</script></a>`
	if out != want {
		t.Fatalf("normal mode behavior changed:\n got: %q\nwant: %q", out, want)
	}
}
