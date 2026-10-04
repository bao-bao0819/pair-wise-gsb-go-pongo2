package pongo2

import "strings"

// feedText processes one literal HTML chunk (the rendered text of a nodeHTML
// node, already honoring whitespace trimming).
func (s *htmlScanner) feedText(text string) {
	for i := 0; i < len(text); i++ {
		s.feedByte(text[i])
	}
}

// feedSlot classifies a variable insertion point. The returned kind tells the
// analyzer which escaping to compile in; if ok is false the context is
// undecidable and unsupported explains the rejection reason.
func (s *htmlScanner) feedSlot() (kind escapeKind, ok bool, unsupported string) {
	switch s.state {
	case stText:
		return escapeHTMLText, true, ""
	case stAttrQuoted:
		return s.attrSlot()
	case stScriptDQ:
		return s.jsStringSlot()
	case stScriptSQ:
		return s.jsStringSlot()
	case stAttrUnquoted:
		return 0, false, "variables are only supported in quoted attribute values (missing quotes around the attribute value)"
	case stAttrBadValue:
		return 0, false, "variables are not supported in event handler (on*) or style attributes"
	case stCloseTagName, stClosing:
		return 0, false, "variables are not supported inside a closing tag"
	case stComment, stDecl:
		return 0, false, "variables are not supported inside HTML comments or declarations"
	case stScriptTpl:
		return 0, false, "variables are not supported inside JavaScript template literals"
	case stScriptTaint:
		return 0, false, "the surrounding <script> contains a slash outside quoted strings; JavaScript string boundaries cannot be proven"
	case stScriptClosing:
		return 0, false, "variables are not supported inside the closing <script> tag"
	case stStyle, stStyleClosing:
		return 0, false, "variables are not supported inside <style> elements (CSS context)"
	case stScriptCode:
		return 0, false, "variables are only supported inside single or double quoted JavaScript strings (found variable in script code)"
	default:
		return 0, false, "dynamic tag or attribute names and unquoted attribute markup are not supported"
	}
}

// attrSlot classifies a variable inside a quoted attribute value.
func (s *htmlScanner) attrSlot() (escapeKind, bool, string) {
	if s.jsBackslash {
		return 0, false, "variable immediately follows a backslash in a quoted attribute value"
	}
	kind, supported := attrValueKind(s.attrName.String())
	if !supported {
		return 0, false, "variables are not supported in event handler (on*) or style attributes"
	}
	return kind, true, ""
}

// jsStringSlot classifies a variable inside a quoted JavaScript string.
func (s *htmlScanner) jsStringSlot() (escapeKind, bool, string) {
	if s.jsTainted {
		return 0, false, "the surrounding <script> contains a slash outside quoted strings; JavaScript string boundaries cannot be proven"
	}
	if s.jsBackslash {
		return 0, false, "variable immediately follows a backslash inside a JavaScript string"
	}
	return escapeJSString, true, ""
}

func (s *htmlScanner) feedByte(b byte) {
	if s.inScript() {
		s.feedScriptByte(b)
		return
	}
	if s.state == stStyle || s.state == stStyleClosing {
		s.feedStyleByte(b)
		return
	}
	s.feedHTMLByte(b)
}

func (s *htmlScanner) inScript() bool {
	switch s.state {
	case stScriptCode, stScriptDQ, stScriptSQ, stScriptTpl,
		stScriptTaint, stScriptClosing:
		return true
	}
	return false
}

// feedHTMLByte handles every state outside <script>/<style> raw content.
func (s *htmlScanner) feedHTMLByte(b byte) {
	switch s.state {
	case stText:
		s.textByte(b)
	case stTagOpen:
		s.tagOpenByte(b)
	case stTagName:
		s.tagNameByte(b)
	case stAttrBeforeEq:
		s.beforeEqByte(b)
	case stAttrAfterEq:
		s.afterEqByte(b)
	case stAttrQuoted:
		s.attrQuotedByte(b)
	case stAttrUnquoted:
		s.terminatedValueByte(b)
	case stAttrBadValue:
		s.terminatedValueByte(b)
	case stCloseTagName:
		s.closeTagNameByte(b)
	case stClosing:
		if b == '>' {
			s.enterText()
		}
	case stComment:
		s.commentByte(b)
	case stDecl:
		s.declByte(b)
	default:
		// stAttrs / stAttrName live in their own dispatch group below.
		s.feedAttrMarkupByte(b)
	}
}

// feedAttrMarkupByte handles the in-tag states that scan attribute markup.
func (s *htmlScanner) feedAttrMarkupByte(b byte) {
	switch s.state {
	case stAttrs:
		s.attrsByte(b)
	case stAttrName:
		s.attrNameByte(b)
	}
}

func (s *htmlScanner) beforeEqByte(b byte) {
	if isSpaceByte(b) {
		return
	}
	if b == '=' {
		s.state = stAttrAfterEq
		return
	}
	s.enterAttrsLike(b)
}

func (s *htmlScanner) attrQuotedByte(b byte) {
	if b == s.quote {
		s.depth--
		s.closed++
		s.state = stAttrs
	}
}

func (s *htmlScanner) terminatedValueByte(b byte) {
	switch {
	case isSpaceByte(b):
		s.state = stAttrs
	case b == '>':
		s.enterText()
	}
}

