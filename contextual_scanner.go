package pongo2

import (
	"strings"
)

// escapeKind identifies the context a variable output is rendered into when
// contextual escaping is enabled. The compile-time analyzer assigns one of
// these kinds to every output node it can classify.
type escapeKind int

const (
	escNone escapeKind = iota

	// escHTMLText: element text content, e.g. <p>{{ x }}</p>.
	escHTMLText

	// escHTMLAttr: a quoted attribute value which is not a URL,
	// e.g. <input value="{{ x }}">.
	escHTMLAttr

	// escURLAttr: a quoted URL attribute value, e.g. <a href="{{ x }}">.
	// In addition to HTML attribute escaping the value is protocol-checked.
	escURLAttr

	// escJSString: a single- or double-quoted JavaScript string literal
	// inside a <script> element, e.g. <script>var a = "{{ x }}";</script>.
	escJSString
)

// scanPhase is the lexical HTML/JS context the analyzer is currently in.
// A phase is "resumable": the analyzer alternates between raw HTML chunks and
// variable holes and always resumes in the phase it left off.
type scanPhase int

const (
	// phText: HTML text outside any element.
	phText scanPhase = iota

	// phTagName: immediately inside "<" / "</", reading the tag name.
	phTagName

	// phLt / phLtSlash: saw "<" or "</" with the next byte still pending
	// (possibly a variable hole or the next HTML chunk).
	phLt
	phLtSlash

	// phTag: inside an element, between attributes.
	phTag

	// phAttrName: reading an attribute name.
	phAttrName

	// phUnquoted: inside an unquoted attribute value. Variables are not
	// allowed here, but static text still has to be scanned to find its end.
	phUnquoted

	// phAttrValueStart: after '=' before the first value byte; a variable
	// here means an unquoted/dynamic attribute value and is rejected.
	phAttrValueStart

	// phCSSAttr: inside a quoted style="..." value (CSS). Variables are
	// rejected.
	phCSSAttr

	// phRawCloseEnd: the literal "</script"/"</style" name has been matched
	// inside raw text and we are checking for whitespace, '/' or '>'.
	phRawCloseEnd

	// phAttrDouble / phAttrSingle: inside a quoted (non-URL) attribute.
	phAttrDouble
	phAttrSingle

	// phURLDouble / phURLSingle: inside a quoted URL attribute.
	phURLDouble
	phURLSingle

	// phRawClose: inside <script>/<style> raw text scanning byte-by-byte for
	// the literal closing tag (per the HTML tokenizer raw-text rules).
	phRawClose

	// phJSDouble / phJSSingle: inside a quoted JS string.
	phJSDouble
	phJSSingle

	// phJSCode: inside <script> JavaScript code (outside a string).
	phJSCode

	// phJSLineComment / phJSBlockComment: inside JS comments. Variables are
	// not allowed.
	phJSLineComment
	phJSBlockComment

	// phJSTpl: inside a JS template literal (`...`). Variables are not
	// allowed, but backticks/holes still have to be tracked.
	phJSTpl

	// phJSRegexp: inside a JS regular expression literal. Variables are not
	// allowed; this only keeps slash state unambiguous.
	phJSRegexp
)

// scanState is a fully serializable lexical state. Fields other than phase
// carry the small amount of context required to resume scanning across an
// arbitrary number of HTML chunks and variable holes.
type scanState struct {
	phase scanPhase

	// closeTag is true for a closing element ("</...").
	closeTag bool

	// tagName is the lowercased name of the element currently being parsed,
	// used to recognize script/style closing tags.
	tagName string

	// attrName is the lowercased attribute name currently being parsed.
	attrName string

	// attrVal accumulates the current attribute value (for capturing a static
	// script type, including an unquoted one).
	attrVal string

	// rawName is "script" or "style" while in phRawClose.
	rawName string

	// rawMatch is the number of matched bytes of the "</name" prefix while
	// scanning raw element text.
	rawMatch int

	// scriptType holds the static value of the type attribute for the script
	// element currently being parsed.
	scriptType string

	// haveType indicates whether a type attribute was seen on the script.
	haveType bool

	// pendingType marks the attribute currently being parsed as the script's
	// type attribute so its value reaches scriptType.
	pendingType bool

	// lastSignificant tracks the previous significant JS token used to decide
	// whether "/" starts a regexp or is a division operator.
	lastSignificant jsTokenClass

	// afterOpenBrace is true while no significant token followed a "{" yet;
	// in that position "/" always opens a regexp (block start).
	afterOpenBrace bool

	// jsRawMatch counts matched bytes of the literal "</script" while scanning
	// JavaScript, tracked incrementally so a split across chunks is detected.
	jsRawMatch int

	// jsEscaped is true when the previous byte was a backslash at a chunk
	// boundary, so the next byte (possibly in another chunk or a hole) is
	// treated as escaped.
	jsEscaped bool
}

