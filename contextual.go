package pongo2

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// escapeKind identifies the concrete place a variable output is emitted into
// when a TemplateSet runs with contextual escaping enabled.
//
// The kind is determined at compile time by contextAnalyzer and stored on the
// analyzed template; at execution time nodeVariable (and the firstof/cycle
// tags) use it to pick the matching escape function.
type escapeKind int

const (
	// escapeHTMLText: variable is part of element text content.
	// Example: <p>{{ x }}</p>
	escapeHTMLText escapeKind = iota

	// escapeHTMLAttr: variable is inside a quoted value of a non-URL
	// attribute (including data-* attributes).
	// Example: <input value="{{ x }}">
	escapeHTMLAttr

	// escapeURLAttr: variable is inside the quoted value of a URL-bearing
	// attribute. HTML attribute escaping is applied and the runtime value is
	// additionally checked for a safe protocol; values that start with an
	// unsafe scheme (javascript:, data:, ...) are rejected at execution.
	// Example: <a href="{{ x }}">
	escapeURLAttr

	// escapeJSString: variable is inside a single or double quoted
	// JavaScript string literal within a <script> element.
	// Example: <script>var s = "{{ x }}";</script>
	escapeJSString
)

// contextualFrame holds the compile-time result of contextual analysis for one
// template rendered from a given entry context. slots maps each analyzed
// output node to the escape kind of the place it is emitted into.
//
// Every static {% include %} re-analyzes the included template from the
// include site's context and overwrites that template's frame, so the same
// partial included at different contexts is annotated per site. Nested static
// includes chain naturally through their own frames.
type contextualFrame struct {
	slots map[INode]escapeKind
}

func newContextualFrame() *contextualFrame {
	return &contextualFrame{slots: make(map[INode]escapeKind)}
}

// contextualSlot walks the active frame stack (top frame last) and returns the
// escape kind recorded for node in the most specific frame. ok is false when
// the node has no contextual annotation (e.g. a macro body rendered in a frame
// that did not analyze it); callers fall back to regular HTML escaping then.
func contextualSlot(stack []*contextualFrame, node INode) (kind escapeKind, ok bool) {
	for i := len(stack) - 1; i >= 0; i-- {
		if kind, ok = stack[i].slots[node]; ok {
			return kind, true
		}
	}
	return escapeHTMLText, false
}

// contextualEscape renders value for the given compile-time context.
//
// Unlike ordinary autoescaping it never honors a "safe" mark: contextual
// escaping is a strict mode that validates every inserted value against the
// context it lands in, so |safe cannot be used to bypass e.g. the URL
// protocol check or JavaScript string escaping.
//
// Non-string values are rendered as-is: numbers and booleans cannot contain
// any context-breaking character.
func contextualEscape(kind escapeKind, value *Value) (string, error) {
	if !value.IsString() {
		return value.String(), nil
	}
	s := value.String()

	switch kind {
	case escapeHTMLText, escapeHTMLAttr:
		return htmlEscapeReplacer.Replace(s), nil
	case escapeURLAttr:
		if err := checkSafeURL(s); err != nil {
			return "", err
		}
		return htmlEscapeReplacer.Replace(s), nil
	case escapeJSString:
		return escapeJSStringLiteral(s), nil
	default:
		panic("pongo2: unknown contextual escape kind")
	}
}

// allowedURLSchemes is the allowlist of protocols accepted in URL attributes.
// Everything that is not an absolute reference to one of these schemes (i.e.
// relative, root-relative or same-scheme URLs) is rejected at execution.
//
// "data:" is intentionally absent (it allows scriptable payloads);
// "vbscript:" and "javascript:" are likewise rejected.
var allowedURLSchemes = map[string]struct{}{
	"http":     {},
	"https":    {},
	"ftp":      {},
	"ftps":     {},
	"mailto":   {},
	"tel":      {},
	"news":     {},
	"sms":      {},
	"irc":      {},
	"git":      {},
	"ssh":      {},
	"feed":     {},
	"xmpp":     {},
	"gopher":   {},
	"nntp":     {},
	"steam":    {},
	"magnet":   {},
	"webcal":   {},
	"file":     {},
	"cid":      {},
	"mid":      {},
	"prospero": {},
}

