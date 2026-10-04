package pongo2

import (
	"strings"
	"testing"
)

// A quoted attribute may span multiple literal HTML tokens because the
// variable tag splits the token stream; the scanner must keep the attribute
// context across those node boundaries.
func TestContextualAttributeAcrossNodes(t *testing.T) {
	set := newContextualSet(t, map[string]string{
		"main.html": `<input title="Hello {{ v }} world {{ v }}" ` +
			`data-x="{{ v }}" onclick="x={{ v }}">`,
	})

	// onclick (event handler) is not supported even when quoted.
	_, err := renderSet(set, "main.html", Context{"v": contextualTestValue})
	if err == nil || !strings.Contains(err.Error(), "event handler") {
		t.Fatalf("expected event-handler rejection, got: %v", err)
	}

	set = newContextualSet(t, map[string]string{
		"main.html": `<input title="Hello {{ v }} world {{ v }}" data-x="{{ v }}">`,
	})
	out, err := renderSet(set, "main.html", Context{"v": contextualTestValue})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !strings.Contains(out, `title="Hello a&#39;&quot;b&lt;c&gt;:d&amp;e world`) {
		t.Fatalf("attribute context not tracked across nodes: %q", out)
	}
	if !strings.Contains(out, `data-x="a&#39;`) {
		t.Fatalf("data-* attribute should use attribute escaping: %q", out)
	}
}

// The two if branches must leave the parser in the same context. One branch
// leaving an attribute quote open is a compile error at the conditional
// position rather than guessed output.
func TestContextualIfBranchesMustConverge(t *testing.T) {
	set := newContextualSet(t, map[string]string{
		"main.html": `<input title="{% if x %}{{ v }}{% else %}{{ v }}"{% endif %}>`,
	})
	_, err := renderSet(set, "main.html", Context{"x": true, "v": contextualTestValue})
	if err == nil {
		t.Fatalf("expected branch convergence error")
	}
	if !strings.Contains(err.Error(), "do not converge") {
		t.Fatalf("expected convergence error pointing at the branch, got: %v", err)
	}
	if err, ok := err.(*Error); !ok || err.Sender != "contextual" || err.Line == 0 {
		t.Fatalf("error must point to the template position, got: %v", err)
	}
}

// Balanced branches that both return to the entry context are accepted.
func TestContextualIfBalancedBranches(t *testing.T) {
	set := newContextualSet(t, map[string]string{
		"main.html": `<p>{% if x %}{{ v }}{% else %}z{{ v }}{% endif %}</p>` +
			`<input title="{% if x %}a{{ v }}b{% else %}c{{ v }}d{% endif %}">`,
	})
	out, err := renderSet(set, "main.html", Context{"x": true, "v": contextualTestValue})
	if err != nil {
		t.Fatalf("balanced branches should be accepted: %v", err)
	}
	if !strings.Contains(out, "<p>a&#39;") || !strings.Contains(out, `title="aa&#39;`) {
		t.Fatalf("balanced branches rendered incorrectly: %q", out)
	}

	out, err = renderSet(set, "main.html", Context{"x": false, "v": contextualTestValue})
	if err != nil {
		t.Fatalf("else branch render failed: %v", err)
	}
	if !strings.Contains(out, "<p>za&#39;") || !strings.Contains(out, `title="ca&#39;`) {
		t.Fatalf("else branch rendered incorrectly: %q", out)
	}
}

// A loop body is entered repeatedly and must end in the context it started.
func TestContextualLoopBodyReturnsToEntry(t *testing.T) {
	set := newContextualSet(t, map[string]string{
		"main.html": `{% for item in items %}<p>{{ item }}</p>{% endfor %}`,
	})
	out, err := renderSet(set, "main.html", Context{"items": []string{contextualTestValue, "x"}})
	if err != nil {
		t.Fatalf("well-formed loop failed: %v", err)
	}
	if !strings.Contains(out, "<p>a&#39;&quot;b&lt;c&gt;") || !strings.Contains(out, "<p>x</p>") {
		t.Fatalf("loop output wrong: %q", out)
	}

	bad := newContextualSet(t, map[string]string{
		"main.html": `<input title="{% for item in items %}{{ item }}"{% endfor %}>`,
	})
	_, err = renderSet(bad, "main.html", Context{"items": []string{"x"}})
	if err == nil || !strings.Contains(err.Error(), "loop body does not return") {
		t.Fatalf("expected loop context error, got: %v", err)
	}

	// An attribute that simply stays open until the end is rejected too.
	bad = newContextualSet(t, map[string]string{
		"main.html": `<input title="{% for item in items %}{{ item }}{% endfor %}`,
	})
	_, err = renderSet(bad, "main.html", Context{"items": []string{"x"}})
	if err == nil || !strings.Contains(err.Error(), "not closed") {
		t.Fatalf("expected unterminated attribute error, got: %v", err)
	}
}
