package pongo2

import "fmt"

// contextAnalyzer walks the parsed node tree of the executed template with a
// single htmlScanner. Every variable output is assigned the escape kind of
// the place where it lands; places that cannot be decided statically produce
// a compile error at the responsible template position.
//
// Control flow is handled as a merge problem:
//   - if/elif/else: every branch is analyzed from the entry state and all
//     reachable branch ends must converge on the same state (a missing else
//     means the entry state is also a possible continuation).
//   - for: the body is executed zero or more times, so it must start and end
//     in the same state; the empty clause is merged with that state.
//   - static include: the included template is analyzed from the entry
//     state as its own frame (per include site) and its end state continues.
//   - extends/block: the executed root template is analyzed, and every block
//     in the inheritance chain is analyzed at its use position.
type contextAnalyzer struct {
	scanner htmlScanner
	frame   *contextualFrame
	root    *Template
}

// analyzeTemplateRoot runs contextual analysis over the template that will
// actually be executed (walking extends to the root) and stores the resulting
// frame on that root. Called at the end of parsing the top-level template.
func analyzeTemplateRoot(tpl *Template) error {
	root := tpl
	for root.parent != nil {
		root = root.parent
	}

	a := &contextAnalyzer{
		frame: newContextualFrame(),
		root:  root,
	}
	if err := a.walkNodes(root.root.Nodes); err != nil {
		return err
	}
	if err := a.scanner.finishCheck(); err != nil {
		return err
	}
	root.contextualFrame = a.frame
	return nil
}

// walkNodeList analyzes a linear sequence of nodes, mutating the scanner.
func (a *contextAnalyzer) walkNodes(nodes []INode) error {
	for _, n := range nodes {
		if err := a.walkNode(n); err != nil {
			return err
		}
	}
	return nil
}

func (a *contextAnalyzer) walkNode(n INode) error {
	switch node := n.(type) {
	case *nodeHTML:
		return a.feedHTMLNode(node)
	case *nodeVariable:
		return a.assignSlot(node, node.locationToken)

	case *tagIfNode:
		return a.walkIf(node)
	case *tagForNode:
		return a.walkFor(node)
	case *tagIfEqualNode:
		return a.walkBranchPair(node.thenWrapper, node.elseWrapper, node.var1.GetPositionToken())
	case *tagIfNotEqualNode:
		return a.walkBranchPair(node.thenWrapper, node.elseWrapper, node.var1.GetPositionToken())
	case *tagIfchangedNode:
		return a.walkIfchanged(node)
	case *tagWithNode:
		return nil
	default:
		return a.walkTagNode(n)
	}
}

// walkTagNode handles every tag not covered by the inline control-flow group
// in walkNode: transparent wrappers, composition tags, output tags and tags
// that are explicitly rejected in contextual mode.
func (a *contextAnalyzer) walkTagNode(n INode) error {
	switch node := n.(type) {
	case *tagWithNode, *tagSpacelessNode, *tagAutoescapeNode:
		return a.walkWrapperOf(node)
	case *tagBlockNode:
		return a.walkBlock(node)
	case *tagIncludeNode:
		return a.walkInclude(node)
	case *tagExtendsNode, *tagCommentNode:
		return nil
	case *tagFirstofNode:
		return a.assignSlot(node, node.position)
	case *tagCycleNode:
		if node.silent {
			return nil
		}
		return a.assignSlot(node, node.position)
	case *tagMacroNode:
		return a.walkMacro(node)
	case *tagSetNode, *tagNowNode, *tagWidthratioNode, *tagTemplateTagNode,
		*tagLoremNode, *tagImportNode:
		return nil
	case *tagSSINode:
		return a.unsupported(n, "the 'ssi' tag is not supported in contextual escaping mode (use 'include' with static templates)")
	case *tagFilterNode:
		return a.unsupported(n, "the 'filter' block tag is not supported in contextual escaping mode (filters may change HTML/script structure)")
	default:
		return a.unsupported(n, "this tag is not supported in contextual escaping mode (the output context cannot be determined statically)")
	}
}

// wrapperNode returns the NodeWrapper carried by a transparent wrapper tag.
func (a *contextAnalyzer) walkWrapperOf(n INode) error {
	switch node := n.(type) {
	case *tagWithNode:
		return a.walkNodes(node.wrapper.nodes)
	case *tagSpacelessNode:
		return a.walkNodes(node.wrapper.nodes)
	case *tagAutoescapeNode:
		return a.walkNodes(node.wrapper.nodes)
	}
	return nil
}

