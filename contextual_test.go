package pongo2

import (
	"io"
	"path"
	"strings"
	"testing"
)

// memLoader is an in-memory template loader used to exercise static
// include/extends composition without touching the file system.
type memLoader struct {
	files map[string]string
}

func (l *memLoader) Abs(base, name string) string {
	if base == "" || base == "<string>" {
		return name
	}
	return path.Join(path.Dir(base), name)
}

func (l *memLoader) Get(filename string) (io.Reader, error) {
	content, ok := l.files[filename]
	if !ok {
		return nil, errMemTemplateNotFound(filename)
	}
	return strings.NewReader(content), nil
}

type memTemplateNotFoundError struct{ name string }

func (e *memTemplateNotFoundError) Error() string { return "template not found: " + e.name }

func errMemTemplateNotFound(name string) error { return &memTemplateNotFoundError{name: name} }

func newContextualSet(t *testing.T, files map[string]string) *TemplateSet {
	t.Helper()
	set := NewSet("contextual-test", &memLoader{files: files})
	if err := set.SetContextualEscaping(true); err != nil {
		t.Fatalf("SetContextualEscaping failed: %v", err)
	}
	return set
}

func renderSet(set *TemplateSet, name string, ctx Context) (string, error) {
	tpl, err := set.FromFile(name)
	if err != nil {
		return "", err
	}
	return tpl.Execute(ctx)
}

// contextualTestValue contains a quote, angle brackets and a colon so the
// four supported contexts can be told apart in the expected output.
const contextualTestValue = `a'"b<c>:d&e`

func TestContextualEscapingFourContexts(t *testing.T) {
	set := newContextualSet(t, map[string]string{
		"main.html": `<p>{{ v }}</p>` +
			`<input value="{{ v }}">` +
			`<a href="{{ v }}">x</a>` +
			`<script>var s = "{{ v }}"; var q = '{{ v }}';</script>`,
	})

	out, err := renderSet(set, "main.html", Context{"v": contextualTestValue})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	want := `<p>a&#39;&quot;b&lt;c&gt;:d&amp;e</p>` +
		`<input value="a&#39;&quot;b&lt;c&gt;:d&amp;e">` +
		`<a href="a&#39;&quot;b&lt;c&gt;:d&amp;e">x</a>` +
		`<script>var s = "a\u0027\u0022b\u003Cc\u003E:d\u0026e"; var q = 'a\u0027\u0022b\u003Cc\u003E:d\u0026e';</script>`
	if out != want {
		t.Fatalf("contextual escaping mismatch:\n got: %q\nwant: %q", out, want)
	}
}

func TestContextualEscapingSafeDoesNotBypass(t *testing.T) {
	set := newContextualSet(t, map[string]string{
		"main.html": `<p>{{ v|safe }}</p><a href="{{ u|safe }}">x</a>`,
	})

	out, err := renderSet(set, "main.html", Context{"v": contextualTestValue, "u": "javascript:alert(1)"})
	if err == nil {
		t.Fatalf("expected dangerous URL to be rejected even with |safe, got output %q", out)
	}
	if !strings.Contains(err.Error(), "disallowed protocol") {
		t.Fatalf("expected protocol rejection, got: %v", err)
	}

	// Text context must still escape despite |safe.
	out, err = renderSet(set, "main.html", Context{"v": contextualTestValue, "u": "/ok"})
	if err != nil {
		t.Fatalf("render with safe URL failed: %v", err)
	}
	if strings.Contains(out, contextualTestValue) || !strings.Contains(out, "&lt;c&gt;") {
		t.Fatalf("|safe must not bypass text escaping, got %q", out)
	}
}
