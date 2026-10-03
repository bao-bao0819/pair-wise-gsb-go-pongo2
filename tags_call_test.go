package pongo2

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func TestCallTagBasic(t *testing.T) {
	// The motivating use-case: a macro renders the outer table (borders),
	// the {% call %} block defines how a single row is drawn.
	tpl := Must(FromString(`{% macro email_table(rows) -%}` +
		`<table border="1">{% for row in rows %}<tr>{{ caller(row) }}</tr>{% endfor %}</table>` +
		`{%- endmacro -%}` +
		`{% call(item) email_table(users) %}<td>{{ item.name }}</td><td>{{ item.email }}</td>{% endcall %}`))

	out, err := tpl.Execute(Context{
		"users": []map[string]string{
			{"name": "Alice", "email": "alice@example.com"},
			{"name": "Bob", "email": "bob@example.com"},
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	expected := `<table border="1"><tr><td>Alice</td><td>alice@example.com</td></tr>` +
		`<tr><td>Bob</td><td>bob@example.com</td></tr></table>`
	if out != expected {
		t.Errorf("got %q, want %q", out, expected)
	}
}

func TestCallTagScoping(t *testing.T) {
	t.Run("body reads declaration-site locals, not macro temporaries", func(t *testing.T) {
		tpl := Must(FromString(
			`{% set item = "outer" %}` +
				`{% macro m() %}{% set item = "macro-temp" %}{{ caller() }}{% endmacro %}` +
				`{% call m() %}[{{ item }}]{% endcall %}{{ item }}`))
		out, err := tpl.Execute(nil)
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
		if expected := "[outer]outer"; out != expected {
			t.Errorf("got %q, want %q", out, expected)
		}
	})

	t.Run("caller params only apply during the call", func(t *testing.T) {
		// Two nested loops both use "item"; the caller parameter is also
		// named "item" and must shadow the loop variable only inside the
		// call-block body.
		tpl := Must(FromString(
			`{% macro wrap() %}{{ caller("param") }}{% endmacro %}` +
				`{% for item in outer %}{% for item in inner %}` +
				`{% call(item) wrap() %}{{ item }}{% endcall %}({{ item }})` +
				`{% endfor %};{% endfor %}`))
		out, err := tpl.Execute(Context{
			"outer": []string{"o1", "o2"},
			"inner": []string{"i1", "i2"},
		})
		if err != nil {
			t.Fatalf("execute failed: %v", err)
		}
		if expected := "param(i1)param(i2);param(i1)param(i2);"; out != expected {
			t.Errorf("got %q, want %q", out, expected)
		}
	})
}

func TestCallTagMultipleCallerInvocations(t *testing.T) {
	tpl := Must(FromString(
		`{% macro twice() %}{{ caller(1) }}+{{ caller(2) }}{% endmacro %}` +
			`{% call(n) twice() %}[{{ n }}]{% endcall %}`))
	out, err := tpl.Execute(nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if expected := "[1]+[2]"; out != expected {
		t.Errorf("got %q, want %q", out, expected)
	}
}

func TestCallTagNested(t *testing.T) {
	tpl := Must(FromString(
		`{% macro outer_m() %}<outer>{{ caller() }}</outer>{% endmacro %}` +
			`{% macro inner_m(x) %}<inner>{{ x }}:{{ caller() }}</inner>{% endmacro %}` +
			`{% call outer_m() %}{% call inner_m("in") %}X{% endcall %}{% endcall %}`))
	out, err := tpl.Execute(nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if expected := "<outer><inner>in:X</inner></outer>"; out != expected {
		t.Errorf("got %q, want %q", out, expected)
	}
}

func TestCallTagImportedMacro(t *testing.T) {
	set := NewSet("call-import-test", NewFSLoader(fstest.MapFS{
		"macros.html": &fstest.MapFile{Data: []byte(
			`{% macro frame(title) export %}<h1>{{ title }}</h1><div>{{ caller() }}</div>{% endmacro %}`)},
	}))

	tpl, err := set.FromString(
		`{% import "macros.html" frame %}{% call frame("Hi") %}<p>{{ who }}</p>{% endcall %}`)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	out, err := tpl.Execute(Context{"who": "World"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if expected := "<h1>Hi</h1><div><p>World</p></div>"; out != expected {
		t.Errorf("got %q, want %q", out, expected)
	}
}

func TestCallTagArgumentCountMismatch(t *testing.T) {
	// The macro calls caller(1, 2) but the call-block only declares one
	// parameter. The error must carry BOTH template positions: the
	// caller(...) invocation inside the macro (line 2) and the {% call %}
	// tag definition (line 4) - and must not be a bare reflection error.
	tpl := Must(FromString(`{% macro m() %}
{{ caller(1, 2) }}
{% endmacro %}
{% call(x) m() %}{{ x }}{% endcall %}`))

	out, err := tpl.Execute(nil)
	if err == nil {
		t.Fatalf("expected error, got output %q", out)
	}
	if out != "" {
		t.Errorf("expected no partial output, got %q", out)
	}

	msg := err.Error()
	if !strings.Contains(msg, "wrong number of arguments for call-block") {
		t.Errorf("error should explain the argument mismatch, got: %s", msg)
	}
	if !strings.Contains(msg, "Line 2") {
		t.Errorf("error should contain the macro-side caller() position (line 2), got: %s", msg)
	}
	if !strings.Contains(msg, "<string>:4:") {
		t.Errorf("error should contain the call-tag definition position (line 4), got: %s", msg)
	}
	if strings.Contains(msg, "function input argument count") {
		t.Errorf("error must not be the raw reflection error, got: %s", msg)
	}
}

func TestCallTagConcurrentRender(t *testing.T) {
	// One compiled template, rendered concurrently with different contexts.
	tpl := Must(FromString(
		`{% macro box(v) %}<box>{{ caller(v) }}</box>{% endmacro %}` +
			`{% for item in items %}{% call(x) box(item) %}[{{ x }}]{% endcall %}{% endfor %}`))

	const goroutines = 32
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			out, err := tpl.Execute(Context{"items": []string{fmt.Sprintf("a%d", n), fmt.Sprintf("b%d", n)}})
			if err != nil {
				errs <- err
				return
			}
			expected := fmt.Sprintf("<box>[a%d]</box><box>[b%d]</box>", n, n)
			if out != expected {
				errs <- fmt.Errorf("goroutine %d: got %q, want %q", n, out, expected)
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestCallTagRecursionDepthLimit(t *testing.T) {
	t.Run("macro-caller ping-pong is depth limited", func(t *testing.T) {
		// n invokes m through {% call %}; m calls the caller; the caller
		// body calls n again. This macro -> caller -> macro chain is
		// unbounded and both edge kinds count towards the depth limit.
		tpl := Must(FromString(
			`{% macro m() %}{{ caller() }}{% endmacro %}` +
				`{% macro n() %}{% call m() %}{{ n() }}{% endcall %}{% endmacro %}` +
				`{{ n() }}`))
		_, err := tpl.Execute(nil)
		if err == nil {
			t.Fatal("expected recursion depth error")
		}
		if !strings.Contains(err.Error(), "maximum recursive macro call depth") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("macro recursion through call-tag is depth limited", func(t *testing.T) {
		tpl := Must(FromString(
			`{% macro m() %}{% call m() %}x{% endcall %}{% endmacro %}{{ m() }}`))
		_, err := tpl.Execute(nil)
		if err == nil {
			t.Fatal("expected recursion depth error")
		}
		if !strings.Contains(err.Error(), "maximum recursive macro call depth") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("plain macro recursion still limited", func(t *testing.T) {
		tpl := Must(FromString(`{% macro m() %}{{ m() }}{% endmacro %}{{ m() }}`))
		_, err := tpl.Execute(nil)
		if err == nil {
			t.Fatal("expected recursion depth error")
		}
		if !strings.Contains(err.Error(), "maximum recursive macro call depth") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestCallTagAutoescape(t *testing.T) {
	set := NewSet("call-autoescape-test", &DummyLoader{})

	tpl, err := set.FromString(
		`{% macro m() %}{{ caller() }}{% endmacro %}{% call m() %}{{ val }}{% endcall %}`)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	out, err := tpl.Execute(Context{"val": "<b>"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	// The body output is escaped once (autoescape), and neither the caller
	// result nor the macro result is escaped a second time.
	if expected := "&lt;b&gt;"; out != expected {
		t.Errorf("got %q, want %q", out, expected)
	}
}

func TestCallTagMacroDefaultsStillWork(t *testing.T) {
	tpl := Must(FromString(
		`{% macro m(x, y="def") %}{{ x }}-{{ y }}-{{ caller() }}{% endmacro %}` +
			`{% call m("a") %}B{% endcall %}`))
	out, err := tpl.Execute(nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if expected := "a-def-B"; out != expected {
		t.Errorf("got %q, want %q", out, expected)
	}
}

func TestCallTagParseErrors(t *testing.T) {
	tests := []struct {
		name     string
		template string
	}{
		{"missing call expression", `{% call %}{% endcall %}`},
		{"params but no call expression", `{% call(x) %}{% endcall %}`},
		{"malformed param list", `{% call(x x) m() %}{% endcall %}`},
		{"duplicate param name", `{% call(x, x) m() %}{% endcall %}`},
		{"missing endcall", `{% call m() %}`},
		{"trailing garbage", `{% call m() 123 %}{% endcall %}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := FromString(tt.template); err == nil {
				t.Error("expected parse error, got none")
			}
		})
	}
}
