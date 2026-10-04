package pongo2

// Iterator is the Go-side convention for lazily streaming values into a
// {% for %} loop (e.g. rows of a mail export cursor) without first
// materializing them into a slice.
//
// A value passed through the template Context that implements Iterator is
// consumed item by item by the for-tag:
//
//	{% for mail in mailCursor %}{{ mail.Subject }}{% endfor %}
//
// The for-tag pulls at most one item ahead of the current one (to compute
// forloop.Last) and never drains the sequence just to learn its length.
// Close is called exactly once when the loop finishes, is left early via
// {% break %}, or aborts with a template or writer error.
type Iterator interface {
	// Next reports the next item of the sequence:
	//
	//	ok == true:  item holds the next value.
	//	ok == false, err == nil: the sequence is exhausted.
	//	ok == false, err != nil: iteration failed.
	Next() (item any, ok bool, err error)

	// Close releases the underlying cursor. The for-tag calls it exactly
	// once for every iterator it obtained, on normal completion as well as
	// on early or erroneous exits.
	Close() error
}

// SizedIterator is an optional extension an Iterator can implement to
// announce its total length. Only when the length is known, the for-tag
// populates forloop.Revcounter and forloop.Revcounter0; otherwise these
// variables are explicitly NIL inside the loop.
type SizedIterator interface {
	Iterator

	// Len reports the total number of items. ok == false means the length
	// is unknown.
	Len() (length int, ok bool)
}

// ReversibleIterator is an optional extension an Iterator can implement to
// declare support for {% for ... reversed %}. Loops requesting reversed
// order over an iterator not implementing this interface fail with an
// error instead of silently buffering the whole sequence.
//
// Reverse returns the iterator the loop will actually consume (and close);
// ownership of the original iterator is transferred to it.
type ReversibleIterator interface {
	Iterator

	// Reverse returns an iterator yielding the same items in reverse order.
	Reverse() (Iterator, error)
}

// asIterator returns the Iterator held by v, if any. A value implementing
// Iterator takes precedence over the built-in slice/map/string iteration.
func asIterator(v *Value) (Iterator, bool) {
	if v == nil || !v.val.IsValid() {
		return nil, false
	}
	it, ok := v.Interface().(Iterator)
	return it, ok
}
