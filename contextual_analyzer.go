package pongo2

// contextualAnalyzer walks a compiled template AST while driving a ctxScanner.
// It assigns an escapeKind to every output node ("assign" mode) or verifies an
// already-assigned tree against a different entry state ("verify" mode).
//
// Enforced invariants:
//   - if/elif/else (and ifequal/ifnotequal/ifchanged) branches must merge
//     into an identical lexical state;
//   - for-loop bodies and their empty clause must return to the entry state;
//   - balanced wrappers (block definitions, macros, include targets) must
//     return to their entry state;
//   - static {% include %} targets participate with their actual entry state;
//     dynamic includes are rejected;
//   - the effective (most-derived) {% block %} override is re-checked at the
//     ancestor block tag position so {% extends %} composition participates.
type contextualAnalyzer struct {
	scanner *ctxScanner
	verify  bool
}

func newAnalyzer(verify bool) *contextualAnalyzer {
	return &contextualAnalyzer{scanner: newCtxScanner(), verify: verify}
}

// analyzeTemplate is called once when a template is compiled in contextual
// mode. Entry state is HTML text and the final state must be HTML text too.
func analyzeTemplate(tpl *Template) error {
	a := newAnalyzer(false)
	if err := a.walkNodes(tpl.root.Nodes); err != nil {
		return err
	}
	return a.scanner.finish(lastNodeToken(tpl.root.Nodes))
}

// verifyInheritanceChain re-checks effective block overrides for a template
// that extends a parent. For each block tag encountered while walking an
// ancestor, the most-derived wrapper in the inheritance chain is analyzed at
// that exact position (verify mode for the chain prefix, assign/balance for
// the wrapper itself).
func verifyInheritanceChain(child *Template) error {
	root := child
	for root.parent != nil {
		root = root.parent
	}
	return verifyTemplateBlocks(root, child)
}

// verifyTemplateBlocks walks ancestor's nodes starting from HTML text; at a
// block tag it substitutes the most-derived wrapper (from child down to
// ancestor) and requires the wrapper to preserve the block tag's entry state.
func verifyTemplateBlocks(ancestor, child *Template) error {
	a := newAnalyzer(true)
	return a.walkChainNodes(ancestor, child)
}

func (a *contextualAnalyzer) walkChainNodes(ancestor, child *Template) error {
	for _, node := range ancestor.root.Nodes {
		if err := a.walkChainNode(node, ancestor, child); err != nil {
			return err
		}
	}
	return a.scanner.finish(lastNodeToken(ancestor.root.Nodes))
}

func (a *contextualAnalyzer) walkChainNode(node INode, ancestor, child *Template) error {
	switch n := node.(type) {
	case *nodeHTML:
		return a.scanner.scanChunk(n.token.Val, n.token)

	case *nodeVariable:
		return a.assign(n.locationToken, &n.escapeKind)

	case *tagBlockNode:
		effective := mostDerivedBlock(child, n.name)
		if effective == nil {
			effective = n.wrapper
		}
		return a.walkBlock(effective)

	case *tagIncludeNode:
		// Includes were verified when their owning template was compiled.
		return a.walkInclude(n)

	default:
		// Control structures containing blocks must still preserve state.
		return a.walkChainGeneric(node, ancestor, child)
	}
}

