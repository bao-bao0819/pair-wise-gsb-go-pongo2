package pongo2

import (
	"strings"
	"testing"
)

func TestContextualIfequalConvergence(t *testing.T) {
	set := newContextualSet(t, map[string]string{
		"main.html": `<p>{% ifequal a b %}{{ v }}{% else %}{{ v }}{% endifequal %}</p>` +
			`<input title="{% ifequal a b %}a{{ v }}b{% else %}c{% endifequal %}">`,
	})

	// Non-converging attribute branch (else leaves the quote open).
	bad := newContextualSet(t, map[string]string{
		"main.html": `<input title="{% ifequal a b %}{{ v }}{% else %}{{ v }}"{% endifequal %}>`,
	})
	if _, err := renderSet(bad, "main.html", Context{"a": 1, "b": 2, "v": contextualTestValue}); err == nil ||
		!strings.Contains(err.Error(), "do not converge") {
		t.Fatalf("expected ifequal convergence error, got: %v", err)
	}

	out, err := renderSet(set, "main.html", Context{"a": 1, "b": 1, "v": contextualTestValue})
	if err != nil {
		t.Fatalf("ifequal render failed: %v", err)
	}
	if !strings.Contains(out, "<p>a&#39;") || !strings.Contains(out, `title="aa&#39;`) {
		t.Fatalf("ifequal branches wrong: %q", out)
	}
}

func TestContextualIfchangedConvergence(t *testing.T) {
	set := newContextualSet(t, map[string]string{
		"main.html": `{% for i in items %}<p>{% ifchanged i %}{{ i }}{% endifchanged %}</p>{% endfor %}`,
	})
	out, err := renderSet(set, "main.html", Context{"items": []string{"a", "a", contextualTestValue}})
	if err != nil {
		t.Fatalf("ifchanged render failed: %v", err)
	}
	if !strings.Contains(out, "<p>a</p><p></p><p>a&#39;&quot;b&lt;c&gt;") {
		t.Fatalf("ifchanged output wrong: %q", out)
	}
}