// walkMacro analyzes a macro body as an HTML-text-neutral fragment. Its
// return value is escaped again at the call site, so the definition must not
// change the surrounding scanner state.
func (a *contextAnalyzer) walkMacro(node *tagMacroNode) error {
	child := &contextAnalyzer{frame: newContextualFrame(), root: a.root}
	return child.walkNodes(node.wrapper.nodes)
}

func (a *contextAnalyzer) feedHTMLNode(node *nodeHTML) error {
	text := node.token.Val
	if node.trimLeft {
		text = trimLeftSpace(text)
	}
	if node.trimRight {
		text = trimRightSpace(text)
	}
	a.scanner.feedText(text)
	return nil
}

func trimLeftSpace(s string) string {
	for len(s) > 0 && isSpaceByte(s[0]) {
		s = s[1:]
	}
	return s
}

func trimRightSpace(s string) string {
	for len(s) > 0 && isSpaceByte(s[len(s)-1]) {
		s = s[:len(s)-1]
	}
	return s
}

// assignSlot records the context at the current position for node.
func (a *contextAnalyzer) assignSlot(node INode, token *Token) error {
	kind, ok, why := a.scanner.feedSlot()
	if !ok {
		return contextualError(token, why)
	}
	a.frame.slots[node] = kind
	return nil
}

// walkIf analyzes every branch from the same entry state and requires all
// possible continuation states to be identical.
func (a *contextAnalyzer) walkIf(node *tagIfNode) error {
	entry := a.scanner.snapshot()

	ends := make([]scannerSnapshot, 0, len(node.wrappers))
	for _, wrapper := range node.wrappers {
		a.scanner.restore(entry)
		if err := a.walkNodes(wrapper.nodes); err != nil {
			return err
		}
		ends = append(ends, a.scanner.snapshot())
	}

	// A missing else makes "no branch executed" (== entry) a continuation.
	if len(node.conditions) < len(node.wrappers) {
		ends = append(ends, entry)
	}

	for _, end := range ends[1:] {
		if !branchesConverge(end, ends[0]) {
			return contextualError(ifPosition(node),
				"if branches do not converge on the same context: one branch leaves an unclosed tag, attribute or quoted string")
		}
	}
	a.scanner.restore(ends[0])
	return nil
}

func ifPosition(node *tagIfNode) *Token {
	if len(node.conditions) > 0 {
		return node.conditions[0].GetPositionToken()
	}
	return nil
}

// branchesConverge reports whether two branch ends represent the same
// continuation context. Full snapshot equality is required, including the
// cumulative open/close counts: a branch that closes an outer quote and opens
// a new one leaves an equal instantaneous depth but is still wrong.
func branchesConverge(a, b scannerSnapshot) bool {
	return a == b
}

// loopConverges reports whether a loop body can repeat from the entry state.
// Balanced open/close pairs are allowed (they simply repeat each iteration),
// so only the continuation state and the depth delta must match.
func loopConverges(entry, end scannerSnapshot) bool {
	if end.state != entry.state || end.depth != entry.depth {
		return false
	}
	return end.depth-entry.depth == (end.opened-end.closed)-(entry.opened-entry.closed)
}

// walkBranchPair analyzes then/else wrappers for ifequal/ifnotequal. A
// missing else means the entry state is a possible continuation.
func (a *contextAnalyzer) walkBranchPair(thenWrapper, elseWrapper *NodeWrapper, pos *Token) error {
	entry := a.scanner.snapshot()

	wrappers := []*NodeWrapper{thenWrapper}
	if elseWrapper != nil {
		wrappers = append(wrappers, elseWrapper)
	} else {
		wrappers = append(wrappers, nil) // entry state continuation
	}

	ends := make([]scannerSnapshot, 0, len(wrappers))
	for _, wrapper := range wrappers {
		a.scanner.restore(entry)
		if wrapper != nil {
			if err := a.walkNodes(wrapper.nodes); err != nil {
				return err
			}
		}
		ends = append(ends, a.scanner.snapshot())
	}

	for _, end := range ends[1:] {
		if !branchesConverge(end, ends[0]) {
			return contextualError(pos,
				"conditional branches do not converge on the same context: one branch leaves an unclosed tag, attribute or quoted string")
		}
	}
	a.scanner.restore(ends[0])
	return nil
}

