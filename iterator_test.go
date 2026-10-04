package pongo2_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/flosch/pongo2/v7"
)

// fakeIterator is a lazy pongo2.Iterator that records how often Next and
// Close were called, so tests can prove the loop neither drains the source
// nor leaks the cursor.
type fakeIterator struct {
	items      []any
	pos        int
	nextCalls  int
	closeCalls int
	errAtCall  int   // 1-based Next call that fails; 0 disables
	nextErr    error // error returned at errAtCall
	closeErr   error // error returned by Close
}

func newFakeIterator(n int) *fakeIterator {
	items := make([]any, n)
	for i := range items {
		items[i] = i + 1
	}
	return &fakeIterator{items: items}
}

func (f *fakeIterator) Next() (any, bool, error) {
	f.nextCalls++
	if f.errAtCall > 0 && f.nextCalls == f.errAtCall {
		return nil, false, f.nextErr
	}
	if f.pos >= len(f.items) {
		return nil, false, nil
	}
	item := f.items[f.pos]
	f.pos++
	return item, true, nil
}

func (f *fakeIterator) Close() error {
	f.closeCalls++
	return f.closeErr
}

// sizedFakeIterator additionally announces its length (SizedIterator).
type sizedFakeIterator struct {
	*fakeIterator
}

func (s *sizedFakeIterator) Len() (int, bool) {
	return len(s.items), true
}

// reversibleFakeIterator declares support for reversed iteration.
type reversibleFakeIterator struct {
	*fakeIterator
	reversed *fakeIterator
}

func (r *reversibleFakeIterator) Reverse() (pongo2.Iterator, error) {
	items := make([]any, len(r.items))
	for i, item := range r.items {
		items[len(r.items)-1-i] = item
	}
	r.reversed = &fakeIterator{items: items}
	return r.reversed, nil
}

func renderWithIterator(t *testing.T, tpl string, ctx pongo2.Context) (string, error) {
	t.Helper()
	template, err := pongo2.FromString(tpl)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	return template.Execute(ctx)
}

