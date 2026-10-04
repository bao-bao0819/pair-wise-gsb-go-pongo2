package pongo2

// Iterator is the Go-side contract for lazily streaming items into a
// {% for %} loop without materializing the whole sequence first (e.g. when
// exporting a large amount of records from a database).
//
// When the object after "in" evaluates to an Iterator, the for-tag consumes
// it item by item instead of calling Value.IterateOrder:
//
//	{% for mail in mailIterator %}
//	    {{ forloop.Counter }}. {{ mail.Subject }}
//	{% empty %}
//	    No mails.
//	{% endfor %}
//
// Semantics:
//   - Next reports either the next item (ok == true), the end of the
//     sequence (ok == false, err == nil) or an error (err != nil).
//   - The for-tag looks ahead by at most one item to compute forloop.Last;
//     it never drains the source to the end.
//   - The cursor obtained by a loop is closed exactly once, no matter how
//     the loop is left: normal end, {% break %}, a template error or an
//     output writer failure.
//   - forloop.Counter, forloop.Counter0, forloop.First and forloop.Last
//     work as usual. forloop.Revcounter and forloop.Revcounter0 stay nil
//     (render as an empty string) unless the iterator also implements
//     SizedIterator, because the total length is unknown.
//   - "reversed" is only available if the iterator implements
//     ReversibleIterator; "sorted" and "for key, value in ..." are not
//     supported for iterators.
//
// Items returned by Next may be plain Go values or *pongo2.Value (used
// as-is, e.g. to return safe/escaped values).
type Iterator interface {
	// Next advances the cursor and reports the next item.
	// It returns (item, true, nil) for an item, (nil, false, nil) at the
	// end of the sequence and (nil, false, err) on failure.
	Next() (item any, ok bool, err error)
	// Close releases the cursor. It is called exactly once by the
	// consuming for-tag.
	Close() error
}

// SizedIterator is optionally implemented by Iterators that know their
// total number of items upfront. It enables forloop.Revcounter and
// forloop.Revcounter0 for lazy iteration. A negative length is treated
// as "unknown".
type SizedIterator interface {
	Len() int
}

// ReversibleIterator is optionally implemented by Iterators that support
// reversed iteration. Without it, using "reversed" in a for-tag over an
// iterator is an execution error.
//
// Reverse returns a new iterator yielding the items in reverse order. The
// for-tag closes the returned iterator; implementations wrapping the
// original iterator should chain Close accordingly.
type ReversibleIterator interface {
	Reverse() (Iterator, error)
}

// asIteratorValue wraps an item produced by an Iterator into a *Value.
// Items that already are *Value are used as-is.
func asIteratorValue(item any) *Value {
	if v, ok := item.(*Value); ok {
		return v
	}
	return AsValue(item)
}
