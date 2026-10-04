package pongo2

import "errors"

// tagForNode represents the {% for %} tag.
//
// The for tag loops over each item in a sequence (slice, array, map, or
// string) or lazily consumes an Iterator (see iterator.go) without
// materializing all items first.
// It provides loop variables through the special "forloop" object.
//
// Basic usage:
//
//	{% for item in items %}
//	    {{ item }}
//	{% endfor %}
//
// Iterating over maps with key and value:
//
//	{% for key, value in myMap %}
//	    {{ key }}: {{ value }}
//	{% endfor %}
//
// Using the empty clause (displayed when the sequence is empty):
//
//	{% for item in items %}
//	    {{ item }}
//	{% empty %}
//	    No items found.
//	{% endfor %}
//
// Using "reversed" to iterate in reverse order:
//
//	{% for item in items reversed %}
//	    {{ item }}
//	{% endfor %}
//
// Using "sorted" to iterate over maps in sorted key order:
//
//	{% for key, value in myMap sorted %}
//	    {{ key }}: {{ value }}
//	{% endfor %}
//
// Loop variables available via forloop:
//   - forloop.Counter: Current iteration (1-indexed)
//   - forloop.Counter0: Current iteration (0-indexed)
//   - forloop.Revcounter: Iterations remaining (1-indexed), nil if the
//     total length is unknown (lazy iterators without SizedIterator)
//   - forloop.Revcounter0: Iterations remaining (0-indexed), nil if unknown
//   - forloop.First: True if this is the first iteration
//   - forloop.Last: True if this is the last iteration
//   - forloop.Parentloop: Access parent loop in nested loops
//
// Loop control:
//   - {% break %} stops the innermost loop
//   - {% continue %} skips to the next item of the innermost loop
//
// Example with loop variables:
//
//	{% for item in items %}
//	    {% if forloop.First %}<ul>{% endif %}
//	    <li>{{ forloop.Counter }}. {{ item }}</li>
//	    {% if forloop.Last %}</ul>{% endif %}
//	{% endfor %}
type tagForNode struct {
	key             string
	value           string // only for maps: for key, value in map
	objectEvaluator IEvaluator
	reversed        bool
	sorted          bool

	bodyWrapper  *NodeWrapper
	emptyWrapper *NodeWrapper
}

// tagForLoopInformation provides loop metadata accessible via "forloop" variable.
type tagForLoopInformation struct {
	Counter     int
	Counter0    int
	Revcounter  *int // nil when the total length is unknown (lazy iterators)
	Revcounter0 *int // nil when the total length is unknown (lazy iterators)
	First       bool
	Last        bool
	Parentloop  *tagForLoopInformation
}

// setRevcounter updates the length-dependent counters for a known length.
func (li *tagForLoopInformation) setRevcounter(count, idx int) {
	revcounter := count - idx
	revcounter0 := count - (idx + 1)
	li.Revcounter = &revcounter
	li.Revcounter0 = &revcounter0
}

// Execute evaluates the loop object and dispatches to lazy iterator
// consumption or classic sequence iteration. If the object is empty, it
// renders the empty wrapper (if present).
func (node *tagForNode) Execute(ctx *ExecutionContext, writer TemplateWriter) error {
	// Backup forloop (as parentloop in public context), key-name and value-name
	forCtx := NewChildExecutionContext(ctx)
	parentloop := forCtx.Private["forloop"]

	// Create loop struct
	loopInfo := &tagForLoopInformation{
		First: true,
	}

	// Is it a loop in a loop?
	if parentloop != nil {
		loopInfo.Parentloop = parentloop.(*tagForLoopInformation)
	}

	// Register loopInfo in public context
	forCtx.Private["forloop"] = loopInfo

	obj, err := node.objectEvaluator.Evaluate(forCtx)
	if err != nil {
		return err
	}

	if it, ok := obj.Interface().(Iterator); ok {
		return node.executeIterator(forCtx, writer, loopInfo, it)
	}
	return node.executeSequence(forCtx, writer, loopInfo, obj)
}

// executeSequence iterates over slices, arrays, maps and strings using
// Value.IterateOrder, preserving the historic behavior (all counters,
// including the length-dependent ones, are always available).
func (node *tagForNode) executeSequence(
	forCtx *ExecutionContext,
	writer TemplateWriter,
	loopInfo *tagForLoopInformation,
	obj *Value,
) (forError error) {
	obj.IterateOrder(func(idx, count int, key, value *Value) bool {
		// There's something to iterate over (correct type and at least 1 item)

		// Update loop infos and public context
		forCtx.Private[node.key] = key
		if value != nil && node.value != "" {
			forCtx.Private[node.value] = value
		}
		loopInfo.Counter = idx + 1
		loopInfo.Counter0 = idx
		if idx == 1 {
			loopInfo.First = false
		}
		if idx+1 == count {
			loopInfo.Last = true
		}
		loopInfo.setRevcounter(count, idx)

		// Render elements with updated context
		err := node.bodyWrapper.Execute(forCtx, writer)
		if errors.Is(err, errLoopBreak) {
			return false
		}
		if err != nil && !errors.Is(err, errLoopContinue) {
			forError = err
			return false
		}
		return true
	}, func() {
		// Nothing to iterate over (maybe wrong type or no items)
		if node.emptyWrapper != nil {
			err := node.emptyWrapper.Execute(forCtx, writer)
			if err != nil {
				forError = err
			}
		}
	}, node.reversed, node.sorted)

	return forError
}