func (s *htmlScanner) closeTagNameByte(b byte) {
	switch {
	case isNameByte(b):
		s.tagName.WriteByte(b)
	case isSpaceByte(b):
		s.state = stClosing
	case b == '>':
		s.enterText()
	}
}

// declByte distinguishes comments (<!-- -->) from declarations (<!DOCTYPE>).
// After the leading '!', seeing '--' upgrades the state to comment; otherwise
// it stays a declaration until the matching '>'. Slots are rejected in both.
func (s *htmlScanner) declByte(b byte) {
	if s.state != stDecl {
		return
	}
	switch b {
	case '-':
		if s.attrName.Len() < 3 {
			s.attrName.WriteByte(b)
		}
		if s.attrName.String() == "!--" {
			s.attrName.Reset()
			s.commentDashes = 0
			s.state = stComment
		}
	case '>':
		s.attrName.Reset()
		s.enterText()
	}
}

// tagOpenByte resolves the byte directly following '<' in text content.
func (s *htmlScanner) tagOpenByte(b byte) {
	switch {
	case isNameByte(b):
		s.tagName.Reset()
		s.tagName.WriteByte(b)
		s.state = stTagName
	case b == '/':
		s.tagName.Reset()
		s.state = stCloseTagName
	case b == '!':
		s.commentDashes = 0
		s.attrName.Reset()
		s.attrName.WriteByte('!')
		s.state = stDecl
	case b == '?':
		s.attrName.Reset()
		s.state = stDecl
	default:
		// Stray '<' stays literal text.
		s.enterText()
	}
}

func (s *htmlScanner) textByte(b byte) {
	if b != '<' {
		return
	}
	s.scriptClose, s.styleClose = 0, 0
	s.tagName.Reset()
	s.state = stTagOpen
}

func (s *htmlScanner) tagNameByte(b byte) {
	switch {
	case isNameByte(b):
		s.tagName.WriteByte(b)
	case b == '>':
		s.finishOpenTag()
	case isSpaceByte(b):
		s.attrName.Reset()
		s.state = stAttrs
	case b == '/':
		// Self-closing slash before any attribute; stay in the attribute gap
		// until '>' (attrsByte treats a '/' there as harmless).
		s.attrName.Reset()
		s.state = stAttrs
	default:
		// Unrecognized tag syntax: treat remaining markup as attributes.
		s.attrName.Reset()
		s.state = stAttrs
	}
}

// finishOpenTag moves from '>' after a tag name to text/script/style content.
func (s *htmlScanner) finishOpenTag() {
	switch strings.ToLower(s.tagName.String()) {
	case "script":
		s.jsTainted, s.jsBackslash = false, false
		s.scriptClose = 0
		s.depth++
		s.opened++
		s.state = stScriptCode
	case "style":
		s.styleClose = 0
		s.depth++
		s.opened++
		s.state = stStyle
	default:
		s.enterText()
	}
}

func (s *htmlScanner) attrsByte(b byte) {
	switch {
	case isSpaceByte(b):
	case b == '>':
		s.finishOpenTag()
	case b == '/':
		// self-closing slash; stay in attribute gap until '>'
	case b == '<':
		// Malformed; keep scanning conservatively.
	default:
		s.startAttrName(b)
	}
}

// startAttrName begins scanning a new attribute name from its first byte.
func (s *htmlScanner) startAttrName(b byte) {
	s.attrName.Reset()
	s.attrName.WriteByte(b)
	s.state = stAttrName
}

func (s *htmlScanner) attrNameByte(b byte) {
	switch {
	case isNameByte(b):
		s.attrName.WriteByte(b)
	case b == '=':
		s.state = stAttrAfterEq
	case isSpaceByte(b):
		s.state = stAttrBeforeEq
	case b == '>':
		// Boolean attribute ending a tag.
		s.finishOpenTag()
	default:
		// '=' preceded by other characters is consumed; anything else is
		// treated as continuation to avoid splitting dynamic names.
	}
}

// enterAttrsLike restarts attribute scanning at b after a finished attribute.
func (s *htmlScanner) enterAttrsLike(b byte) {
	if isNameByte(b) {
		s.startAttrName(b)
		return
	}
	s.state = stAttrs
}

func (s *htmlScanner) afterEqByte(b byte) {
	switch {
	case isSpaceByte(b):
	case b == '\'' || b == '"':
		s.quote = b
		s.depth++
		s.opened++
		if _, supported := attrValueKind(s.attrName.String()); supported {
			s.state = stAttrQuoted
		} else {
			s.state = stAttrBadValue
		}
	default:
		// Unquoted value; variable slots here are rejected.
		s.state = stAttrUnquoted
	}
}

func (s *htmlScanner) commentByte(b byte) {
	// Minimal "-->" detection. State persists across HTML tokens, so the
	// marker does not need to survive a template slot (slots are rejected in
	// comments anyway).
	switch b {
	case '-':
		if s.commentDashes < 2 {
			s.commentDashes++
		}
		return
	case '>':
		if s.commentDashes >= 2 {
			s.enterText()
		}
	default:
		s.commentDashes = 0
	}
}
