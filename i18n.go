package pongo2

import (
	"errors"
	"regexp"
)

// i18nContextKey is the Public-context key used to pass a *I18nRuntime to a
// single template execution. It is a valid identifier so it never clashes with
// the context-key validation performed before rendering.
const i18nContextKey = "pongo2_i18n"

// placeholderPattern matches named placeholders of the form {name} inside
// translation message texts. Placeholders are plain text, not template
// expressions: the name has to be a valid identifier and nothing inside the
// braces is evaluated.
var placeholderPattern = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

// ErrNoTranslation is returned by a Translator to signal that no translation
// is available for the requested message. The {% trans %} tag reacts to it
// according to the configured MissingStrategy instead of treating it like an
// arbitrary translator failure.
var ErrNoTranslation = errors.New("pongo2: no translation available")

// FailureStrategy tells the {% trans %} tag what to render when translation
// cannot proceed as expected. The zero value (FailureStrategyError) returns
// the error from Execute.
type FailureStrategy int

const (
	// FailureStrategyError aborts rendering and returns the error.
	FailureStrategyError FailureStrategy = iota

	// FailureStrategyEmpty renders an empty string.
	FailureStrategyEmpty

	// FailureStrategySource renders the untranslated source text, with the
	// resolved placeholders substituted using the current escaping rules.
	FailureStrategySource
)

// TranslationMessage is a single translatable block extracted at compile time.
// No variables, filters or translators are evaluated during extraction: the
// fields contain literal source text only.
type TranslationMessage struct {
	// Singular is the source text used when no count is given or when the
	// requested plural form is singular.
	Singular string

	// Plural is the source text used for non-singular counts. It is empty when
	// the block declares no count.
	Plural string

	// Placeholders are all distinct placeholder names declared across the
	// singular and plural texts, in first-appearance order.
	Placeholders []string
}

// HasPlural reports whether the message declares a plural source text.
func (m TranslationMessage) HasPlural() bool {
	return m.Plural != ""
}

// TranslationRequest is passed to the translator for every rendered {% trans %}
// block.
type TranslationRequest struct {
	// Message is the compile-time extracted source message.
	Message TranslationMessage

	// Locale is the language requested for this execution, as provided through
	// WithI18n.
	Locale string

	// HasCount reports whether the block was rendered with a count.
	HasCount bool

	// Count is the evaluated count expression, evaluated exactly once per
	// rendering. It is zero when HasCount is false.
	Count int64
}

// TranslationResult is a translator's answer.
type TranslationResult struct {
	// Forms holds the translated forms. A singular-only message expects one
	// form; a plural message expects two (index 0 singular, index 1 plural).
	// Form selects the entry to use.
	Forms []string

	// Form selects the plural form of the translation. It is ignored for
	// singular-only messages (it is expected to be 0).
	Form int
}

// Translator resolves a single message for the requested locale. Return
// ErrNoTranslation to trigger the configured MissingStrategy; returning an
// illegal Form index (or forms that do not honor the declared placeholders)
// triggers the configured InvalidStrategy.
type Translator func(req TranslationRequest) (TranslationResult, error)

// I18nRuntime configures translation for one template execution. It is passed
// through the render context using WithI18n and never mutates the template, so
// concurrent executions with different locales do not interfere.
type I18nRuntime struct {
	// Locale selects the requested language for this execution.
	Locale string

	// Translator is called for every {% trans %} block during rendering.
	Translator Translator

	// MissingStrategy selects the result when the translator returns
	// ErrNoTranslation. Defaults to FailureStrategyError.
	MissingStrategy FailureStrategy

	// InvalidStrategy selects the result when the translation cannot be used
	// (translator error, illegal plural form index, unknown or missing required
	// placeholder, or a missing placeholder value). Defaults to
	// FailureStrategyError.
	InvalidStrategy FailureStrategy
}

// WithI18n returns a context copy carrying the given translation runtime. Use
// the returned context when calling a template's Execute method.
func WithI18n(parent Context, rt *I18nRuntime) Context {
	capacity := 1
	if parent != nil {
		capacity += len(parent)
	}
	out := make(Context, capacity)
	for k, v := range parent {
		out[k] = v
	}
	out[i18nContextKey] = rt
	return out
}

// i18nFromContext returns the per-execution runtime, or nil when none was given.
func i18nFromContext(ctx Context) *I18nRuntime {
	rt, _ := ctx[i18nContextKey].(*I18nRuntime)
	return rt
}

// extractPlaceholders returns the distinct {name} placeholders of text in
// first-appearance order.
func extractPlaceholders(text string) []string {
	matches := placeholderPattern.FindAllString(text, -1)
	seen := make(map[string]struct{}, len(matches))
	placeholders := make([]string, 0, len(matches))
	for _, match := range matches {
		name := match[1 : len(match)-1]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		placeholders = append(placeholders, name)
	}
	return placeholders
}

// unionPlaceholders merges placeholder lists preserving first-appearance order.
func unionPlaceholders(lists ...[]string) []string {
	seen := make(map[string]struct{})
	union := make([]string, 0)
	for _, list := range lists {
		for _, name := range list {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			union = append(union, name)
		}
	}
	return union
}

// validateForms checks the selected translation against the source message.
// Translations may reorder or repeat declared placeholders, but they may not
// introduce unknown placeholders or drop placeholders required by the selected
// source form.
func validateForms(res TranslationResult, msg TranslationMessage) error {
	if len(res.Forms) == 0 {
		return errors.New("translator returned no translation forms")
	}
	formCount := 1
	if msg.HasPlural() {
		formCount = 2
	}
	// Languages without plural distinction may answer a plural message with a
	// single form, but they may not invent extra forms beyond the source.
	if len(res.Forms) < 1 || len(res.Forms) > formCount {
		return errors.New("translator returned an illegal number of plural forms")
	}
	// A single-form answer to a plural message can only use the singular form.
	if res.Form < 0 || res.Form >= len(res.Forms) {
		return errors.New("translator returned an illegal plural form index")
	}
	if res.Form >= formCount {
		return errors.New("translator returned an illegal plural form for this message")
	}
	text := res.Forms[res.Form]
	if text == "" {
		return errors.New("translator returned an empty translation")
	}

	source := msg.Singular
	if msg.HasPlural() && res.Form == 1 {
		source = msg.Plural
	}
	required := make(map[string]struct{})
	for _, name := range extractPlaceholders(source) {
		required[name] = struct{}{}
	}
	allowed := make(map[string]struct{}, len(msg.Placeholders))
	for _, name := range msg.Placeholders {
		allowed[name] = struct{}{}
	}
	for _, name := range extractPlaceholders(text) {
		if _, ok := allowed[name]; !ok {
			return errors.New("translation uses unknown placeholder {" + name + "}")
		}
		delete(required, name)
	}
	for name := range required {
		return errors.New("translation is missing required placeholder {" + name + "}")
	}
	return nil
}