// executeIterator lazily consumes an Iterator. It pulls at most one item
// ahead (to compute forloop.Last) and closes the cursor exactly once,
// however the loop is left.
func (node *tagForNode) executeIterator(
	forCtx *ExecutionContext,
	writer TemplateWriter,
	loopInfo *tagForLoopInformation,
	it Iterator,
) (forError error) {
	if node.value != "" {
		return forCtx.Error("Key, value iteration is not supported for iterators.", nil)
	}
	if node.sorted {
		return forCtx.Error("'sorted' is not supported for iterators (it would require consuming all items).", nil)
	}
	if node.reversed {
		reversible, ok := it.(ReversibleIterator)
		if !ok {
			return forCtx.Error("'reversed' is not supported by this iterator.", nil)
		}
		reversed, err := reversible.Reverse()
		if err != nil {
			return err
		}
		it = reversed
	}

	// The cursor obtained by this loop is closed exactly once: on normal
	// end, break, template errors and output writer failures alike.
	closed := false
	defer func() {
		if closed {
			return
		}
		closed = true
		if err := it.Close(); err != nil && forError == nil {
			forError = err
		}
	}()

	// Length-dependent counters (revcounter) are only available when the
	// iterator declares its length; the source is never drained to find out.
	length := -1
	if sized, ok := it.(SizedIterator); ok {
		length = sized.Len()
	}

	item, ok, err := it.Next()
	if err != nil {
		return err
	}
	if !ok {
		// Empty iterator: render the empty branch (if present).
		if node.emptyWrapper != nil {
			return node.emptyWrapper.Execute(forCtx, writer)
		}
		return nil
	}

	for idx := 0; ; idx++ {
		// Look ahead by at most one item so forloop.Last can be reported
		// without draining the source.
		next, hasNext, err := it.Next()
		if err != nil {
			return err
		}

		forCtx.Private[node.key] = asIteratorValue(item)
		loopInfo.Counter = idx + 1
		loopInfo.Counter0 = idx
		loopInfo.First = idx == 0
		loopInfo.Last = !hasNext
		if length >= 0 {
			loopInfo.setRevcounter(length, idx)
		}

		err = node.bodyWrapper.Execute(forCtx, writer)
		if errors.Is(err, errLoopBreak) {
			return nil
		}
		if err != nil && !errors.Is(err, errLoopContinue) {
			return err
		}

		if !hasNext {
			return nil
		}
		item = next
	}
}

// tagForParser parses the {% for %} tag. It supports key/value iteration,
// "in" keyword, and optional "reversed" and "sorted" modifiers.
func tagForParser(doc *Parser, start *Token, arguments *Parser) (INodeTag, error) {
	forNode := &tagForNode{}

	// Arguments parsing
	var valueToken *Token
	keyToken := arguments.MatchType(TokenIdentifier)
	if keyToken == nil {
		return nil, arguments.Error("Expected an key identifier as first argument for 'for'-tag", nil)
	}

	if arguments.Match(TokenSymbol, ",") != nil {
		// Value name is provided
		valueToken = arguments.MatchType(TokenIdentifier)
		if valueToken == nil {
			return nil, arguments.Error("Value name must be an identifier.", nil)
		}
	}

	if arguments.Match(TokenKeyword, "in") == nil {
		return nil, arguments.Error("Expected keyword 'in'.", nil)
	}

	objectEvaluator, err := arguments.ParseExpression()
	if err != nil {
		return nil, err
	}
	forNode.objectEvaluator = objectEvaluator
	forNode.key = keyToken.Val
	if valueToken != nil {
		forNode.value = valueToken.Val
	}

	if arguments.MatchOne(TokenIdentifier, "reversed") != nil {
		forNode.reversed = true
	}

	if arguments.MatchOne(TokenIdentifier, "sorted") != nil {
		forNode.sorted = true
	}

	if arguments.Remaining() > 0 {
		return nil, arguments.Error("Malformed for-loop arguments.", nil)
	}

	// Body wrapping
	wrapper, endargs, err := doc.WrapUntilTag("empty", "endfor")
	if err != nil {
		return nil, err
	}
	forNode.bodyWrapper = wrapper

	if endargs.Count() > 0 {
		return nil, endargs.Error("Arguments not allowed here.", nil)
	}

	if wrapper.Endtag == "empty" {
		// if there's an else in the if-statement, we need the else-Block as well
		wrapper, endargs, err = doc.WrapUntilTag("endfor")
		if err != nil {
			return nil, err
		}
		forNode.emptyWrapper = wrapper

		if endargs.Count() > 0 {
			return nil, endargs.Error("Arguments not allowed here.", nil)
		}
	}

	return forNode, nil
}

func init() {
	mustRegisterTag("for", tagForParser)
}
