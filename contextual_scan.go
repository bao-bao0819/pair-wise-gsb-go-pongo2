package pongo2

import "strings"

// htmlScanner is a conservative lexer over the literal HTML text between
// template tags. It is driven by the compile-time contextual analyzer:
// feedText() consumes one nodeHTML token, feedSlot() reports the context at a
// {{ }} insertion point.
//
// The scanner deliberately understands only a small, decidable subset of
// HTML/JavaScript. Anything it cannot classify reliably is rejected at the
// insertion point instead of guessed.
type htmlScanner struct {
	state scanState

	// tagName holds the name of the open/close tag currently being scanned.
	tagName strings.Builder
	// attrName holds the attribute name currently being scanned.
	attrName strings.Builder

	// quote is the opening quote of a quoted attribute value (' or '"').
	quote byte

	// jsBackslash is true when the last byte of a quoted JavaScript string
	// was a backslash right at a chunk boundary. A variable following such
	// a backslash would be consumed by JS escaping and must be rejected.
	jsBackslash bool

	// jsTainted marks script content after a slash appeared outside of a
	// quoted string. We do not parse JS grammar (regex vs division), so once
	// a slash can have started a regular expression or a comment the scanner
	// cannot prove where quoted strings start; all later variable slots in
	// that script are rejected until </script>.
	jsTainted bool

	// scriptClose counts the matched prefix of "</script" while in script.
	scriptClose int
	// styleClose counts the matched prefix of "</style" while in style.
	styleClose int
	// rawEndPending means the "</script"/"</style" name fully matched and the
	// scanner is waiting for its terminating byte ('>', whitespace or '/').
	rawEndPending bool

	// commentDashes counts consecutive '-' while inside an HTML comment.
	commentDashes int

	// depth counts open contexts that must be closed to return to the same
	// place: quoted attribute values and <script>/<style> elements. It lets
	// the analyzer distinguish "inside one more unclosed quote" from a true
	// convergence (state alone is insufficient because states nest).
	depth int

	// opened and closed count nesting transitions cumulatively. Branch merges
	// compare the full pair (so "close outer quote, open new quote" is not
	// mistaken for convergence); loop bodies compare only the balanced delta
	// because balanced open/close pairs may repeat every iteration.
	opened int
	closed int
}

type scanState int

const (
	stText         scanState = iota // element text content
	stTagOpen                       // '<' consumed, deciding comment/decl/open/close
	stTagName                       // <NAME being scanned
	stAttrs                         // whitespace/gap before attribute name in open tag
	stAttrName                      // attribute name being scanned
	stAttrBeforeEq                  // whitespace before '='
	stAttrAfterEq                   // gap after '='
	stAttrQuoted                    // quoted attribute value
	stAttrUnquoted                  // unquoted attribute value (slots rejected)
	stAttrBadValue                  // value of event handler/style attribute (slots rejected)
	stCloseTagName                  // </NAME being scanned
	stClosing                       // gap after close tag name until '>'
	stComment                       // inside <!-- ... -->
	stDecl                          // inside <! ... > (DOCTYPE/other declaration)

	stScriptCode    // <script>: JavaScript code
	stScriptDQ      // <script>: "..."
	stScriptSQ      // <script>: '...'
	stScriptTpl     // <script>: `...` (slots rejected)
	stScriptTaint   // <script>: slash made strings unprovable
	stScriptClosing // whitespace after matched </script, waiting for '>'

	stStyle        // <style>: raw content (slots rejected)
	stStyleClosing // whitespace after matched </style, waiting for '>'
)

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

func isNameByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	}
	return b == '-' || b == '_' || b == ':' || b == '.'
}

