package pongo2

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// zgotmplz is the neutralization marker emitted for URL attribute values that
// fail the protocol check. It mirrors Go's html/template behavior: the value
// becomes a harmless same-document fragment instead of an executable URL.
const zgotmplz = "#ZgotmplZ"

// contextualEscape applies the escaper associated with kind. It is used at
// execution time when contextual escaping is enabled.
//
// Unlike ordinary autoescaping it deliberately ignores both the "safe"
// filter/marker and the autoescape setting: a value marked safe for HTML text
// is not automatically safe inside a JavaScript string or an href, so the
// compile-time-assigned context always wins.
func contextualEscape(val string, kind escapeKind) string {
	switch kind {
	case escHTMLText, escHTMLAttr:
		return htmlEscapeReplacer.Replace(val)
	case escURLAttr:
		return escapeURLAttr(val)
	case escJSString:
		return escapeJSStringLiteral(val)
	default:
		return val
	}
}

// escapeURLAttr validates the URL scheme of val and then HTML-attribute-escapes
// it. Values that do not parse as an acceptable URL are neutralized.
func escapeURLAttr(val string) string {
	if !safeURL(val) {
		return zgotmplz
	}
	return htmlEscapeReplacer.Replace(val)
}

// safeURL reports whether val may appear in an href/src-style attribute.
//
// Rules (intentionally conservative):
//   - relative URLs (no scheme) are allowed;
//   - only http, https, ftp, ftps and mailto schemes are allowed;
//   - leading control characters/whitespace are trimmed first;
//   - anything that looks like an unknown scheme (e.g. "javascript:") fails;
//   - embedded control characters fail.
func safeURL(val string) bool {
	v := strings.TrimLeft(val, " \t\n\r\f\v")
	v = strings.TrimRight(v, " \t\n\r\f\v")
	if v == "" {
		return true
	}

	// Fragment, path, network-path and same-document references are fine.
	switch v[0] {
	case '/', '#', '?', '.':
		return !containsControl(v)
	}

	// Look for a scheme: RFC 3986 requires scheme = ALPHA *( ALPHA / DIGIT /
	// "+" / "-" / "." ). A colon found after only such bytes defines a scheme.
	for i := 0; i < len(v); i++ {
		b := v[i]
		if b == ':' {
			scheme := strings.ToLower(v[:i])
			return isAllowedScheme(scheme) && !containsControl(v)
		}
		if b == '/' || b == '?' || b == '#' {
			break
		}
		if !(isSchemeByte(b)) {
			// Not a scheme prefix (e.g. contains a quote, space or '<'); treat
			// the value as a relative reference.
			break
		}
	}

	// No scheme delimiter before the path: relative reference like
	// "foo/bar:a" or "a b:c" is fine.
	return !containsControl(v)
}

func isSchemeByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '+' || b == '-' || b == '.'
}

var allowedURLSchemes = map[string]struct{}{
	"http":   {},
	"https":  {},
	"ftp":    {},
	"ftps":   {},
	"mailto": {},
}

func isAllowedScheme(scheme string) bool {
	_, ok := allowedURLSchemes[scheme]
	return ok
}

func containsControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// escapeJSStringLiteral escapes a value for safe inclusion in a single- or
// double-quoted JavaScript string. Every byte outside printable ASCII that is
// not an ordinary letter/digit gets a deterministic escape, which also makes
// the output safe from premature "</script>" termination.
func escapeJSStringLiteral(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		c, size := utf8.DecodeRuneInString(s[i:])
		if c == utf8.RuneError && size == 1 {
			b.WriteString(`\uFFFD`)
			i += size
			continue
		}

		switch {
		case c == '\\':
			b.WriteString(`\u005C`)
		case c == '\'':
			b.WriteString(`\u0027`)
		case c == '"':
			b.WriteString(`\u0022`)
		case c == '`':
			b.WriteString(`\u0060`)
		case c == '<':
			b.WriteString(`\u003C`)
		case c == '>':
			b.WriteString(`\u003E`)
		case c == '&':
			b.WriteString(`\u0026`)
		case c == '=':
			b.WriteString(`\u003D`)
		case c == '\n':
			b.WriteString(`\u000A`)
		case c == '\r':
			b.WriteString(`\u000D`)
		case c == '\t':
			b.WriteString(`\u0009`)
		case c == 0x2028:
			b.WriteString(`\u2028`)
		case c == 0x2029:
			b.WriteString(`\u2029`)
		case c < 0x20 || c == 0x7f || (c >= 0x80 && c <= 0x9f):
			fmt.Fprintf(&b, `\u%04X`, c)
		default:
			b.WriteRune(c)
		}

		i += size
	}

	return b.String()
}
