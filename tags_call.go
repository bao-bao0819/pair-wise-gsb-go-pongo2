package pongo2

import (
	"bytes"
	"fmt"
)

// callBlockCallerContextKey is the private context key under which the {% call %}
// tag hands the caller function to the invoked macro. The key is not a valid
// template identifier, so it can never clash with user-provided variables.
// It is deliberately NOT copied into the macro's execution context; the macro
// receives the caller under the plain name "caller" instead. This way the
// caller is only visible to the macro directly invoked by the {% call %} tag
// and never leaks into nested macro calls or the call-block body.
const callBlockCallerContextKey = "\x00pongo2:caller"

// tagCallNode represents the {% call %} tag.
//
// The call tag invokes a macro (or any callable expression) and passes the
// tag's body as a callback - the "caller" - to it. Inside the macro, the
// caller is available as the special variable "caller" and can be called
// like a function. This is useful when a macro provides the outer structure
// (e.g. a table with borders) and the caller provides the inner content
// (e.g. how a single row is rendered).
//
// Basic usage (Jinja2-compatible syntax):
//
//	{% macro table(rows) %}
//	    <table>
//	    {% for row in rows %}
//	        <tr>{{ caller(row) }}</tr>
//	    {% endfor %}
//	    </table>
//	{% endmacro %}
//
//	{% call(item) table(items) %}
//	    <td>{{ item.name }}</td>
//	{% endcall %}
//
// The optional parenthesized list right after "call" declares the parameters
// the caller accepts; the macro supplies their values with each {{ caller(...) }}
// invocation. The list may be omitted entirely if the caller takes no arguments:
//
//	{% call show_banner() %}...body...{% endcall %}
//
// Scoping rules:
//   - The body reads variables from the place where the {% call %} tag is
//     declared (e.g. surrounding loop variables), not from the macro.
//   - Caller parameters are bound only for the duration of that single
//     caller invocation and shadow same-named outer variables.
//   - Temporary variables of the macro are never visible inside the body.
//
// The caller may be invoked multiple times by the macro, {% call %} blocks
// may be nested, and macros imported via {% import %} can call back as well.
// Each macro -> caller edge counts towards the recursive macro depth limit.
type tagCallNode struct {
	position     *Token
	callerParams []string
	callExpr     IEvaluator

	wrapper *NodeWrapper
}

// Execute invokes the call expression with the caller available and writes
// the produced output.
func (node *tagCallNode) Execute(ctx *ExecutionContext, writer TemplateWriter) error {
	callCtx := NewChildExecutionContext(ctx)
	callCtx.Private[callBlockCallerContextKey] = node.makeCaller(ctx)

	result, err := node.callExpr.Evaluate(callCtx)
	if err != nil {
		return err
	}

	if !result.safe && result.IsString() && ctx.Autoescape {
		// apply escape filter (same behavior as {{ ... }} output)
		escapeFn := ctx.template.set.filters["escape"]
		if escapeFn != nil {
			result, err = escapeFn(result, nil)
			if err != nil {
				return err
			}
		}
	}

	_, err = writer.WriteString(result.String())
	return err
}

// makeCaller builds the caller function passed to the macro. The returned
// closure renders the {% call %} body in a child of the declaration-site
// context, so it sees the locals of the declaration site but never the
// macro's own temporary variables.
func (node *tagCallNode) makeCaller(declCtx *ExecutionContext) func(...*Value) (*Value, error) {
	return func(args ...*Value) (*Value, error) {
		// The macro -> caller edge counts towards the recursion depth limit.
		*declCtx.macroDepth++
		defer func() {
			*declCtx.macroDepth--
		}()

		if *declCtx.macroDepth > maxMacroDepth {
			return nil, declCtx.Error(fmt.Sprintf("maximum recursive macro call depth reached (max is %v)", maxMacroDepth), node.position)
		}

		if len(args) != len(node.callerParams) {
			// Report the position of the {% call %} tag here; the position
			// of the caller(...) invocation inside the macro is attached by
			// the expression evaluator while this error propagates.
			return nil, fmt.Errorf("wrong number of arguments for call-block: takes %d, given %d "+
				"(call-block defined at %s:%d:%d)",
				len(node.callerParams), len(args),
				node.position.Filename, node.position.Line, node.position.Col)
		}

		bodyCtx := NewChildExecutionContext(declCtx)
		for idx, name := range node.callerParams {
			bodyCtx.Private[name] = args[idx].Interface()
		}

		var b bytes.Buffer
		if err := node.wrapper.Execute(bodyCtx, &b); err != nil {
			return AsSafeValue(""), err
		}

		return AsSafeValue(b.String()), nil
	}
}

// tagCallParser parses the {% call %} tag. It accepts an optional caller
// parameter list in parentheses, followed by the call expression, and wraps
// the body up to {% endcall %}.
func tagCallParser(doc *Parser, start *Token, arguments *Parser) (INodeTag, error) {
	callNode := &tagCallNode{
		position: start,
	}

	// Optional caller parameter list: {% call(param1, param2) ... %}
	if arguments.Peek(TokenSymbol, "(") != nil {
		arguments.Consume() // consume "("
		if err := callNode.parseCallerParams(arguments); err != nil {
			return nil, err
		}
	}

	if arguments.Remaining() == 0 {
		return nil, arguments.Error("Call-tag needs a call expression (e.g. {% call my_macro() %}).", nil)
	}

	callExpr, err := arguments.ParseExpression()
	if err != nil {
		return nil, err
	}
	callNode.callExpr = callExpr

	if arguments.Remaining() > 0 {
		return nil, arguments.Error("Malformed call-tag.", nil)
	}

	// Body wrapping
	wrapper, endargs, err := doc.WrapUntilTag("endcall")
	if err != nil {
		return nil, err
	}
	callNode.wrapper = wrapper

	if endargs.Count() > 0 {
		return nil, endargs.Error("Arguments not allowed here.", nil)
	}

	return callNode, nil
}

// parseCallerParams parses the caller parameter list after the opening "("
// has been consumed: zero or more comma-separated identifiers followed by ")".
func (node *tagCallNode) parseCallerParams(arguments *Parser) error {
	seen := make(map[string]bool)

	for arguments.Match(TokenSymbol, ")") == nil {
		paramToken := arguments.MatchType(TokenIdentifier)
		if paramToken == nil {
			return arguments.Error("Expected caller parameter name as identifier.", nil)
		}
		if seen[paramToken.Val] {
			return arguments.Error(fmt.Sprintf("Duplicate caller parameter name '%s'.", paramToken.Val), paramToken)
		}
		seen[paramToken.Val] = true
		node.callerParams = append(node.callerParams, paramToken.Val)

		if arguments.Match(TokenSymbol, ")") != nil {
			break
		}
		if arguments.Match(TokenSymbol, ",") == nil {
			return arguments.Error("Expected ',' or ')'.", nil)
		}
	}

	return nil
}

func init() {
	mustRegisterTag("call", tagCallParser)
}