// walkChainGeneric handles nested wrappers/tags in the ancestor, substituting
// effective blocks recursively.
func (a *contextualAnalyzer) walkChainGeneric(node INode, ancestor, child *Template) error {
	switch n := node.(type) {
	case *tagIfNode:
		return a.walkChainBranches(n.wrappers, ancestor, child)
	case *tagIfEqualNode:
		return a.walkChainBranches(optionalWrappers(n.thenWrapper, n.elseWrapper), ancestor, child)
	case *tagIfNotEqualNode:
		return a.walkChainBranches(optionalWrappers(n.thenWrapper, n.elseWrapper), ancestor, child)
	case *tagIfchangedNode:
		return a.walkChainBranches(optionalWrappers(n.thenWrapper, n.elseWrapper), ancestor, child)
	case *tagForNode:
		if err := a.walkChainLoop(n.bodyWrapper, ancestor, child, false); err != nil {
			return err
		}
		if n.emptyWrapper != nil {
			if err := a.walkChainLoop(n.emptyWrapper, ancestor, child, true); err != nil {
				return err
			}
		}
		return nil
	case *tagWithNode:
		return a.walkChainWrapper(n.wrapper, ancestor, child)
	case *tagFilterNode:
		return a.walkChainWrapper(n.bodyWrapper, ancestor, child)
	case *tagSpacelessNode:
		return a.walkChainWrapper(n.wrapper, ancestor, child)
	case *tagAutoescapeNode:
		return a.walkChainWrapper(n.wrapper, ancestor, child)
	case *tagMacroNode:
		return a.walkChainWrapper(n.wrapper, ancestor, child)
	case *tagFirstofNode:
		return a.assign(n.position, &n.escapeKind)
	case *tagCycleNode:
		if n.silent {
			return nil
		}
		return a.assign(n.position, &n.escapeKind)
	case *nodeVariable:
		return a.assign(n.locationToken, &n.escapeKind)
	case *nodeHTML:
		return a.scanner.scanChunk(n.token.Val, n.token)
	case *tagSetNode, *tagImportNode, *tagExtendsNode, *tagNowNode,
		*tagLoremNode, *tagWidthratioNode, *tagTemplateTagNode, *tagCommentNode:
		return nil
	case *tagSSINode:
		return ctxError(nil, "contextual escaping: the {% ssi %} tag is not supported")
	case *tagIncludeEmptyNode:
		return nil
	default:
		return ctxError(positionOfNode(node),
			"contextual escaping: unknown or custom tag is not supported by context analysis")
	}
}

func (a *contextualAnalyzer) walkChainBranches(wrappers []*NodeWrapper, ancestor, child *Template) error {
	entry := cloneScanState(&a.scanner.state)
	var merged *scanState
	for _, wrapper := range wrappers {
		a.scanner.state = cloneScanState(&entry)
		if err := a.walkChainWrapperNodes(wrapper, ancestor, child); err != nil {
			return err
		}
		out := a.scanner.state
		if merged == nil {
			cp := cloneScanState(&out)
			merged = &cp
		} else if !mergeCompatible(merged, &out) {
			return ctxError(wrapper.start,
				"contextual escaping: conditional branches end in different contexts")
		}
	}
	a.scanner.state = cloneScanState(merged)
	return nil
}

func (a *contextualAnalyzer) walkChainLoop(wrapper *NodeWrapper, ancestor, child *Template, empty bool) error {
	entry := cloneScanState(&a.scanner.state)
	if err := a.walkChainWrapperNodes(wrapper, ancestor, child); err != nil {
		return err
	}
	if contextOpen(&a.scanner.state) {
		msg := "contextual escaping: the loop body opens a context that is not closed inside the body"
		if empty {
			msg = "contextual escaping: the {% empty %} branch must return to the loop's entry context"
		}
		return ctxError(wrapper.start, msg)
	}
	a.scanner.state = cloneScanState(&entry)
	return nil
}

func (a *contextualAnalyzer) walkChainWrapper(wrapper *NodeWrapper, ancestor, child *Template) error {
	entry := cloneScanState(&a.scanner.state)
	if err := a.walkChainWrapperNodes(wrapper, ancestor, child); err != nil {
		return err
	}
	if contextOpen(&a.scanner.state) {
		return ctxError(wrapper.start,
			"contextual escaping: this block must return to its entry context")
	}
	a.scanner.state = cloneScanState(&entry)
	return nil
}

func (a *contextualAnalyzer) walkChainWrapperNodes(wrapper *NodeWrapper, ancestor, child *Template) error {
	for _, node := range wrapper.nodes {
		if err := a.walkChainNode(node, ancestor, child); err != nil {
			return err
		}
	}
	return nil
}

// mostDerivedBlock walks from child toward the root and returns the first
// wrapper defined for name (child-most definition wins at execution time).
func mostDerivedBlock(child *Template, name string) *NodeWrapper {
	for tpl := child; tpl != nil; tpl = tpl.parent {
		if wrapper, ok := tpl.blocks[name]; ok {
			return wrapper
		}
	}
	return nil
}

func (a *contextualAnalyzer) walkNodes(nodes []INode) error {
	for _, node := range nodes {
		if err := a.walkNode(node); err != nil {
			return err
		}
	}
	return nil
}