// walkIfchanged handles {% ifchanged %} whose then wrapper always starts at
// the entry state and whose optional else wrapper is a second continuation.
func (a *contextAnalyzer) walkIfchanged(node *tagIfchangedNode) error {
	return a.walkBranchPair(node.thenWrapper, node.elseWrapper, nodePosition(node))
}

// walkFor requires the loop body to return to its entry state (the body runs
// zero or more times) and merges the optional empty clause with that state.
func (a *contextAnalyzer) walkFor(node *tagForNode) error {
	entry := a.scanner.snapshot()

	a.scanner.restore(entry)
	if err := a.walkNodes(node.bodyWrapper.nodes); err != nil {
		return err
	}
	end := a.scanner.snapshot()
	if !loopConverges(entry, end) {
		return contextualError(node.objectEvaluator.GetPositionToken(),
			"loop body does not return to the context it started in (an unclosed tag, attribute or quoted string spans the loop boundary)")
	}

	if node.emptyWrapper != nil {
		a.scanner.restore(entry)
		if err := a.walkNodes(node.emptyWrapper.nodes); err != nil {
			return err
		}
		if end := a.scanner.snapshot(); !loopConverges(entry, end) {
			return contextualError(node.objectEvaluator.GetPositionToken(),
				"empty branch does not converge with the loop entry context")
		}
	}

	a.scanner.restore(entry)
	return nil
}

// walkBlock analyzes the most-derived block and every wrapper in the
// inheritance chain at the block's use position, since block.Super() can
// render parent content at that same position.
func (a *contextAnalyzer) walkBlock(node *tagBlockNode) error {
	entry := a.scanner.snapshot()

	wrappers := node.getBlockWrappers(a.root)
	if len(wrappers) == 0 {
		return fmt.Errorf("contextual escaping: internal error, block %q not found", node.name)
	}

	for _, wrapper := range wrappers {
		a.scanner.restore(entry)
		if err := a.walkNodes(wrapper.nodes); err != nil {
			return err
		}
		if end := a.scanner.snapshot(); !branchesConverge(end, entry) {
			return contextualError(nil,
				fmt.Sprintf("block %q and its overrides do not converge on the same context as the parent block", node.name))
		}
	}

	a.scanner.restore(entry)
	return nil
}

// walkInclude analyzes a static include as a fresh child frame, starting the
// included content at the current state and continuing with its end state.
func (a *contextAnalyzer) walkInclude(node *tagIncludeNode) error {
	if node.lazy {
		return contextualError(nil,
			"dynamic (non-static) 'include' is not supported in contextual escaping mode (the included content is not known at compile time)")
	}
	if node.tpl == nil {
		// Missing include with if_exists: renders nothing.
		return nil
	}
	if node.tpl.parent != nil {
		return contextualError(nil,
			"including a template that uses 'extends' is not supported in contextual escaping mode")
	}

	child := &contextAnalyzer{
		frame: newContextualFrame(),
		root:  node.tpl,
	}
	child.scanner.restore(a.scanner.snapshot())
	if err := child.walkNodes(node.tpl.root.Nodes); err != nil {
		return err
	}

	// Re-annotate the included template as if it were rendered from this
	// include site. Its own frame (node.tpl.contextualFrame) is what runtime
	// execution pushes, so nested static includes chain from that frame.
	node.tpl.contextualFrame = child.frame
	a.scanner.restore(child.scanner.snapshot())
	return nil
}

func (a *contextAnalyzer) unsupported(n INode, msg string) error {
	return contextualError(nodePosition(n), msg)
}

// nodePosition extracts a token for error location when the node provides one.
func nodePosition(n INode) *Token {
	type positioned interface {
		GetPositionToken() *Token
	}
	if p, ok := n.(positioned); ok {
		return p.GetPositionToken()
	}
	return nil
}

func contextualError(token *Token, msg string) error {
	err := &Error{
		Sender:    "contextual",
		OrigError: fmt.Errorf("%s", msg),
	}
	if token != nil {
		err.Filename = token.Filename
		err.Line = token.Line
		err.Column = token.Col
		err.Token = token
	}
	return err
}