// checkSafeURL validates a value that is about to be inserted into a quoted
// URL attribute. Encoding the surrounding quotes is not enough: a value like
// "javascript:alert(1)" stays inside the attribute but executes when followed.
//
// The check mirrors what a browser does when resolving the attribute value:
// control characters (including NUL and tab) are rejected, leading
// control/whitespace is stripped the same way HTML attribute resolution
// strips it, and a scheme is only accepted when it is on the allowlist.
// URLs without any scheme part (relative/root-relative/scheme-relative,
// anchors and query strings) are always accepted.
func checkSafeURL(raw string) error {
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("contextual escaping: URL contains control character U+%04X, which is not allowed in a URL attribute", r)
		}
	}

	s := strings.TrimLeft(raw, " \t\n\r\f")

	// Cut at the first scheme-terminator. A slash (or backslash) before any
	// colon means the value is a relative/network reference and never carries
	// a scheme; browsers also treat backslashes like slashes for special URLs.
	end := strings.IndexAny(s, ":/\\")
	if end < 0 {
		return nil
	}
	if s[end] != ':' {
		return nil
	}

	scheme := strings.ToLower(s[:end])
	if scheme == "" {
		return fmt.Errorf("contextual escaping: URL %q has an empty protocol scheme", raw)
	}
	for i := 0; i < len(scheme); i++ {
		c := scheme[i]
		isAlpha := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		if i > 0 && (isAlpha || isDigit || c == '+' || c == '-' || c == '.') {
			continue
		}
		if i == 0 && isAlpha {
			continue
		}
		// Not a syntactically valid scheme; treat the colon as ordinary text.
		return nil
	}

	if _, ok := allowedURLSchemes[scheme]; ok {
		return nil
	}
	return fmt.Errorf("contextual escaping: URL %q uses disallowed protocol %q in a URL attribute", raw, scheme)
}

// escapeJSStringLiteral escapes a string for safe inclusion inside a single
// or double quoted JavaScript string literal within a <script> element.
//
// It uses \uXXXX escapes for every character that could close the string,
// introduce a line terminator, break out of the script element or confuse an
// HTML parser. Letters/digits/punctuation are written through. Invalid UTF-8
// is replaced with the replacement character to avoid emitting broken bytes.
func escapeJSStringLiteral(input string) string {
	var b strings.Builder
	b.Grow(len(input))

	for i := 0; i < len(input); {
		c, size := utf8.DecodeRuneInString(input[i:])
		i += size

		if escaped, ok := jsStringEscapeFor(c, size); ok {
			b.WriteString(escaped)
			continue
		}
		b.WriteRune(c)
	}

	return b.String()
}

// jsStringEscapeFor returns the literal sequence to emit for a rune inside a
// quoted JavaScript string. ok is false when the rune may pass through.
func jsStringEscapeFor(c rune, size int) (string, bool) {
	if c == utf8.RuneError && size == 1 {
		return `\uFFFD`, true
	}
	if esc, ok := jsStringEscapeTable[c]; ok {
		return esc, true
	}
	if c < 0x20 || c == 0x7f || (c >= 0x80 && c <= 0x9f) {
		return fmt.Sprintf(`\u%04X`, c), true
	}
	return "", false
}

// jsStringEscapeTable covers every fixed (\uXXXX or literal) escape.
var jsStringEscapeTable = map[rune]string{
	'\\':     `\u005C`,
	'"':      `\u0022`,
	'\'':     `\u0027`,
	'`':      `\u0060`,
	'<':      `\u003C`,
	'>':      `\u003E`,
	'&':      `\u0026`,
	'=':      `\u003D`,
	'-':      `\u002D`,
	';':      `\u003B`,
	'/':      `\u002F`,
	'\n':     `\u000A`,
	'\r':     `\u000D`,
	'\t':     `\u0009`,
	'\u2028': `\u2028`,
	'\u2029': `\u2029`,
}

// writeContextual applies contextual escaping to value for node when the
// active frame stack knows node's context. It reports handled=false in normal
// (non-contextual) mode so callers keep their existing autoescape logic.
func writeContextual(ctx *ExecutionContext, writer TemplateWriter, node INode, value *Value, token *Token) (handled bool, err error) {
	kind, ok := contextualSlot(ctx.escapeFrames(), node)
	if !ok {
		return false, nil
	}
	out, err := contextualEscape(kind, value)
	if err != nil {
		return true, ctx.OrigError(err, token)
	}
	_, werr := writer.WriteString(out)
	return true, werr
}