func (a *contextualAnalyzer) walkNode(node INode) error {
	switch n := node.(type) {
	case *nodeHTML:
		return a.scanner.scanChunk(n.token.Val, n.token)

	case *nodeVariable:
		return a.assign(n.locationToken, &n.escapeKind)

	case *tagIfNode:
		return a.walkBranches(n.wrappers)

	case *tagIfEqualNode:
		return a.walkBranches(optionalWrappers(n.thenWrapper, n.elseWrapper))

	case *tagIfNotEqualNode:
		return a.walkBranches(optionalWrappers(n.thenWrapper, n.elseWrapper))

	case *tagIfchangedNode:
		return a.walkBranches(optionalWrappers(n.thenWrapper, n.elseWrapper))

	case *tagForNode:
		return a.walkLoop(n.bodyWrapper, n.emptyWrapper)

	case *tagWithNode:
		return a.walkBalancedWrapper(n.wrapper)

	case *tagFilterNode:
		return a.walkBalancedWrapper(n.bodyWrapper)

	case *tagSpacelessNode:
		return a.walkBalancedWrapper(n.wrapper)

	case *tagAutoescapeNode:
		// Autoescape off does not disable contextual analysis: the output
		// position is still statically known.
		return a.walkBalancedWrapper(n.wrapper)

	case *tagBlockNode:
		return a.walkBlock(n.wrapper)

	case *tagMacroNode:
		return a.walkBalancedWrapper(n.wrapper)

	case *tagIncludeNode:
		return a.walkInclude(n)

	case *tagIncludeEmptyNode:
		// Missing include with if_exists renders nothing.
		return nil

	case *tagFirstofNode:
		return a.assign(n.position, &n.escapeKind)

	case *tagCycleNode:
		if n.silent {
			return nil
		}
		return a.assign(n.position, &n.escapeKind)

	case *tagSetNode, *tagImportNode, *tagExtendsNode,
		*tagNowNode, *tagLoremNode, *tagWidthratioNode,
		*tagTemplateTagNode, *tagCommentNode:
		// These produce no template-controlled string output or are static.
		return nil

	case *tagSSINode:
		return ctxError(nil, "contextual escaping: the {% ssi %} tag is not supported")

	default:
		return ctxError(positionOfNode(node),
			"contextual escaping: unknown or custom tag is not supported by context analysis")
	}
}

// assign records (or verifies) the context kind at an output position.
func (a *contextualAnalyzer) assign(pos *Token, kind *escapeKind) error {
	got, err := a.scanner.holeKind(pos)
	if err != nil {
		return err
	}
	if a.verify {
		if *kind != got {
			return ctxError(pos, "contextual escaping: this output is reused (e.g. an include or block override) "+
				"in a different context than where it was defined; every use must have the same context")
		}
		return nil
	}
	*kind = got
	return nil
}

// walkBranches analyzes mutually exclusive branches starting from the same
// state and requires all branches to finish in the same state.
func (a *contextualAnalyzer) walkBranches(wrappers []*NodeWrapper) error {
	entry := cloneScanState(&a.scanner.state)

	var merged *scanState
	for _, wrapper := range wrappers {
		a.scanner.state = cloneScanState(&entry)
		if err := a.walkNodes(wrapper.nodes); err != nil {
			return err
		}
		out := a.scanner.state
		if merged == nil {
			cp := cloneScanState(&out)
			merged = &cp
		} else if !mergeCompatible(merged, &out) {
			return ctxError(wrapper.start,
				"contextual escaping: conditional branches end in different contexts; "+
					"each branch must close quotes/tags it opens so both sides merge into the same context")
		}
	}

	a.scanner.state = cloneScanState(merged)
	return nil
}

// mergeCompatible defines when two post-branch states may be joined. Both
// branches must have closed any quoted/raw/script context; once back in HTML
// text, partial element structure (including attributes started before the
// if-tag) is allowed to continue, since branches render mutually exclusively.
func mergeCompatible(a, b *scanState) bool {
	if a.phase == phText && b.phase == phText {
		return true
	}
	return statesEqual(a, b)
}

