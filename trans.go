package pongo2

import (
	"errors"
	"sort"
	"strings"
)

// Placeholders inside translation messages use gettext-style syntax:
//
//	"%(name)s"
//
// It is deliberately not pongo2 template syntax, so translation files can
// never contain executable template expressions. A translation may reorder
// or repeat these placeholders, but it must not introduce new ones and it
// must not omit declared ones.

// TranslateRequest is passed to a Translator for a single {% trans %} block.
type TranslateRequest struct {
	// Singular and Plural are the stable source messages extracted at
	// compile time. Placeholders use the "%(name)s" format. Plural is empty
	// for non-plural blocks.
	Singular string
	Plural   string

	// HasPlural is true when the block declares a "plural" section.
	HasPlural bool

	// Count is the value of the optional "count" expression. It is evaluated
	// exactly once per rendering and is nil when no count is declared.
	Count *int

	// Language is the per-request language selected via TranslateOptions.
	Language string

	// Placeholders are the named placeholders declared in the block, in
	// first-occurrence order, without duplicates.
	Placeholders []string
}

// Translator resolves a message for the requested language.
//
// Result holds the translated forms using "%(name)s" placeholders. For
// non-plural messages it must contain exactly one form; for plural messages
// it must contain exactly two forms (singular, plural). PluralIndex selects
// the form to render and must be a valid index into Result (0 or 1).
//
// Return ErrNoTranslation means that no translation is available; the
// configured TranslateMissingPolicy then decides what happens. Any other
// error, an illegal plural index, unknown/missing placeholders or an
// unexpected number of forms is handled by TranslateErrorPolicy.
//
// A Translator never receives runtime variable or filter values and is never
// invoked during message extraction.
type Translator interface {
	Translate(req TranslateRequest) (result []string, pluralIndex int, err error)
}

// TranslatorFunc adapts a plain function to the Translator interface.
type TranslatorFunc func(req TranslateRequest) ([]string, int, error)

// Translate implements Translator.
func (fn TranslatorFunc) Translate(req TranslateRequest) ([]string, int, error) {
	return fn(req)
}

// ErrNoTranslation is returned by a Translator when a message has no
// translation for the requested language. It is handled separately from
// hard errors according to TranslateMissingPolicy.
var ErrNoTranslation = errors.New("pongo2: no translation available")

// TranslateMissingPolicy selects the behaviour when a translator returns
// ErrNoTranslation or no translator is configured.
type TranslateMissingPolicy int

const (
	// TranslateMissingFallbackSource renders the original source message
	// (singular or plural selected from Count). This is the zero value.
	TranslateMissingFallbackSource TranslateMissingPolicy = iota

	// TranslateMissingEmpty renders the empty string for untranslated
	// messages.
	TranslateMissingEmpty

	// TranslateMissingError returns an execution error for untranslated
	// messages.
	TranslateMissingError
)

// TranslateErrorPolicy selects the behaviour when a translation result is
// invalid: a hard translator error, an illegal plural index,
// unknown/missing placeholders, or an unexpected number of translated forms.
type TranslateErrorPolicy int

const (
	// TranslateErrorFallbackSource falls back to the original source
	// message. This is the zero value.
	TranslateErrorFallbackSource TranslateErrorPolicy = iota

	// TranslateErrorEmpty renders the empty string for invalid translations.
	TranslateErrorEmpty

	// TranslateErrorFail returns an execution error for invalid translations.
	TranslateErrorFail
)

// TranslateOptions configures translation for one template execution.
//
// Options are scoped to a single Execute call: nested blocks and includes
// inherit them, but separate requests never share language or translator
// state.
type TranslateOptions struct {
	// Translator resolves messages at render time. If nil, messages follow
	// MissingPolicy.
	Translator Translator

	// Language is forwarded verbatim to the translator.
	Language string

	// MissingPolicy handles ErrNoTranslation and a missing translator.
	MissingPolicy TranslateMissingPolicy

	// ErrorPolicy handles invalid translator results.
	ErrorPolicy TranslateErrorPolicy
}

