package pongo2

import (
	"bytes"
	"fmt"
)

// pendingCallerKey is the private context key a {% call %} tag uses to hand
// its block (as a callback) to the macro it invokes. It is not a valid
// template identifier, so templates cannot set or read it directly.
const pendingCallerKey = "pongo2:pending-caller"

// tagCallNode represents the {% call %} tag.
//
// The call tag invokes a macro (or any callable expression) and passes the
// block body as a callback. Inside the macro, the callback is available as
// the special variable "caller". When the macro calls caller(...), the block
// body is rendered with the given arguments bound to the declared parameters.
//
// Basic usage (a macro rendering the table frame, the caller providing rows):
//
//	{% macro render_table(items) %}
//	    <table>
//	    {% for item in items %}{{ caller(item) }}{% endfor %}
//	    </table>
//	{% endmacro %}
//
//	{% call(item) render_table(users) %}
//	    <tr><td>{{ item.name }}</td></tr>
//	{% endcall %}
//
// A parameterless block omits the parameter list:
//
//	{% call render_dialog("Confirm") %}
//	    Are you sure?
//	{% endcall %}
//
// Scoping rules:
//   - The block body sees the variables of the context where the {% call %}
//     tag is declared, not the macro's local (e.g. same-named) variables.
//   - The caller parameters are bound only for the duration of that single
//     callback invocation.
//   - The macro may invoke caller multiple times; each invocation re-binds
//     the parameters.
//   - Callback invocations count towards the recursive macro call depth
//     limit (maxMacroDepth), just like regular macro calls.
type tagCallNode struct {
	position *Token
	params   []string
	callExpr IEvaluator

	wrapper *NodeWrapper
}

// Execute hands the block to the callee as "caller" and writes the callee's
// (usually the macro's) rendered output.
func (node *tagCallNode) Execute(ctx *ExecutionContext, writer TemplateWriter) error {
	callCtx := NewChildExecutionContext(ctx)
	callCtx.Private[pendingCallerKey] = &tagCallerValue{node: node, ctx: ctx}

	value, err := node.callExpr.Evaluate(callCtx)
	if err != nil {
		return err
	}

	if !value.safe && value.IsString() && ctx.Autoescape {
		// apply escape filter (same as for {{ ... }} output)
		escapeFn := ctx.template.set.filters["escape"]
		if escapeFn != nil {
			value, err = escapeFn(value, nil)
			if err != nil {
				return err
			}
		}
	}

	_, err = writer.WriteString(value.String())
	return err
}

// tagCallerValue is the not-yet-bound callback a {% call %} tag passes to the
// macro it invokes. tagMacroNode.call promotes it to a callable "caller".
type tagCallerValue struct {
	node *tagCallNode
	ctx  *ExecutionContext // declaration-site context of the {% call %} block
}

// macroCallerFunc is the callable bound to "caller" inside a macro invoked
// through {% call %}. The dedicated type lets macro calls recognize (and not
// leak) callbacks of nested macro invocations.
type macroCallerFunc func(args ...*Value) (*Value, error)

// bind turns the callback into a callable bound to the given macro, so errors
// can reference both the macro and the {% call %} block positions.
func (c *tagCallerValue) bind(macro *tagMacroNode) macroCallerFunc {
	return func(args ...*Value) (*Value, error) {
		return c.invoke(macro, args)
	}
}

// invoke renders the {% call %} block body with the given arguments bound to
// the declared parameters. The body executes in a fresh child of the
// declaration-site context, so it sees the locals of the {% call %} site and
// none of the macro's local variables; parameter bindings do not outlive this
// single invocation.
func (c *tagCallerValue) invoke(macro *tagMacroNode, args []*Value) (*Value, error) {
	c.ctx.macroDepth++
	defer func() {
		c.ctx.macroDepth--
	}()

	if c.ctx.macroDepth > maxMacroDepth {
		return nil, c.ctx.Error(fmt.Sprintf("maximum recursive macro call depth reached (max is %v)", maxMacroDepth), c.node.position)
	}

	if len(args) != len(c.node.params) {
		return nil, &Error{
			Template: c.ctx.template,
			Filename: c.node.position.Filename,
			Line:     c.node.position.Line,
			Column:   c.node.position.Col,
			Token:    c.node.position,
			Sender:   "execution",
			OrigError: fmt.Errorf(
				"callback of macro '%s' takes %d argument(s), but %d were given "+
					"(callback declared at %s, macro '%s' defined at %s)",
				macro.name, len(c.node.params), len(args),
				formatCallTokenPosition(c.node.position), macro.name, formatCallTokenPosition(macro.position)),
		}
	}

	callCtx := NewChildExecutionContext(c.ctx)
	for idx, name := range c.node.params {
		callCtx.Private[name] = args[idx].Interface()
	}

	var b bytes.Buffer
	if err := c.node.wrapper.Execute(callCtx, &b); err != nil {
		return nil, err
	}

	return AsSafeValue(b.String()), nil
}

// formatCallTokenPosition formats a token position as "file:line:col".
func formatCallTokenPosition(t *Token) string {
	if t == nil {
		return "<unknown>"
	}
	return fmt.Sprintf("%s:%d:%d", t.Filename, t.Line, t.Col)
}

// tagCallParser parses the {% call %} tag: an optional parameter list in
// parentheses, followed by the expression to call (usually a macro call),
// and the block body up to {% endcall %}.
func tagCallParser(doc *Parser, start *Token, arguments *Parser) (INodeTag, error) {
	callNode := &tagCallNode{
		position: start,
	}

	// Optional parameter list: {% call(a, b) ... %}
	if arguments.Peek(TokenSymbol, "(") != nil {
		arguments.Consume() // consume "("

		seen := make(map[string]bool)
		for arguments.Match(TokenSymbol, ")") == nil {
			paramToken := arguments.MatchType(TokenIdentifier)
			if paramToken == nil {
				return nil, arguments.Error("Expected parameter name as identifier.", nil)
			}
			if seen[paramToken.Val] {
				return nil, arguments.Error(fmt.Sprintf("Duplicate parameter name '%s'.", paramToken.Val), paramToken)
			}
			seen[paramToken.Val] = true
			callNode.params = append(callNode.params, paramToken.Val)

			if arguments.Match(TokenSymbol, ")") != nil {
				break
			}
			if arguments.Match(TokenSymbol, ",") == nil {
				return nil, arguments.Error("Expected ',' or ')'.", nil)
			}
		}
	}

	// The expression to call, e.g. render_table(users)
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

func init() {
	mustRegisterTag("call", tagCallParser)
}
