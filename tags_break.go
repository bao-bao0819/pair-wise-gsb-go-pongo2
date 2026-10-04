package pongo2

import "errors"

// Loop control signals. They are returned by the {% break %} and
// {% continue %} tags and caught by the innermost enclosing {% for %} loop,
// so nested loops only ever affect their own level. Reaching the top level
// (break/continue outside of a for-loop) surfaces them as an error.
var (
	errLoopBreak    = errors.New("break outside of for-loop")
	errLoopContinue = errors.New("continue outside of for-loop")
)

// tagBreakNode represents the {% break %} tag.
//
// The break tag stops the innermost enclosing for-loop immediately:
//
//	{% for item in items %}
//	    {% if item.Done %}{% break %}{% endif %}
//	    {{ item.Name }}
//	{% endfor %}
type tagBreakNode struct{}

// Execute signals the enclosing for-loop to stop iterating.
func (node *tagBreakNode) Execute(ctx *ExecutionContext, writer TemplateWriter) error {
	return errLoopBreak
}

// tagBreakParser parses the {% break %} tag. It takes no arguments.
func tagBreakParser(doc *Parser, start *Token, arguments *Parser) (INodeTag, error) {
	if arguments.Remaining() > 0 {
		return nil, arguments.Error("Arguments not allowed here.", nil)
	}
	return &tagBreakNode{}, nil
}

// tagContinueNode represents the {% continue %} tag.
//
// The continue tag skips the rest of the current iteration of the
// innermost enclosing for-loop:
//
//	{% for item in items %}
//	    {% if item.Hidden %}{% continue %}{% endif %}
//	    {{ item.Name }}
//	{% endfor %}
type tagContinueNode struct{}

// Execute signals the enclosing for-loop to skip to the next item.
func (node *tagContinueNode) Execute(ctx *ExecutionContext, writer TemplateWriter) error {
	return errLoopContinue
}

// tagContinueParser parses the {% continue %} tag. It takes no arguments.
func tagContinueParser(doc *Parser, start *Token, arguments *Parser) (INodeTag, error) {
	if arguments.Remaining() > 0 {
		return nil, arguments.Error("Arguments not allowed here.", nil)
	}
	return &tagContinueNode{}, nil
}

func init() {
	mustRegisterTag("break", tagBreakParser)
	mustRegisterTag("continue", tagContinueParser)
}
