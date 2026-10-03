package pongo2

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func TestTagCallBasic(t *testing.T) {
	// The macro renders the table frame, the {% call %} block provides a row.
	tpl, err := FromString(`{% macro render_table(items) %}<table>{% for it in items %}{{ caller(it) }}{% endfor %}</table>{% endmacro %}{% call(item) render_table(users) %}<tr><td>{{ item }}</td></tr>{% endcall %}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	out, err := tpl.Execute(Context{"users": []string{"alice", "bob"}})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	expected := `<table><tr><td>alice</td></tr><tr><td>bob</td></tr></table>`
	if out != expected {
		t.Errorf("expected %q, got %q", expected, out)
	}
}

func TestTagCallDeclarationSiteScope(t *testing.T) {
	// The block reads locals from its declaration site; a same-named
	// temporary variable inside the macro must not be visible.
	tpl, err := FromString(`{% set item = "decl" %}{% macro m() %}{% set item = "macro-temp" %}[{{ caller() }}]{% endmacro %}{% call() m() %}{{ item }}{% endcall %}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	out, err := tpl.Execute(nil)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "[decl]" {
		t.Errorf("expected %q, got %q", "[decl]", out)
	}
}

func TestTagCallParamsScopedPerInvocation(t *testing.T) {
	// The macro invokes caller multiple times; each invocation binds the
	// parameter only for that call and does not leak it afterwards.
	tpl, err := FromString(`{% macro m() %}{{ caller("x") }}|{{ caller("y") }}{% endmacro %}{% call(v) m() %}({{ v }}){% endcall %}[{{ v }}]`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	out, err := tpl.Execute(nil)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "(x)|(y)[]" {
		t.Errorf("expected %q, got %q", "(x)|(y)[]", out)
	}
}

func TestTagCallNested(t *testing.T) {
	tpl, err := FromString(`{% macro outer_m() %}<{{ caller() }}>{% endmacro %}{% macro inner_m() %}[{{ caller() }}]{% endmacro %}{% call() outer_m() %}A{% call() inner_m() %}B{% endcall %}C{% endcall %}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	out, err := tpl.Execute(nil)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "<A[B]C>" {
		t.Errorf("expected %q, got %q", "<A[B]C>", out)
	}
}

func TestTagCallNestedLoopsSameVarName(t *testing.T) {
	// Two nested loops both use "item"; the block must see the innermost
	// declaration-site variable, not the macro's same-named temporary.
	tpl, err := FromString(`{% macro cell() %}{% set item = "macro-temp" %}<{{ caller() }}>{% endmacro %}{% for item in rows %}{% for item in cols %}{% call() cell() %}{{ item }}{% endcall %}{% endfor %}{% endfor %}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	out, err := tpl.Execute(Context{
		"rows": []string{"r1", "r2"},
		"cols": []string{"c1", "c2"},
	})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "<c1><c2><c1><c2>" {
		t.Errorf("expected %q, got %q", "<c1><c2><c1><c2>", out)
	}
}

func TestTagCallImportedMacro(t *testing.T) {
	set := NewSet("test-tag-call-import", NewFSLoader(fstest.MapFS{
		"macros.html": &fstest.MapFile{Data: []byte(`{% macro row_wrap() export %}<row>{{ caller() }}</row>{% endmacro %}`)},
	}))
	tpl, err := set.FromString(`{% import "macros.html" row_wrap %}{% call() row_wrap() %}cell{% endcall %}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	out, err := tpl.Execute(nil)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "<row>cell</row>" {
		t.Errorf("expected %q, got %q", "<row>cell</row>", out)
	}
}

func TestTagCallMacroDefaultArgs(t *testing.T) {
	// Macro default arguments keep working when invoked through {% call %}.
	tpl, err := FromString(`{% macro m(sep=", ") %}{{ caller("a") }}{{ sep }}{{ caller("b") }}{% endmacro %}{% call(v) m() %}[{{ v }}]{% endcall %}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	out, err := tpl.Execute(nil)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "[a], [b]" {
		t.Errorf("expected %q, got %q", "[a], [b]", out)
	}
}

func TestTagCallAutoescape(t *testing.T) {
	// Autoescaping still applies inside the {% call %} block body.
	tpl, err := FromString(`{% macro m() %}{{ caller("<b>") }}{% endmacro %}{% call(v) m() %}{{ v }}{% endcall %}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	out, err := tpl.Execute(nil)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "&lt;b&gt;" {
		t.Errorf("expected %q, got %q", "&lt;b&gt;", out)
	}
}

func TestTagCallArityError(t *testing.T) {
	// Wrong argument count must produce an error carrying both template
	// positions: the {% call %} block and the macro definition.
	tpl, err := FromString("{% macro m() %}{{ caller(1, 2) }}{% endmacro %}\n{% call(x) m() %}{% endcall %}")
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	_, err = tpl.Execute(nil)
	if err == nil {
		t.Fatal("expected an error, got none")
	}
	msg := err.Error()
	for _, want := range []string{
		"takes 1 argument(s), but 2 were given",
		"callback declared at <string>:2:",
		"macro 'm' defined at <string>:1:",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
}

func TestTagCallDepthLimit(t *testing.T) {
	// Mutual recursion through a {% call %} callback must be stopped by the
	// recursive macro call depth limit.
	tpl, err := FromString(`{% macro ping() %}{% call() pong() %}{% endcall %}{% endmacro %}{% macro pong() %}{{ caller() }}{{ ping() }}{% endmacro %}{{ ping() }}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	_, err = tpl.Execute(nil)
	if err == nil {
		t.Fatal("expected a depth limit error, got none")
	}
	if !strings.Contains(err.Error(), "maximum recursive macro call depth reached") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTagCallCallbackCountsTowardsDepth(t *testing.T) {
	// A callback invocation itself consumes one unit of the macro call
	// depth: with the depth one below the limit, the macro call still fits
	// but the callback edge must trip the limit.
	tpl, err := FromString(`{% macro m() %}{{ caller() }}{% endmacro %}{% call() m() %}x{% endcall %}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	parent, ctx, err := tpl.newContextForExecution(nil)
	if err != nil {
		t.Fatalf("context error: %v", err)
	}
	ctx.macroDepth = maxMacroDepth - 1

	var buf bytes.Buffer
	err = parent.root.Execute(ctx, &buf)
	if err == nil {
		t.Fatal("expected a depth limit error, got none")
	}
	if !strings.Contains(err.Error(), "maximum recursive macro call depth reached") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTagCallConcurrent(t *testing.T) {
	// The same compiled template must be safe for concurrent rendering.
	tpl, err := FromString(`{% macro render_table(items) %}<table>{% for it in items %}{{ caller(it) }}{% endfor %}</table>{% endmacro %}{% call(item) render_table(users) %}<tr><td>{{ item }}</td></tr>{% endcall %}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	expected := `<table><tr><td>alice</td></tr><tr><td>bob</td></tr></table>`

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				out, err := tpl.Execute(Context{"users": []string{"alice", "bob"}})
				if err != nil {
					errs <- err
					return
				}
				if out != expected {
					errs <- fmt.Errorf("expected %q, got %q", expected, out)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent execution failed: %v", err)
	}
}
