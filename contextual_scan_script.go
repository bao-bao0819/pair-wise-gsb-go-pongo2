package pongo2

// feedStyleByte tracks raw CSS content up to </style>. Variables are never
// accepted inside CSS.
func (s *htmlScanner) feedStyleByte(b byte) {
	s.feedRawEnd(styleEnd, &s.styleClose, b, stStyle, stStyleClosing)
}

// feedScriptByte handles every byte inside a <script> element. The element
// ends at the first </script even when it appears inside a JS string or a
// comment (matches HTML tokenization), so the end matcher runs in parallel to
// the small JavaScript state machine.
func (s *htmlScanner) feedScriptByte(b byte) {
	if s.feedRawEnd(scriptEnd, &s.scriptClose, b, stScriptCode, stScriptClosing) {
		return
	}

	switch s.state {
	case stScriptCode:
		s.scriptCodeByte(b)
	case stScriptDQ:
		s.scriptStringByte(b, stScriptDQ)
	case stScriptSQ:
		s.scriptStringByte(b, stScriptSQ)
	case stScriptTpl:
		if b == '`' {
			s.state = stScriptCode
		}
	case stScriptTaint:
		// Once string boundaries are unprovable, only </script> can end it.
	}
}

// feedRawEnd streams one byte through the "</script>"/"</style>" matcher.
// matched counts matched marker-name bytes; rawEndPending means the name is
// complete and the scanner waits for a legal terminator ('>', whitespace or
// '/'). It returns true when the element fully closes on this byte.
//
// On a false end tag (name not followed by a terminator) scanning resumes in
// contentState; gapState is used while the terminator is still possible.
func (s *htmlScanner) feedRawEnd(marker string, matched *int, b byte, contentState, gapState scanState) bool {
	if s.rawEndPending {
		switch {
		case b == '>':
			*matched = 0
			s.rawEndPending = false
			s.depth--
			s.closed++
			s.enterText()
			return true
		case isSpaceByte(b) || b == '/':
			s.state = gapState
			return false
		default:
			s.rawEndPending = false
			s.state = contentState
			// Fall through to process b as ordinary content.
		}
	}

	m := *matched
	if m < len(marker) && marker[m] == b {
		m++
	} else if b == marker[0] {
		m = 1
	} else {
		m = 0
	}

	if m == len(marker) {
		m = 0
		s.rawEndPending = true
		s.state = gapState
	}
	*matched = m
	return false
}

func (s *htmlScanner) scriptCodeByte(b byte) {
	switch b {
	case '"':
		s.jsBackslash = false
		s.state = stScriptDQ
	case '\'':
		s.jsBackslash = false
		s.state = stScriptSQ
	case '`':
		s.state = stScriptTpl
	case '/':
		// We do not parse JavaScript grammar: '/' might begin a regular
		// expression, a comment or a division. String boundaries after it
		// cannot be proven, so every later variable slot in this script is
		// rejected until </script>.
		s.jsTainted = true
		s.state = stScriptTaint
	}
}

// scriptStringByte is shared by single and double quoted strings. A trailing
// backslash at a chunk boundary sets jsBackslash; the analyzer rejects a
// variable that would otherwise be consumed by that escape.
func (s *htmlScanner) scriptStringByte(b byte, closeState scanState) {
	if s.jsBackslash {
		s.jsBackslash = false
		return
	}
	if b == '\\' {
		s.jsBackslash = true
		return
	}
	if b == '\n' || b == '\r' {
		// A raw newline cannot legally close a quoted string; fail closed.
		s.state = stScriptTaint
		return
	}
	if (closeState == stScriptDQ && b == '"') || (closeState == stScriptSQ && b == '\'') {
		s.state = stScriptCode
	}
}
