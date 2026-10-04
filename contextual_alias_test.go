package pongo2

import (
	"strings"
	"testing"
)

// The same partial included at two different contexts (even repeatedly, e.g.
// inside a loop) must always be escaped for its include site.
func TestContextualSamePartialDifferentContexts(t *testing.T) {
	files := map[string]string{
		"part.html": `{{ v }}`,
		"main.html": `{% for i in items %}<p>{% include "part.html" %}</p>` +
			`<input value="{% include "part.html" %}">` +
			`<script>var s = "{% include "part.html" %}";</script>{% endfor %}`,
	}
	set := newContextualSet(t, files)
	out, err := renderSet(set, "main.html", Context{
		"items": []string{"a", "b"},
		"v":     contextualTestValue,
	})
	if err != nil {
		t.Fatalf("shared partial failed: %v", err)
	}

	wantText := "<p>a&#39;&quot;b&lt;c&gt;:d&amp;e</p>"
	wantAttr := `<input value="a&#39;&quot;b&lt;c&gt;:d&amp;e">`
	wantJS := `<script>var s = "a\u0027\u0022b\u003Cc\u003E:d\u0026e";</script>`
	if strings.Count(out, wantText) != 2 || strings.Count(out, wantAttr) != 2 || strings.Count(out, wantJS) != 2 {
		t.Fatalf("shared partial escaped inconsistently:\n%s", out)
	}
}