type jsTokenClass int

const (
	jsTokNone  jsTokenClass = iota
	jsTokValue              // identifier/number/string/)]}
	jsTokOp                 // operator other than /
)

func cloneScanState(s *scanState) scanState {
	c := *s
	return c
}

func statesEqual(a, b *scanState) bool {
	if a.phase == phText && b.phase == phText {
		return true
	}
	if a.phase != b.phase || a.closeTag != b.closeTag ||
		a.rawName != b.rawName || a.rawMatch != b.rawMatch ||
		a.scriptType != b.scriptType || a.haveType != b.haveType ||
		a.jsRawMatch != b.jsRawMatch || a.jsEscaped != b.jsEscaped ||
		a.lastSignificant != b.lastSignificant || a.afterOpenBrace != b.afterOpenBrace {
		return false
	}
	return a.tagName == b.tagName &&
		a.attrName == b.attrName
}

func isHTMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

func isNameByte(b byte) bool {
	// ASCII-only is sufficient: attribute/tag names containing non-ASCII or
	// exotic punctuation are unusual and static anyway. Variables placed in
	// those positions remain classified by the surrounding state.
	return b == '-' || b == '_' || b == ':' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func isAttrNameByte(b byte) bool {
	return isNameByte(b)
}

// urlAttributes lists attributes whose value is a URL and therefore needs a
// protocol check in addition to attribute escaping.
var urlAttributes = map[string]struct{}{
	"action":     {},
	"background": {},
	"cite":       {},
	"data":       {},
	"formaction": {},
	"href":       {},
	"poster":     {},
	"src":        {},
	"xlink:href": {},
}

// javaScriptMIMETypes lists static <script type="..."> values treated as
// executable JavaScript. Any other static type makes the element non-JS
// (variables inside are rejected).
var javaScriptMIMETypes = map[string]struct{}{
	"":                         {},
	"text/javascript":          {},
	"application/javascript":   {},
	"application/ecmascript":   {},
	"text/ecmascript":          {},
	"application/x-ecmascript": {},
	"text/jscript":             {},
	"module":                   {},
	"text/javascript1.0":       {},
	"text/javascript1.1":       {},
	"text/javascript1.2":       {},
	"text/javascript1.3":       {},
	"text/javascript1.4":       {},
	"text/javascript1.5":       {},
	"text/x-javascript":        {},
	"text/x-ecmascript":        {},
}

// ctxScanner drives a scanState over a sequence of static HTML chunks with
// variable holes in between. It never guesses: every hole must land in one of
// the four supported contexts, otherwise an error describing the unsupported
// position is returned.
type ctxScanner struct {
	state scanState
}

func newCtxScanner() *ctxScanner {
	return &ctxScanner{state: scanState{phase: phText, lastSignificant: jsTokNone}}
}

// scanChunk processes a piece of static template text.
func (s *ctxScanner) scanChunk(chunk string, posToken *Token) error {
	for i := 0; i < len(chunk); {
		advanced, err := s.step(chunk, i, posToken)
		if err != nil {
			return err
		}
		i += advanced
	}
	return nil
}

// holeKind classifies a variable output at the current position.
func (s *ctxScanner) holeKind(posToken *Token) (escapeKind, error) {
	switch s.state.phase {
	case phText:
		return escHTMLText, nil
	case phAttrDouble, phAttrSingle:
		return escHTMLAttr, nil
	case phURLDouble, phURLSingle:
		return escURLAttr, nil
	case phJSDouble, phJSSingle:
		return escJSString, nil
	case phCSSAttr:
		return escNone, s.unsupported(posToken)
	default:
		return escNone, s.unsupported(posToken)
	}
}

// finish must be called after the last chunk. It rejects templates that end
// while a context is still open (unclosed attribute, script string, etc.).
func (s *ctxScanner) finish(posToken *Token) error {
	if s.state.phase == phText {
		return nil
	}
	if s.state.phase == phRawClose || s.state.phase == phRawCloseEnd {
		return ctxError(posToken, "contextual escaping: <"+s.state.rawName+
			"> element is not closed before the end of the template")
	}
	return s.unsupported(posToken)
}

func (s *ctxScanner) unsupported(posToken *Token) error {
	var where string
	switch s.state.phase {
	case phTagName:
		where = "a dynamic element/tag name"
	case phTag, phAttrName, phAttrValueStart:
		where = "a dynamic attribute name or an unquoted attribute boundary"
	case phUnquoted:
		where = "an unquoted HTML attribute value (quote the attribute)"
	case phRawClose, phRawCloseEnd:
		where = "raw <" + s.state.rawName + "> content (only quoted JS strings inside <script> are supported)"
	case phJSCode:
		where = "JavaScript code outside a quoted string"
	case phJSLineComment, phJSBlockComment:
		where = "a JavaScript comment"
	case phCSSAttr:
		where = "a CSS style attribute value"
	case phJSTpl:
		where = "a JavaScript template literal"
	case phJSRegexp:
		where = "a JavaScript regular expression literal"
	default:
		where = "an unsupported or unclosed HTML/JS context"
	}
	return ctxError(posToken, "contextual escaping: cannot determine the output context: "+
		where+" is not supported; use HTML text, a quoted attribute, a quoted URL attribute or a quoted JS string")
}

func ctxError(posToken *Token, msg string) error {
	err := &Error{
		Sender:    "contextual escaping",
		OrigError: &simpleError{msg: msg},
	}
	if posToken != nil {
		err.Filename = posToken.Filename
		err.Line = posToken.Line
		err.Column = posToken.Col
		err.Token = posToken
	}
	return err
}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }

