package pongo2

import (
	"errors"
	"fmt"
)

// loopControlSignal is raised by the {% break %} and {% continue %} tags and
// unwinds the node execution until the innermost enclosing for-tag catches
// it. If it escapes all for-tags (which the parser normally prevents), it
// surfaces to the caller of Template.Execute as a regular error.
type loopControlSignal struct {
	isBreak bool
	token   *Token
}

func (s *loopControlSignal) Error() string {
	return fmt.Sprintf("{%% %s %%} used outside of a for loop", s.keyword())
}

func (s *loopControlSignal) keyword() string {
	if s.isBreak {
		return "break"
	}
	return "continue"
}

// isLoopControl reports whether err carries a loop-control signal of the
// requested kind (isBreak == true for {% break %}, false for {% continue %}).
func isLoopControl(err error, isBreak bool) bool {
	var s *loopControlSignal
	if errors.As(err, &s) {
		return s.isBreak == isBreak
	}
	return false
}

// tagBreakNode represents the {% break %} tag. It aborts the innermost
// enclosing for loop, skipping all remaining items.
type tagBreakNode struct {
	token *Token
}

func (node *tagBreakNode) Execute(ctx *ExecutionContext, writer TemplateWriter) error {
	return &loopControlSignal{isBreak: true, token: node.token}
}

// tagContinueNode represents the {% continue %} tag. It skips the rest of
// the current iteration of the innermost enclosing for loop.
type tagContinueNode struct {
	token *Token
}

func (node *tagContinueNode) Execute(ctx *ExecutionContext, writer TemplateWriter) error {
	return &loopControlSignal{isBreak: false, token: node.token}
}

// tagLoopControlParser parses {% break %} and {% continue %}. Both take no
// arguments and are only allowed inside the body of a {% for %} tag.
func tagLoopControlParser(isBreak bool) TagParser {
	keyword := "continue"
	if isBreak {
		keyword = "break"
	}
	return func(doc *Parser, start *Token, arguments *Parser) (INodeTag, error) {
		if doc.forDepth == 0 {
			return nil, arguments.Error(fmt.Sprintf("'%s' can only be used inside a for loop.", keyword), start)
		}
		if arguments.Remaining() > 0 {
			return nil, arguments.Error(fmt.Sprintf("No arguments allowed for '%s'.", keyword), nil)
		}
		if isBreak {
			return &tagBreakNode{token: start}, nil
		}
		return &tagContinueNode{token: start}, nil
	}
}

func init() {
	mustRegisterTag("break", tagLoopControlParser(true))
	mustRegisterTag("continue", tagLoopControlParser(false))
}
