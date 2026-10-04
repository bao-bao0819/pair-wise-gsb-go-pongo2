package pongo2_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/flosch/pongo2/v7"
)

// failWriter always fails with err, simulating an output writer failure.
type failWriter struct{ err error }

func (w failWriter) Write(p []byte) (int, error) { return 0, w.err }

// fakeIterator is a pongo2.Iterator that records how often Next and Close
// were called, so tests can prove lazy consumption and exact-once closing.
type fakeIterator struct {
	items      []any
	pos        int
	nextCalls  int
	closeCalls int
	errAt      int // 1-based Next call that returns err; 0 disables
	err        error
}

func newFakeIterator(items ...any) *fakeIterator {
	return &fakeIterator{items: items}
}

func (f *fakeIterator) Next() (any, bool, error) {
	f.nextCalls++
	if f.errAt > 0 && f.nextCalls == f.errAt {
		return nil, false, f.err
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
	return nil
}

// sizedFakeIterator additionally reports its total length.
type sizedFakeIterator struct {
	*fakeIterator
}

func (s *sizedFakeIterator) Len() int { return len(s.items) }

// reversibleFakeIterator supports reversed iteration by returning a new
// (recorded) iterator over the reversed items.
type reversibleFakeIterator struct {
	*fakeIterator
	reversed *fakeIterator
}

func (r *reversibleFakeIterator) Reverse() (pongo2.Iterator, error) {
	items := make([]any, 0, len(r.items))
	for i := len(r.items) - 1; i >= 0; i-- {
		items = append(items, r.items[i])
	}
	r.reversed = newFakeIterator(items...)
	return r.reversed, nil
}

func renderString(t *testing.T, tpl string, ctx pongo2.Context) (string, error) {
	t.Helper()
	template, err := pongo2.FromString(tpl)
	if err != nil {
		t.Fatalf("compilation failed: %v", err)
	}
	out, err := template.Execute(ctx)
	return out, err
}

// TestForIteratorLazyBreak proves that taking the first 3 items via
// {% break %} does not drain the whole data source: only 3 items plus the
// single lookahead item are pulled, and the cursor is closed exactly once.
func TestForIteratorLazyBreak(t *testing.T) {
	items := make([]any, 100)
	for i := range items {
		items[i] = i + 1
	}
	it := newFakeIterator(items...)

	out, err := renderString(t,
		`{% for x in it %}{{ x }}{% if forloop.Counter == 3 %}{% break %}{% endif %}{% endfor %}`,
		pongo2.Context{"it": it})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if out != "123" {
		t.Errorf("expected output '123', got '%s'", out)
	}
	if it.nextCalls != 4 {
		t.Errorf("expected 4 Next calls (3 items + 1 lookahead), got %d", it.nextCalls)
	}
	if it.closeCalls != 1 {
		t.Errorf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

// TestForIteratorEmpty checks the {% empty %} branch for an empty iterator.
func TestForIteratorEmpty(t *testing.T) {
	it := newFakeIterator()

	out, err := renderString(t,
		`{% for x in it %}{{ x }}{% empty %}EMPTY{% endfor %}`,
		pongo2.Context{"it": it})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if out != "EMPTY" {
		t.Errorf("expected output 'EMPTY', got '%s'", out)
	}
	if it.nextCalls != 1 {
		t.Errorf("expected 1 Next call, got %d", it.nextCalls)
	}
	if it.closeCalls != 1 {
		t.Errorf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

// TestForIteratorNextError checks that a failure during lookahead aborts
// the loop with that error and still closes the cursor exactly once.
func TestForIteratorNextError(t *testing.T) {
	it := newFakeIterator(1, 2, 3)
	it.errAt = 2 // fail on the lookahead for the first item
	it.err = errors.New("source failed")

	_, err := renderString(t,
		`{% for x in it %}{{ x }}{% endfor %}`,
		pongo2.Context{"it": it})
	if err == nil || !strings.Contains(err.Error(), "source failed") {
		t.Fatalf("expected 'source failed' error, got %v", err)
	}
	if it.closeCalls != 1 {
		t.Errorf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

// TestForIteratorNestedBreakContinue checks that break/continue of the
// inner loop only affect the inner loop: the outer loop still consumes all
// of its items, and every inner cursor is closed exactly once.
func TestForIteratorNestedBreakContinue(t *testing.T) {
	outer := newFakeIterator("a", "b", "c")
	var inners []*fakeIterator
	ctx := pongo2.Context{
		"outer": outer,
		"getInner": func() *fakeIterator {
			inner := newFakeIterator(1, 2, 3, 4, 5)
			inners = append(inners, inner)
			return inner
		},
	}

	out, err := renderString(t,
		`{% for o in outer %}[{{ o }}:{% for i in getInner %}{% if i == 2 %}{% continue %}{% endif %}{{ i }}{% if i == 3 %}{% break %}{% endif %}{% endfor %}]{% endfor %}`,
		ctx)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if out != "[a:13][b:13][c:13]" {
		t.Errorf("expected '[a:13][b:13][c:13]', got '%s'", out)
	}
	if outer.closeCalls != 1 {
		t.Errorf("outer: expected exactly 1 Close call, got %d", outer.closeCalls)
	}
	if len(inners) != 3 {
		t.Fatalf("expected 3 inner iterators, got %d", len(inners))
	}
	for idx, inner := range inners {
		if inner.closeCalls != 1 {
			t.Errorf("inner %d: expected exactly 1 Close call, got %d", idx, inner.closeCalls)
		}
		if inner.nextCalls != 4 {
			t.Errorf("inner %d: expected 4 Next calls (break after 3 items + lookahead), got %d", idx, inner.nextCalls)
		}
	}
}

// TestForIteratorLast checks forloop.Last via at most one item of lookahead.
func TestForIteratorLast(t *testing.T) {
	it := newFakeIterator(1, 2, 3)

	out, err := renderString(t,
		`{% for x in it %}{{ x }}:{{ forloop.Last }};{% endfor %}`,
		pongo2.Context{"it": it})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if out != "1:False;2:False;3:True;" {
		t.Errorf("unexpected output '%s'", out)
	}
	if it.nextCalls != 4 {
		t.Errorf("expected 4 Next calls (3 items + end), got %d", it.nextCalls)
	}
	if it.closeCalls != 1 {
		t.Errorf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

// TestForIteratorRevcounterUnknown checks that length-dependent counters
// stay unavailable (render as empty) when the iterator does not declare a
// length, and that the source is not drained to compute them.
func TestForIteratorRevcounterUnknown(t *testing.T) {
	it := newFakeIterator(1, 2)

	out, err := renderString(t,
		`{% for x in it %}{{ forloop.Counter }}/{{ forloop.Revcounter }}/{{ forloop.Revcounter0 }};{% endfor %}`,
		pongo2.Context{"it": it})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if out != "1//;2//;" {
		t.Errorf("expected '1//;2//;', got '%s'", out)
	}
	if it.nextCalls != 3 {
		t.Errorf("expected 3 Next calls (no draining for revcounter), got %d", it.nextCalls)
	}
}

// TestForIteratorRevcounterSized checks revcounter for iterators that
// declare their length via SizedIterator.
func TestForIteratorRevcounterSized(t *testing.T) {
	it := &sizedFakeIterator{newFakeIterator(1, 2, 3)}

	out, err := renderString(t,
		`{% for x in it %}{{ forloop.Counter }}/{{ forloop.Revcounter }}/{{ forloop.Revcounter0 }};{% endfor %}`,
		pongo2.Context{"it": it})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if out != "1/3/2;2/2/1;3/1/0;" {
		t.Errorf("expected '1/3/2;2/2/1;3/1/0;', got '%s'", out)
	}
	if it.closeCalls != 1 {
		t.Errorf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

// TestForIteratorReversedUnsupported checks that 'reversed' is an error
// unless the iterator declares support for it.
func TestForIteratorReversedUnsupported(t *testing.T) {
	it := newFakeIterator(1, 2, 3)

	_, err := renderString(t,
		`{% for x in it reversed %}{{ x }}{% endfor %}`,
		pongo2.Context{"it": it})
	if err == nil || !strings.Contains(err.Error(), "'reversed' is not supported") {
		t.Fatalf("expected 'reversed not supported' error, got %v", err)
	}
}

// TestForIteratorReversed checks reversed iteration when the iterator
// declares support via ReversibleIterator.
func TestForIteratorReversed(t *testing.T) {
	it := &reversibleFakeIterator{fakeIterator: newFakeIterator(1, 2, 3)}

	out, err := renderString(t,
		`{% for x in it reversed %}{{ x }} {% endfor %}`,
		pongo2.Context{"it": it})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if out != "3 2 1 " {
		t.Errorf("expected '3 2 1 ', got '%s'", out)
	}
	if it.reversed == nil {
		t.Fatal("expected Reverse() to be called")
	}
	if it.reversed.closeCalls != 1 {
		t.Errorf("expected exactly 1 Close call on the reversed cursor, got %d", it.reversed.closeCalls)
	}
}

// TestForIteratorWriterFailure checks that an output writer failure aborts
// the loop and closes the cursor exactly once.
func TestForIteratorWriterFailure(t *testing.T) {
	it := newFakeIterator(1, 2, 3)

	template, err := pongo2.FromString(`{% for x in it %}{{ x }}{% endfor %}`)
	if err != nil {
		t.Fatalf("compilation failed: %v", err)
	}
	err = template.ExecuteWriterUnbuffered(
		pongo2.Context{"it": it},
		failWriter{errors.New("writer failed")})
	if err == nil || !strings.Contains(err.Error(), "writer failed") {
		t.Fatalf("expected 'writer failed' error, got %v", err)
	}
	if it.closeCalls != 1 {
		t.Errorf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

// TestForIteratorTemplateError checks that a template error inside the loop
// body closes the cursor exactly once.
func TestForIteratorTemplateError(t *testing.T) {
	it := newFakeIterator(1, 2, 3)

	_, err := renderString(t,
		`{% for x in it %}{{ x.no.such.field }}{% endfor %}`,
		pongo2.Context{"it": it})
	if err == nil {
		t.Fatal("expected a template error, got nil")
	}
	if it.closeCalls != 1 {
		t.Errorf("expected exactly 1 Close call, got %d", it.closeCalls)
	}
}

// TestForSliceBreakContinue checks break/continue for classic slices and
// that ordinary counting (including revcounter) is unchanged.
func TestForSliceBreakContinue(t *testing.T) {
	out, err := renderString(t,
		`{% for x in items %}{% if x == 2 %}{% continue %}{% endif %}{{ x }}:{{ forloop.Revcounter }} {% if x == 3 %}{% break %}{% endif %}{% endfor %}`,
		pongo2.Context{"items": []int{1, 2, 3, 4, 5}})
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
	if out != "1:5 3:3 " {
		t.Errorf("expected '1:5 3:3 ', got '%s'", out)
	}
}

// TestBreakOutsideLoop checks that break/continue outside of a for-loop
// surface as an error instead of being silently ignored.
func TestBreakOutsideLoop(t *testing.T) {
	for _, tag := range []string{"break", "continue"} {
		_, err := renderString(t, `{% `+tag+` %}`, nil)
		if err == nil || !strings.Contains(err.Error(), tag+" outside of for-loop") {
			t.Errorf("expected '%s outside of for-loop' error, got %v", tag, err)
		}
	}
}