// step processes the byte at index i of chunk and returns how many bytes it
// consumed.
func (s *ctxScanner) step(chunk string, i int, posToken *Token) (int, error) {
	st := &s.state
	b := chunk[i]

	switch st.phase {
	case phText:
		return s.stepText(chunk, i)
	case phTagName:
		return s.stepTagName(b)
	case phLt:
		return s.stepLt(b)
	case phLtSlash:
		return s.stepLtSlash(b)
	case phTag:
		return s.stepTag(b)
	case phAttrName:
		return s.stepAttrName(b)
	case phAttrValueStart:
		return s.stepAttrValueStart(b)
	case phUnquoted:
		return s.stepUnquoted(b)
	case phAttrDouble, phAttrSingle, phURLDouble, phURLSingle:
		return s.stepQuotedAttr(b)
	case phCSSAttr:
		return s.stepCSSAttr(b)
	case phRawClose:
		return s.stepRawClose(chunk, i)
	case phRawCloseEnd:
		return s.stepRawCloseEnd(b)
	case phJSCode:
		return s.stepJSCode(chunk, i)
	case phJSDouble, phJSSingle:
		return s.stepJSLiteral(chunk, i)
	case phJSTpl:
		return s.stepJSLiteral(chunk, i)
	case phJSRegexp:
		return s.stepJSLiteral(chunk, i)
	case phJSLineComment:
		return s.stepJSLineComment(b)
	case phJSBlockComment:
		return s.stepJSBlockComment(chunk, i)
	default:
		return 0, s.unsupported(posToken)
	}
}