func TestIteratorBreakDoesNotDrainSource(t *testing.T) {
	it := newFakeIterator(100)
	out, err := renderWithIterator(t,
		`{% for m in mails %}{{ m }}{% if m == 3 %}{% break %}{% endif %}{% endfor %}`,
		pongo2.Context{"mails": it})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "123" {
		t.Fatalf("expected '123', got %q", out)
	}
	// 3 items consumed + 1 primed lookahead; the remaining 96 rows of the
	// "export" must stay untouched.
	if it.nextCalls != 4 {
		t.Fatalf("expected 4 Next calls, got %d", it.nextCalls)
	}
	if it.closeCalls != 1 {
		t.Fatalf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

func TestIteratorForloopLastViaLookahead(t *testing.T) {
	it := newFakeIterator(3)
	out, err := renderWithIterator(t,
		`{% for m in mails %}{{ forloop.Counter }}:{{ m }}:{{ forloop.First }}:{{ forloop.Last }};{% endfor %}`,
		pongo2.Context{"mails": it})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	expected := "1:1:True:False;2:2:False:False;3:3:False:True;"
	if out != expected {
		t.Fatalf("expected %q, got %q", expected, out)
	}
	if it.nextCalls != 4 {
		t.Fatalf("expected 4 Next calls (3 items + 1 lookahead), got %d", it.nextCalls)
	}
	if it.closeCalls != 1 {
		t.Fatalf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

func TestIteratorEmptyBranch(t *testing.T) {
	it := newFakeIterator(0)
	out, err := renderWithIterator(t,
		`{% for m in mails %}{{ m }}{% empty %}no mails{% endfor %}`,
		pongo2.Context{"mails": it})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "no mails" {
		t.Fatalf("expected empty branch, got %q", out)
	}
	if it.nextCalls != 1 {
		t.Fatalf("expected 1 Next call, got %d", it.nextCalls)
	}
	if it.closeCalls != 1 {
		t.Fatalf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

func TestIteratorErrorDuringLookahead(t *testing.T) {
	it := newFakeIterator(10)
	it.errAtCall = 2 // fails while looking ahead from the first item
	it.nextErr = errors.New("cursor lost")
	_, err := renderWithIterator(t,
		`{% for m in mails %}{{ m }}{% empty %}no mails{% endfor %}`,
		pongo2.Context{"mails": it})
	if err == nil || !strings.Contains(err.Error(), "cursor lost") {
		t.Fatalf("expected 'cursor lost' error, got %v", err)
	}
	if it.closeCalls != 1 {
		t.Fatalf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

func TestIteratorErrorOnFirstNextDoesNotRenderEmpty(t *testing.T) {
	it := newFakeIterator(10)
	it.errAtCall = 1
	it.nextErr = errors.New("connection reset")
	_, err := renderWithIterator(t,
		`{% for m in mails %}{{ m }}{% empty %}no mails{% endfor %}`,
		pongo2.Context{"mails": it})
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("expected 'connection reset' error, got %v", err)
	}
	if it.closeCalls != 1 {
		t.Fatalf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

func TestIteratorClosedOnTemplateError(t *testing.T) {
	it := newFakeIterator(5)
	_, err := renderWithIterator(t,
		`{% for m in mails %}{{ fail() }}{% endfor %}`,
		pongo2.Context{
			"mails": it,
			"fail":  func() (any, error) { return nil, errors.New("boom") },
		})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected 'boom' error, got %v", err)
	}
	if it.closeCalls != 1 {
		t.Fatalf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

// failingWriter fails after a limited number of writes.
type failingWriter struct {
	remaining int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, errors.New("disk full")
	}
	w.remaining--
	return len(p), nil
}

func TestIteratorClosedOnWriterFailure(t *testing.T) {
	it := newFakeIterator(5)
	tpl, err := pongo2.FromString(`{% for m in mails %}{{ m }}{% endfor %}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	err = tpl.ExecuteWriterUnbuffered(pongo2.Context{"mails": it}, &failingWriter{remaining: 2})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected 'disk full' error, got %v", err)
	}
	if it.closeCalls != 1 {
		t.Fatalf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

func TestIteratorCloseErrorPropagates(t *testing.T) {
	it := newFakeIterator(2)
	it.closeErr = errors.New("close failed")
	_, err := renderWithIterator(t,
		`{% for m in mails %}{{ m }}{% endfor %}`,
		pongo2.Context{"mails": it})
	if err == nil || !strings.Contains(err.Error(), "close failed") {
		t.Fatalf("expected 'close failed' error, got %v", err)
	}
	if it.closeCalls != 1 {
		t.Fatalf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

func TestIteratorNestedBreakContinueLevels(t *testing.T) {
	outer := newFakeIterator(3)
	inners := []*fakeIterator{newFakeIterator(3), newFakeIterator(3), newFakeIterator(3)}
	idx := 0
	// Outer loop iterates over a lazy iterator of lazy iterators.
	out, err := renderWithIterator(t,
		`{% for inner in outers %}[{% for m in inner %}{% if m == 2 %}{% continue %}{% endif %}{% if m == 3 %}{% break %}{% endif %}{{ m }}{% endfor %}]{% endfor %}`,
		pongo2.Context{"outers": &sliceIter{outer: outer, inners: inners, idx: &idx}})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "[1][1][1]" {
		t.Fatalf("expected '[1][1][1]', got %q", out)
	}
	// Inner break/continue must not disturb the outer loop: all 3 inner
	// cursors consumed and every cursor closed exactly once.
	if outer.closeCalls != 1 {
		t.Fatalf("expected outer Close once, got %d", outer.closeCalls)
	}
	for i, inner := range inners {
		if inner.closeCalls != 1 {
			t.Fatalf("inner %d: expected 1 Close call, got %d", i, inner.closeCalls)
		}
		if inner.nextCalls != 4 { // prime + one lookahead per rendered item
			t.Fatalf("inner %d: expected 4 Next calls, got %d", i, inner.nextCalls)
		}
	}
}

// sliceIter lazily hands out the pre-built inner iterators.
type sliceIter struct {
	outer  *fakeIterator
	inners []*fakeIterator
	idx    *int
}

func (s *sliceIter) Next() (any, bool, error) {
	if *s.idx >= len(s.inners) {
		return nil, false, nil
	}
	item := s.inners[*s.idx]
	*s.idx++
	return item, true, nil
}

func (s *sliceIter) Close() error { return s.outer.Close() }

func TestIteratorRevcounterUnavailableWithoutLength(t *testing.T) {
	it := newFakeIterator(2)
	out, err := renderWithIterator(t,
		`{% for m in mails %}[{{ forloop.Revcounter }},{{ forloop.Revcounter0 }}]{% endfor %}`,
		pongo2.Context{"mails": it})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	// Length unknown: both variables must be explicitly NIL (rendered as
	// empty), never computed by draining the source.
	if out != "[,][,]" {
		t.Fatalf("expected '[,][,]', got %q", out)
	}
	if it.nextCalls != 3 {
		t.Fatalf("expected 3 Next calls, got %d", it.nextCalls)
	}
}

func TestIteratorRevcounterWithSizedIterator(t *testing.T) {
	it := &sizedFakeIterator{fakeIterator: newFakeIterator(3)}
	out, err := renderWithIterator(t,
		`{% for m in mails %}{{ forloop.Revcounter }},{{ forloop.Revcounter0 }};{% endfor %}`,
		pongo2.Context{"mails": it})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "3,2;2,1;1,0;" {
		t.Fatalf("expected '3,2;2,1;1,0;', got %q", out)
	}
}

func TestIteratorReversedUnsupported(t *testing.T) {
	it := newFakeIterator(3)
	_, err := renderWithIterator(t,
		`{% for m in mails reversed %}{{ m }}{% endfor %}`,
		pongo2.Context{"mails": it})
	if err == nil || !strings.Contains(err.Error(), "reversed") {
		t.Fatalf("expected reversed-support error, got %v", err)
	}
	// The loop never obtained a cursor it could consume.
	if it.nextCalls != 0 {
		t.Fatalf("expected 0 Next calls, got %d", it.nextCalls)
	}
}

func TestIteratorReversedSupported(t *testing.T) {
	it := &reversibleFakeIterator{fakeIterator: newFakeIterator(3)}
	out, err := renderWithIterator(t,
		`{% for m in mails reversed %}{{ m }}{% endfor %}`,
		pongo2.Context{"mails": it})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "321" {
		t.Fatalf("expected '321', got %q", out)
	}
	if it.reversed == nil || it.reversed.closeCalls != 1 {
		t.Fatalf("expected reversed cursor to be closed exactly once, got %+v", it.reversed)
	}
}

func TestIteratorRejectsKeyValueUnpacking(t *testing.T) {
	it := newFakeIterator(3)
	_, err := renderWithIterator(t,
		`{% for k, v in mails %}{{ k }}{{ v }}{% endfor %}`,
		pongo2.Context{"mails": it})
	if err == nil {
		t.Fatal("expected error for key, value unpacking over an iterator")
	}
}

func TestBreakContinueOutsideLoopRejected(t *testing.T) {
	for _, keyword := range []string{"break", "continue"} {
		_, err := pongo2.FromString(fmt.Sprintf(`{%% %s %%}`, keyword))
		if err == nil {
			t.Fatalf("expected compile error for '%s' outside of a for loop", keyword)
		}
	}
}

func TestBreakInsideMacroRejected(t *testing.T) {
	_, err := pongo2.FromString(`{% for i in items %}{% macro m() %}{% break %}{% endmacro %}{% endfor %}`)
	if err == nil {
		t.Fatal("expected compile error for 'break' inside a macro body")
	}
}

func TestSliceLoopBreakContinueKeepsCounting(t *testing.T) {
	out, err := renderWithIterator(t,
		`{% for i in items %}{% if i == 2 %}{% continue %}{% endif %}{% if i == 4 %}{% break %}{% endif %}{{ forloop.Counter }}:{{ i }}:{{ forloop.Revcounter }};{% endfor %}`,
		pongo2.Context{"items": []int{1, 2, 3, 4, 5}})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "1:1:5;3:3:3;" {
		t.Fatalf("expected '1:1:5;3:3:3;', got %q", out)
	}
}

func TestNestedSliceLoopBreakAffectsInnerOnly(t *testing.T) {
	out, err := renderWithIterator(t,
		`{% for i in items %}[{% for j in items %}{% if j == 2 %}{% break %}{% endif %}{{ j }}{% endfor %}]{% endfor %}`,
		pongo2.Context{"items": []int{1, 2, 3}})
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if out != "[1][1][1]" {
		t.Fatalf("expected '[1][1][1]', got %q", out)
	}
}