// transContextKey stores TranslateOptions in the private execution context.
const transContextKey = "__pongo2_translate_options__"

func transOptionsFromContext(ctx *ExecutionContext) TranslateOptions {
	if v, ok := ctx.Private[transContextKey]; ok {
		if opts, ok := v.(TranslateOptions); ok {
			return opts
		}
	}
	return TranslateOptions{}
}

// transSegment is one piece of a translation-block body: literal text or a
// named placeholder backed by a runtime expression.
type transSegment struct {
	literal     string
	placeholder string
	expr        IEvaluator
}

// canonicalizeTransText normalizes body text into a stable message: runs of
// whitespace collapse to a single space and the result is trimmed.
func canonicalizeTransText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// transMessageToken is either a literal piece of a message or a placeholder.
type transMessageToken struct {
	literal     string
	placeholder string
}

// scanTransMessage splits a message into literal/placeholder tokens and
// returns the placeholder names in first-occurrence order. A "%(" must be
// followed by a valid identifier and ")s"; any other '%' is literal text.
func scanTransMessage(msg string) ([]transMessageToken, []string) {
	var tokens []transMessageToken
	var order []string
	seen := map[string]bool{}

	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			tokens = append(tokens, transMessageToken{literal: literal.String()})
			literal.Reset()
		}
	}

	for i := 0; i < len(msg); {
		name, next, ok := matchTransPlaceholder(msg, i)
		if !ok {
			literal.WriteByte(msg[i])
			i++
			continue
		}
		flush()
		tokens = append(tokens, transMessageToken{placeholder: name})
		if !seen[name] {
			seen[name] = true
			order = append(order, name)
		}
		i = next
	}
	flush()
	return tokens, order
}

// matchTransPlaceholder reports whether a "%(name)s" placeholder starts at i.
// On success it returns the name and the index just past the placeholder.
func matchTransPlaceholder(msg string, i int) (string, int, bool) {
	if i+4 > len(msg) || msg[i] != '%' || msg[i+1] != '(' {
		return "", i, false
	}
	j := i + 2
	start := j
	for j < len(msg) && isTransIdentifierChar(msg[j], j-start) {
		j++
	}
	if j == start || j+2 > len(msg) || msg[j] != ')' || msg[j+1] != 's' {
		return "", i, false
	}
	return msg[start:j], j + 2, true
}

func isTransIdentifierChar(c byte, pos int) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(pos > 0 && c >= '0' && c <= '9')
}

// validateTransMessage checks one translated form: no unknown placeholders
// and no missing required placeholders.
func validateTransMessage(msg string, declared []string) error {
	_, usedOrder := scanTransMessage(msg)
	used := make(map[string]bool, len(usedOrder))
	for _, name := range usedOrder {
		used[name] = true
	}

	var unknown []string
	for _, name := range usedOrder {
		if !sliceContains(declared, name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return errors.New("trans: unknown placeholder(s) in translation: " + strings.Join(unknown, ", "))
	}

	var missing []string
	for _, name := range declared {
		if !used[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return errors.New("trans: missing required placeholder(s) in translation: " + strings.Join(missing, ", "))
	}
	return nil
}

func sliceContains(s []string, v string) bool {
	for _, item := range s {
		if item == v {
			return true
		}
	}
	return false
}

// renderTransMessage substitutes placeholders with resolved values. The
// message text is emitted verbatim and is never parsed as a template again.
func renderTransMessage(msg string, values map[string]string) string {
	tokens, _ := scanTransMessage(msg)
	var b strings.Builder
	for _, tok := range tokens {
		if tok.placeholder != "" {
			b.WriteString(values[tok.placeholder])
		} else {
			b.WriteString(tok.literal)
		}
	}
	return b.String()
}