func (s *ctxScanner) stepText(chunk string, i int) (int, error) {
	// Comments, CDATA, PIs and DOCTYPE are static-only constructs: a variable
	// inside them would land in phText after the opener, but to avoid guessing
	// we recognize their complete prefixes and reject holes inside them via
	// dedicated tracking below. For simplicity we only model constructs that
	// can change the lexical mode (elements); comments etc. are treated as
	// text, which is safe because they never contain variables in supported
	// templates (a variable inside a comment stays phText and gets HTML text
	// escaping, which is harmless and conservative).
	b := chunk[i]
	if b == '<' && i+1 < len(chunk) {
		n := chunk[i+1]
		if isNameByte(n) {
			s.state.phase = phTagName
			s.state.closeTag = false
			s.state.tagName = ""
			s.state.scriptType = ""
			s.state.haveType = false
			return 1, nil
		}
	}
	if b == '<' {
		s.state.phase = phLt
		return 1, nil
	}
	return 1, nil
}

// stepLt handles the byte after a pending '<'.
func (s *ctxScanner) stepLt(b byte) (int, error) {
	if isNameByte(b) {
		s.state.phase = phTagName
		s.state.closeTag = false
		s.state.tagName = ""
		s.state.scriptType = ""
		s.state.haveType = false
		s.state.tagName += string(lowerASCII(b))
		return 1, nil
	}
	if b == '/' {
		s.state.phase = phLtSlash
		return 1, nil
	}
	// Not an element opener; resume text and reprocess this byte.
	s.state.phase = phText
	return 0, nil
}

// stepLtSlash handles the byte after a pending "</".
func (s *ctxScanner) stepLtSlash(b byte) (int, error) {
	if isNameByte(b) {
		s.state.phase = phTagName
		s.state.closeTag = true
		s.state.tagName = ""
		s.state.tagName += string(lowerASCII(b))
		return 1, nil
	}
	// "</" followed by anything else is literal text.
	s.state.phase = phText
	return 0, nil
}

func (s *ctxScanner) stepTagName(b byte) (int, error) {
	switch {
	case isNameByte(b):
		s.state.tagName += string(lowerASCII(b))
	case isHTMLSpace(b):
		if len(s.state.tagName) == 0 {
			return 0, s.tagNameError(b)
		}
		s.state.phase = phTag
	case b == '>':
		return 1, s.finishOpeningTag()
	case b == '/':
		// Self-closing or stray slash: wait for '>'.
		s.state.phase = phTag
	default:
		return 0, s.tagNameError(b)
	}
	return 1, nil
}

func (s *ctxScanner) tagNameError(b byte) error {
	return ctxError(nil, "contextual escaping: invalid or dynamic element name near '"+string(b)+"'")
}

func (s *ctxScanner) finishOpeningTag() error {
	name := s.state.tagName
	s.state.phase = phText

	if s.state.closeTag {
		s.state.closeTag = false
		return nil
	}

	switch name {
	case "script":
		if !s.state.haveType {
			s.state.scriptType = ""
		}
		if _, ok := javaScriptMIMETypes[s.state.scriptType]; !ok {
			// Non-JS script (e.g. type="application/json"): raw text, no
			// variables allowed until the closing tag.
			s.state.phase = phRawClose
			s.state.rawName = "script"
			s.state.rawMatch = 0
			return nil
		}
		s.state.phase = phJSCode
		s.state.lastSignificant = jsTokNone
		s.state.afterOpenBrace = false
	case "style":
		s.state.phase = phRawClose
		s.state.rawName = "style"
		s.state.rawMatch = 0
	}
	return nil
}

func (s *ctxScanner) stepTag(b byte) (int, error) {
	switch {
	case isHTMLSpace(b):
	case b == '>':
		return 1, s.finishOpeningTag()
	case isAttrNameByte(b):
		s.state.phase = phAttrName
		s.state.attrName = ""
		s.state.pendingType = false
		s.state.attrName += string(lowerASCII(b))
		s.state.attrVal = ""
	case b == '/':
		// self-closing marker or stray slash; stay in tag
	default:
		return 0, ctxError(nil, "contextual escaping: unexpected character '"+
			string(b)+"' inside an HTML element (dynamic attribute names are not supported)")
	}
	return 1, nil
}