// walkLoop analyzes a loop body (zero or more iterations) and its empty
// clause (zero or one) from the entry state and requires return to it.
func (a *contextualAnalyzer) walkLoop(body, empty *NodeWrapper) error {
	entry := cloneScanState(&a.scanner.state)

	a.scanner.state = cloneScanState(&entry)
	if err := a.walkNodes(body.nodes); err != nil {
		return err
	}
	if contextOpen(&a.scanner.state) {
		return ctxError(body.start,
			"contextual escaping: the loop body opens a context (quote/tag/script string) that is not closed "+
				"inside the body; loop bodies must return to their entry context")
	}

	if empty != nil {
		a.scanner.state = cloneScanState(&entry)
		if err := a.walkNodes(empty.nodes); err != nil {
			return err
		}
		if contextOpen(&a.scanner.state) {
			return ctxError(empty.start,
				"contextual escaping: the {% empty %} branch must return to the loop's entry context")
		}
	}

	return nil
}

// walkBalancedWrapper analyzes a wrapper that must preserve state (block
// definitions, macros, {% with %}, include-like wrappers) and restores the
// entry state afterwards.
func (a *contextualAnalyzer) walkBalancedWrapper(wrapper *NodeWrapper) error {
	entry := cloneScanState(&a.scanner.state)
	if err := a.walkNodes(wrapper.nodes); err != nil {
		return err
	}
	if contextOpen(&a.scanner.state) {
		return ctxError(wrapper.start,
			"contextual escaping: this block opens a context that it does not close; it must return to its entry context")
	}
	a.scanner.state = cloneScanState(&entry)
	return nil
}

// walkBlock analyzes a block definition/override at its output position: the
// wrapper content is required to close everything it opens, but the
// surrounding template continues in the state it had *before* the block tag
// (block content renders in place and returns to HTML text).
func (a *contextualAnalyzer) walkBlock(wrapper *NodeWrapper) error {
	saved := cloneScanState(&a.scanner.state)
	if err := a.walkNodes(wrapper.nodes); err != nil {
		return err
	}
	if contextOpen(&a.scanner.state) {
		return ctxError(wrapper.start,
			"contextual escaping: this block opens a context (quote/attribute/script string) that it does not close")
	}
	a.scanner.state = cloneScanState(&saved)
	return nil
}

func (a *contextualAnalyzer) walkInclude(n *tagIncludeNode) error {
	if n.lazy {
		return ctxError(n.position, "contextual escaping: only static {% include \"...\" %} is supported; "+
			"dynamic template names cannot be analyzed")
	}
	if n.tpl == nil {
		// Recursive include compiled lazily or if_exists miss: unsupported.
		return ctxError(n.position, "contextual escaping: recursive or unresolved {% include %} is not supported")
	}

	entry := cloneScanState(&a.scanner.state)
	if entry.phase != phText {
		return ctxError(n.position, "contextual escaping: {% include %} is only allowed in HTML text position "+
			"(the included template starts and ends its own HTML document context)")
	}

	// The included template was analyzed at its own compilation from HTML text
	// and was required to finish in HTML text. Re-walk it in verify mode to
	// catch reuses under conflicting contexts and to include it in this
	// template's traversal.
	inc := newAnalyzer(true)
	if err := inc.walkNodes(n.tpl.root.Nodes); err != nil {
		return err
	}
	if inc.scanner.state.phase != phText {
		return ctxError(n.position, "contextual escaping: included template does not return to HTML text")
	}
	return nil
}

func optionalWrappers(then, els *NodeWrapper) []*NodeWrapper {
	if then == nil {
		return nil
	}
	if els == nil {
		return []*NodeWrapper{then}
	}
	return []*NodeWrapper{then, els}
}

// lastNodeToken returns a position token at the end of a node list, used to
// report unclosed contexts at EOF.
func lastNodeToken(nodes []INode) *Token {
	for i := len(nodes) - 1; i >= 0; i-- {
		switch n := nodes[i].(type) {
		case *nodeHTML:
			return n.token
		case *nodeVariable:
			return n.locationToken
		}
	}
	return nil
}

func positionOfNode(node INode) *Token {
	type positioned interface {
		getPosition() *Token
	}
	if p, ok := node.(positioned); ok {
		return p.getPosition()
	}
	return nil
}
