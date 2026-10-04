package pongo2

// tagForNode represents the {% for %} tag.
//
// The for tag loops over each item in a sequence (slice, array, map, or string).
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
//   - forloop.Revcounter: Iterations remaining (1-indexed); NIL for lazy
//     iterators that do not announce their length (see SizedIterator)
//   - forloop.Revcounter0: Iterations remaining (0-indexed); NIL likewise
//   - forloop.First: True if this is the first iteration
//   - forloop.Last: True if this is the last iteration (computed for lazy
//     iterators through at most one item of lookahead)
//   - forloop.Parentloop: Access parent loop in nested loops
//
// The object can also be a lazy Iterator (e.g. a database cursor for a mail
// export). It is consumed item by item without being materialized into a
// slice first, and it is closed exactly once when the loop ends, is left via
// {% break %}, or aborts with an error. Reversed iteration requires the
// iterator to implement ReversibleIterator.
//
// The {% break %} and {% continue %} tags leave or skip the current
// iteration of the innermost enclosing for loop.
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
//
// Revcounter and Revcounter0 are ints when the total length of the looped
// object is known (slices, arrays, maps, strings, SizedIterator) and NIL
// otherwise (lazy iterators of unknown length).
type tagForLoopInformation struct {
	Counter     int
	Counter0    int
	Revcounter  any
	Revcounter0 any
	First       bool
	Last        bool
	Parentloop  *tagForLoopInformation
}

// Execute iterates over the object and renders the body for each item.
// If the object is empty, it renders the empty wrapper (if present).
func (node *tagForNode) Execute(ctx *ExecutionContext, writer TemplateWriter) (forError error) {
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

	// Lazy iterators (e.g. cursors over large exports) are streamed item by
	// item instead of being walked through reflection.
	if it, ok := asIterator(obj); ok {
		return node.executeIterator(forCtx, writer, loopInfo, it)
	}

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
		loopInfo.Revcounter = count - idx
		loopInfo.Revcounter0 = count - (idx + 1)

		// Render elements with updated context
		err := node.bodyWrapper.Execute(forCtx, writer)
		if err != nil {
			if isLoopControl(err, false) {
				// {% continue %}: skip to the next item
				return true
			}
			if isLoopControl(err, true) {
				// {% break %}: leave the loop without an error
				return false
			}
			forError = err
			return false
		}
		return true
	}, func() {
		// Nothing to iterate over (maybe wrong type or no items)
		if err := node.executeEmpty(forCtx, writer); err != nil {
			forError = err
		}
	}, node.reversed, node.sorted)

	return forError
}

// executeEmpty renders the {% empty %} branch, if present.
func (node *tagForNode) executeEmpty(ctx *ExecutionContext, writer TemplateWriter) error {
	if node.emptyWrapper == nil {
		return nil
	}
	return node.emptyWrapper.Execute(ctx, writer)
}

// executeIterator runs the loop over a lazy Iterator. It pulls at most one
// item ahead (to compute forloop.Last) and closes the iterator exactly once,
// no matter whether the loop finishes normally, is left via {% break %}, or
// aborts with a template or writer error.
func (node *tagForNode) executeIterator(
	forCtx *ExecutionContext,
	writer TemplateWriter,
	loopInfo *tagForLoopInformation,
	it Iterator,
) (forError error) {
	if node.value != "" {
		return forCtx.Error("Key, value unpacking is not supported for iterators.", nil)
	}
	if node.sorted {
		return forCtx.Error("The 'sorted' modifier is not supported for iterators.", nil)
	}
	if node.reversed {
		rev, ok := it.(ReversibleIterator)
		if !ok {
			return forCtx.Error("The 'reversed' modifier requires the iterator to support reversed iteration (ReversibleIterator).", nil)
		}
		rit, err := rev.Reverse()
		if err != nil {
			return err
		}
		if rit == nil {
			return forCtx.Error("Iterator's Reverse() returned nil.", nil)
		}
		// The loop consumes (and closes) the reversed cursor from now on.
		it = rit
	}

	// Close the cursor obtained by this loop exactly once on every exit
	// path: normal end, {% break %}, template error, or writer failure.
	defer func() {
		if closeErr := it.Close(); closeErr != nil && forError == nil {
			forError = closeErr
		}
	}()

	// Length-dependent loop variables are only available when the iterator
	// announces its length; they stay NIL otherwise (no silent draining).
	length, hasLength := 0, false
	if sized, ok := it.(SizedIterator); ok {
		length, hasLength = sized.Len()
	}

	// Prime the one-item lookahead.
	item, ok, err := it.Next()
	if err != nil {
		return err
	}
	if !ok {
		return node.executeEmpty(forCtx, writer)
	}

	for idx := 0; ; idx++ {
		// Look ahead by at most one item to determine forloop.Last.
		next, hasNext, nextErr := it.Next()
		if nextErr != nil {
			return nextErr
		}

		loopInfo.Counter = idx + 1
		loopInfo.Counter0 = idx
		loopInfo.First = idx == 0
		loopInfo.Last = !hasNext
		if hasLength {
			loopInfo.Revcounter = length - idx
			loopInfo.Revcounter0 = length - (idx + 1)
		}

		forCtx.Private[node.key] = AsValue(item)

		err := node.bodyWrapper.Execute(forCtx, writer)
		if err != nil {
			if isLoopControl(err, false) {
				// {% continue %}: skip to the next item
			} else if isLoopControl(err, true) {
				// {% break %}: leave the loop without an error
				return nil
			} else {
				return err
			}
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
	doc.forDepth++
	wrapper, endargs, err := doc.WrapUntilTag("empty", "endfor")
	doc.forDepth--
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