func (s *ctxScanner) stepAttrName(b byte) (int, error) {
	switch {
	case isAttrNameByte(b):
		s.state.attrName += string(lowerASCII(b))
	case isHTMLSpace(b):
		// Attribute name finished; wait for '=' or another attribute.
		s.markPendingType()
		s.captureAttrValue("")
		s.state.phase = phTag
	case b == '=':
		s.markPendingType()
		s.state.phase = phAttrValueStart
	case b == '>':
		// Boolean attribute without value.
		s.markPendingType()
		s.captureAttrValue("")
		return 1, s.finishOpeningTag()
	case b == '/':
		s.markPendingType()
		s.captureAttrValue("")
		s.state.phase = phTag
	default:
		return 0, ctxError(nil, "contextual escaping: dynamic attribute names are not supported (near '"+string(b)+"')")
	}
	return 1, nil
}

// captureAttrValue records a finished (possibly empty) attribute value so the
// element opener can decide its mode (e.g. a static script type).
func (s *ctxScanner) captureAttrValue(val string) {
	if s.state.pendingType {
		s.state.scriptType = strings.TrimSpace(val)
		s.state.haveType = true
		s.state.pendingType = false
	}
	s.state.attrVal = ""
}

func (s *ctxScanner) markPendingType() {
	s.state.pendingType = s.state.tagName == "script" && s.state.attrName == "type"
}

// unsupportedCSSAttr is true for attributes whose value is CSS and therefore
// outside the supported escaping contexts.
func unsupportedCSSAttr(tagName, attrName string) bool {
	return attrName == "style"
}

func (s *ctxScanner) stepAttrValueStart(b byte) (int, error) {
	name := s.state.attrName
	switch {
	case isHTMLSpace(b):
		// Leading whitespace between '=' and the value: stay.
		return 1, nil
	case b == '"' || b == '\'':
		s.state.attrVal = ""
		if unsupportedCSSAttr(s.state.tagName, name) {
			s.state.phase = phCSSAttr
			s.state.scriptType = string(b) // reuse as quote holder for CSS attr
			return 1, nil
		}
		if _, ok := urlAttributes[name]; ok {
			if b == '"' {
				s.state.phase = phURLDouble
			} else {
				s.state.phase = phURLSingle
			}
		} else {
			if b == '"' {
				s.state.phase = phAttrDouble
			} else {
				s.state.phase = phAttrSingle
			}
		}
	case b == '>':
		s.captureAttrValue("")
		return 1, s.finishOpeningTag()
	default:
		// Unquoted value. Variables are rejected in that position; static
		// text is accumulated so e.g. a static script type still works.
		s.state.attrVal = ""
		s.state.attrVal += string(b)
		s.state.phase = phUnquoted
	}
	return 1, nil
}

func (s *ctxScanner) stepCSSAttr(b byte) (int, error) {
	// Only the opening quote closes the CSS attribute value.
	if s.state.scriptType != "" && b == s.state.scriptType[0] {
		s.state.phase = phTag
	}
	return 1, nil
}

func (s *ctxScanner) stepUnquoted(b byte) (int, error) {
	switch {
	case isHTMLSpace(b):
		s.captureAttrValue(s.state.attrVal)
		s.state.phase = phTag
	case b == '>':
		s.captureAttrValue(s.state.attrVal)
		return 1, s.finishOpeningTag()
	default:
		s.state.attrVal += string(b)
	}
	return 1, nil
}

func (s *ctxScanner) stepQuotedAttr(b byte) (int, error) {
	switch s.state.phase {
	case phAttrDouble, phURLDouble:
		if b == '"' {
			s.captureQuotedAttrValue()
			s.state.phase = phTag
			return 1, nil
		}
	case phAttrSingle, phURLSingle:
		if b == '\'' {
			s.captureQuotedAttrValue()
			s.state.phase = phTag
			return 1, nil
		}
	}
	s.state.attrVal += string(b)
	return 1, nil
}

func (s *ctxScanner) captureQuotedAttrValue() {
	val := s.state.attrVal
	if len(val) > 0 {
		val = strings.TrimSpace(val)
	}
	s.captureAttrValue(val)
}