// isURLAttr reports whether attr is a URL-bearing attribute whose quoted
// values require protocol validation. The list is explicit on purpose: only
// attributes that a browser resolves as a navigable/loadable resource are
// treated as URL contexts.
func isURLAttr(attr string) bool {
	switch strings.ToLower(attr) {
	case "href", "src", "action", "formaction", "cite", "background",
		"longdesc", "profile", "usemap", "classid", "codebase", "manifest",
		"poster", "archive":
		return true
	}
	return false
}

// attrValueKind decides what kind of quoted value the attribute currently open
// accepts. Event handler (on*) and style attributes cannot be escaped with
// plain attribute escaping and are rejected.
func attrValueKind(name string) (escapeKind, bool) {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "on") {
		return 0, false
	}
	if lower == "style" {
		return 0, false
	}
	if isURLAttr(lower) {
		return escapeURLAttr, true
	}
	return escapeHTMLAttr, true
}

const (
	scriptEnd = "</script"
	styleEnd  = "</style"
)

// endTagTerminator reports whether b may legally follow </script or </style:
// '>' ends the tag, whitespace/slash lead into the closing gap.
func endTagTerminator(b byte) bool {
	return b == '>' || isSpaceByte(b) || b == '/'
}

// snapshot captures the complete scanner state so the analyzer can analyze
// control-flow branches from the same entry state.
type scannerSnapshot struct {
	state       scanState
	tagName     string
	attrName    string
	quote       byte
	jsBackslash bool
	jsTainted   bool
	scriptClose int
	styleClose  int
	depth       int
	opened      int
	closed      int
}

func (s *htmlScanner) snapshot() scannerSnapshot {
	return scannerSnapshot{
		state:       s.state,
		tagName:     s.tagName.String(),
		attrName:    s.attrName.String(),
		quote:       s.quote,
		jsBackslash: s.jsBackslash,
		jsTainted:   s.jsTainted,
		scriptClose: s.scriptClose,
		styleClose:  s.styleClose,
		depth:       s.depth,
		opened:      s.opened,
		closed:      s.closed,
	}
}

func (s *htmlScanner) restore(snap scannerSnapshot) {
	s.state = snap.state
	s.tagName.Reset()
	s.tagName.WriteString(snap.tagName)
	s.attrName.Reset()
	s.attrName.WriteString(snap.attrName)
	s.quote = snap.quote
	s.jsBackslash = snap.jsBackslash
	s.jsTainted = snap.jsTainted
	s.scriptClose = snap.scriptClose
	s.styleClose = snap.styleClose
	s.depth = snap.depth
	s.opened = snap.opened
	s.closed = snap.closed
}

// enterText moves back to element text content and clears all per-markup
// scratch fields, so snapshots taken at merge points compare only meaningful
// continuation state.
func (s *htmlScanner) enterText() {
	s.state = stText
	s.tagName.Reset()
	s.attrName.Reset()
	s.quote = 0
	s.jsBackslash = false
	s.jsTainted = false
	s.scriptClose = 0
	s.styleClose = 0
	s.commentDashes = 0
	// depth is preserved: closing a tag only unwinds the current markup; a
	// quoted attribute inside a script (and similar nesting) must keep its
	// outer level. Fresh scans start with depth 0.
}

// finishCheck validates that the template document ends in plain element
// text. A template that leaves an attribute value, tag, comment, script or
// style element open ends in an undecidable context and is rejected.
func (s *htmlScanner) finishCheck() error {
	if s.state == stText {
		return nil
	}
	var what string
	switch s.state {
	case stAttrQuoted:
		what = "a quoted attribute value is not closed at the end of the template"
	case stAttrUnquoted:
		what = "an unquoted attribute value is not closed at the end of the template"
	case stAttrBadValue:
		what = "an event handler or style attribute is not closed at the end of the template"
	case stStyle, stStyleClosing:
		what = "a <style> element is not closed at the end of the template"
	default:
		if s.inScript() {
			what = "a <script> element is not closed at the end of the template"
		} else {
			what = "the template ends inside an open tag or declaration"
		}
	}
	return contextualError(nil, what)
}