// stepRawClose scans raw <script>/<style> content one byte at a time looking
// for the literal closing tag. Content in between is fully opaque (no variable
// holes allowed).
func (s *ctxScanner) stepRawClose(chunk string, i int) (int, error) {
	prefix := "</" + s.state.rawName
	if s.state.rawMatch < len(prefix) && chunk[i] == prefix[s.state.rawMatch] {
		s.state.rawMatch++
		if s.state.rawMatch == len(prefix) {
			// Closing tag name matched; a valid close needs '>', '/', or
			// whitespace next. Remain in a small transitional mode.
			s.state.phase = phRawCloseEnd
		}
		return 1, nil
	}
	if s.state.rawMatch > 0 {
		// Partial prefix failed; restart the match from this byte.
		s.state.rawMatch = 0
		if chunk[i] == '<' {
			s.state.rawMatch = 1
		}
		return 1, nil
	}
	if chunk[i] == '<' {
		s.state.rawMatch = 1
	}
	return 1, nil
}

// stepRawCloseEnd handles bytes after the matched "</name": whitespace,
// attributes, '/' or '>'. Only on a valid closing tag do we leave raw mode.
func (s *ctxScanner) stepRawCloseEnd(b byte) (int, error) {
	switch {
	case isHTMLSpace(b) || b == '/' || b == '-':
		// still part of the closing tag
	case b == '>':
		s.state.phase = phText
		s.state.closeTag = false
	default:
		// Not a real closing tag; resume searching.
		s.state.phase = phRawClose
		s.state.rawMatch = 0
		if b == '<' {
			s.state.rawMatch = 1
		}
	}
	return 1, nil
}

func isJSIdentPart(b byte) bool {
	return b == '_' || b == '$' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func (s *ctxScanner) stepJSCode(chunk string, i int) (int, error) {
	b := chunk[i]

	// Raw-text closing tag detection also applies outside strings.
	switch s.matchJSRawClose(chunk, i) {
	case 2:
		s.state.phase = phRawCloseEnd
		s.state.rawName = "script"
		return 1, nil
	case 1:
		// Byte belongs to a partial "</script" match; do not interpret it
		// as a JS operator.
		return 1, nil
	}

	switch b {
	case '"':
		s.state.phase = phJSDouble
		s.state.jsRawMatch = 0
		s.state.jsEscaped = false
		return 1, nil
	case '\'':
		s.state.phase = phJSSingle
		s.state.jsRawMatch = 0
		s.state.jsEscaped = false
		return 1, nil
	case '`':
		s.state.phase = phJSTpl
		s.state.jsRawMatch = 0
		s.state.jsEscaped = false
		return 1, nil
	case '/':
		if i+1 < len(chunk) {
			switch chunk[i+1] {
			case '/':
				s.state.phase = phJSLineComment
				s.state.jsRawMatch = 0
				return 2, nil
			case '*':
				s.state.phase = phJSBlockComment
				s.state.jsRawMatch = 0
				return 2, nil
			}
			if s.slashStartsRegexp() {
				s.state.phase = phJSRegexp
				s.state.jsRawMatch = 0
				s.state.jsEscaped = false
				return 1, nil
			}
		}
		s.state.lastSignificant = jsTokOp
		s.state.afterOpenBrace = false
	case '=':
		s.state.lastSignificant = jsTokOp
		s.state.afterOpenBrace = false
	case '{':
		s.state.lastSignificant = jsTokNone
		s.state.afterOpenBrace = true
	case '}':
		s.state.lastSignificant = jsTokValue
		s.state.afterOpenBrace = false
	case ')', ']':
		s.state.lastSignificant = jsTokValue
		s.state.afterOpenBrace = false
	case '(':
		s.state.lastSignificant = jsTokNone
		s.state.afterOpenBrace = false
	default:
		if isHTMLSpace(b) {
			break
		}
		if isJSIdentPart(b) {
			s.state.lastSignificant = jsTokValue
		} else {
			// Other operators/punctuation.
			s.state.lastSignificant = jsTokOp
		}
		s.state.afterOpenBrace = false
	}
	return 1, nil
}

// slashStartsRegexp uses the conservative "last significant token" heuristic.
// If the context is ambiguous we could guess wrong, so a variable is only
// ever accepted *inside quotes* anyway; this function only keeps mode
// tracking correct.
func (s *ctxScanner) slashStartsRegexp() bool {
	if s.state.afterOpenBrace {
		return true
	}
	return s.state.lastSignificant != jsTokValue
}

// contextOpen reports whether a state is still inside a construct that must
// have been closed at a merge point (loop body, branch join, balanced
// wrapper). Open elements in HTML text do not count: starting an element in
// one iteration and finishing it in another is safe as long as no quote or
// script/raw-text mode is open.
func contextOpen(s *scanState) bool {
	return s.phase != phText
}

// jsRawClosePrefix is the raw-text element terminator, matched byte by byte
// even inside JS strings (the HTML tokenizer does not understand JS quoting).
var jsRawClosePrefix = "</script"

// stepJSLiteral handles a byte inside a quoted JS string, a template literal
// or a regexp literal. The literal "</script" is matched incrementally here so
// detection works regardless of where the HTML chunk boundary falls.
func (s *ctxScanner) stepJSLiteral(chunk string, i int) (int, error) {
	switch s.matchJSRawClose(chunk, i) {
	case 2:
		s.state.phase = phRawCloseEnd
		s.state.rawName = "script"
		return 1, nil
	case 1:
		return 1, nil
	}

	b := chunk[i]

	// Backslash escapes the next byte in every literal kind, including when
	// the two bytes land in different HTML chunks.
	if s.state.jsEscaped {
		s.state.jsEscaped = false
		return 1, nil
	}
	if b == '\\' {
		if i+1 < len(chunk) {
			return 2, nil
		}
		s.state.jsEscaped = true
		return 1, nil
	}

	switch s.state.phase {
	case phJSDouble:
		if b == '"' {
			s.finishJSLiteral()
		}
	case phJSSingle:
		if b == '\'' {
			s.finishJSLiteral()
		}
	case phJSRegexp:
		if b == '/' {
			s.finishJSLiteral()
		}
	case phJSTpl:
		switch {
		case b == '`':
			s.finishJSLiteral()
		case b == '$' && i+1 < len(chunk) && chunk[i+1] == '{':
			return 2, ctxError(nil, "contextual escaping: ${...} interpolation in JavaScript template literals is not supported")
		}
	}
	return 1, nil
}

// matchJSRawClose performs incremental matching of "</script" across chunks.
// It returns 2 when the closing prefix has been fully matched, 1 when the
// current byte continues a (possibly just started) partial match, and 0 when
// the byte is unrelated.
func (s *ctxScanner) matchJSRawClose(chunk string, i int) int {
	b := lowerASCII(chunk[i])
	if b == jsRawClosePrefix[s.state.jsRawMatch] {
		s.state.jsRawMatch++
		if s.state.jsRawMatch == len(jsRawClosePrefix) {
			s.state.jsRawMatch = 0
			s.state.rawMatch = len(jsRawClosePrefix)
			return 2
		}
		return 1
	} else if s.state.jsRawMatch > 0 {
		// Restart the match from this byte.
		s.state.jsRawMatch = 0
		if b == jsRawClosePrefix[0] {
			s.state.jsRawMatch = 1
			return 1
		}
	}
	return 0
}

func (s *ctxScanner) finishJSLiteral() {
	s.state.phase = phJSCode
	s.state.lastSignificant = jsTokValue
	s.state.afterOpenBrace = false
	s.state.jsRawMatch = 0
}

func (s *ctxScanner) stepJSLineComment(b byte) (int, error) {
	if b == '\n' || b == '\r' {
		s.state.phase = phJSCode
	}
	return 1, nil
}

func (s *ctxScanner) stepJSBlockComment(chunk string, i int) (int, error) {
	if chunk[i] == '*' && i+1 < len(chunk) && chunk[i+1] == '/' {
		s.state.phase = phJSCode
		return 2, nil
	}
	return 1, nil
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
