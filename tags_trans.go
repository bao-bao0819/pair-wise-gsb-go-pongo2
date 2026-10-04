package pongo2

import (
	"errors"
	"fmt"
	"strings"
)

const (
	transTagEnd    = "endtrans"
	transTagPlural = "plural"
)

// tagTransNode implements the {% trans %} block.
//
// Block contents are collected as raw text tokens instead of being parsed as
// template nodes, so neither the source nor any translation can contain
// executable template expressions. The only substitution points are
// {placeholder} references, which are resolved at render time and formatted
// using the surrounding auto-escape configuration.
type tagTransNode struct {
	position *Token

	countExpr IEvaluator

	singular     string
	plural       string
	singularPH   []string
	pluralPH     []string
	message      TranslationMessage
	sourceByForm []string
}

// transFailure bundles the inputs needed to apply a failure strategy.
type transFailure struct {
	strategy FailureStrategy
	cause    error
	hasCount bool
	count    int64
	form     int
}

func tagTransParser(doc *Parser, start *Token, arguments *Parser) (INodeTag, error) {
	node := &tagTransNode{position: start}

	if arguments.Remaining() > 0 {
		if kw := arguments.MatchType(TokenIdentifier); kw == nil || kw.Val != "count" {
			return nil, arguments.Error("Only an optional 'count = <expression>' argument is allowed for the trans-tag.", nil)
		}
		if arguments.Match(TokenSymbol, "=") == nil {
			return nil, arguments.Error("Expected '=' after 'count' in the trans-tag.", nil)
		}
		expr, err := arguments.ParseExpression()
		if err != nil {
			return nil, err
		}
		node.countExpr = expr
		if arguments.Remaining() > 0 {
			return nil, arguments.Error("Only one 'count = <expression>' argument is allowed for the trans-tag.", nil)
		}
	}

	singular, plural, err := consumeTransBody(doc, start)
	if err != nil {
		return nil, err
	}

	node.singular = singular
	node.singularPH = extractPlaceholders(singular)
	node.sourceByForm = []string{singular}

	if node.countExpr != nil {
		if plural == "" {
			return nil, doc.Error("The trans-tag with a count requires a 'plural' section.", start)
		}
		node.plural = plural
		node.pluralPH = extractPlaceholders(plural)
		node.sourceByForm = append(node.sourceByForm, plural)
	} else if plural != "" {
		return nil, doc.Error("The 'plural' section is only allowed when the trans-tag declares a count.", start)
	}

	if strings.TrimSpace(node.singular) == "" {
		return nil, doc.Error("The trans-tag requires a non-empty singular message.", start)
	}

	node.message = TranslationMessage{
		Singular:     node.singular,
		Plural:       node.plural,
		Placeholders: unionPlaceholders(node.singularPH, node.pluralPH),
	}
	doc.template.transMessages = append(doc.template.transMessages, node.message)

	return node, nil
}

// consumeTransBody gathers the literal text of the block up to {% endtrans %},
// stopping at the first {% plural %} divider. Any variable output, comment
// survivor or other tag is rejected so translation text can never carry
// executable template syntax.
func consumeTransBody(doc *Parser, start *Token) (singular, plural string, err error) {
	var singularParts, pluralParts []string
	sawPlural := false

	for doc.Remaining() > 0 {
		tok := doc.Current()

		switch {
		case tok.Typ == TokenHTML:
			if sawPlural {
				pluralParts = append(pluralParts, tok.Val)
			} else {
				singularParts = append(singularParts, tok.Val)
			}
			doc.Consume()

		case tok.Typ == TokenSymbol && tok.Val == "{%":
			name := doc.GetR(1)
			if name == nil || name.Typ != TokenIdentifier {
				return "", "", doc.Error("Malformed tag inside trans-block.", tok)
			}
			switch name.Val {
			case transTagEnd:
				if err := consumeSimpleEndTag(doc, transTagEnd); err != nil {
					return "", "", err
				}
				return strings.TrimSpace(strings.Join(singularParts, "")),
					strings.TrimSpace(strings.Join(pluralParts, "")), nil
			case transTagPlural:
				if sawPlural {
					return "", "", doc.Error("Only one 'plural' section is allowed in the trans-tag.", name)
				}
				if err := consumeSimpleEndTag(doc, transTagPlural); err != nil {
					return "", "", err
				}
				sawPlural = true
			default:
				return "", "", doc.Error(
					"Only 'plural' and 'endtrans' tags are allowed inside a trans-block; found '"+name.Val+"'.",
					name)
			}

		default:
			return "", "", doc.Error(
				"Template expressions are not allowed inside the trans-block; declare named {placeholders} instead.",
				tok)
		}
	}

	return "", "", doc.Error("Unexpected EOF, expected 'endtrans'.", start)
}

// consumeSimpleEndTag consumes a '{%' name ... '%}' divider or end tag and
// rejects any arguments on it.
func consumeSimpleEndTag(doc *Parser, name string) error {
	tagStart := doc.Current()
	doc.ConsumeN(2) // '{%' name
	if doc.Match(TokenSymbol, "%}") != nil {
		return nil
	}
	if doc.Remaining() == 0 {
		return doc.Error("Unexpected EOF in trans-block tag.", tagStart)
	}
	return doc.Error("The '"+name+"' tag does not accept arguments.", doc.Current())
}

func (node *tagTransNode) Execute(ctx *ExecutionContext, writer TemplateWriter) error {
	hasCount := node.countExpr != nil
	var count int64

	if hasCount {
		value, err := node.countExpr.Evaluate(ctx)
		if err != nil {
			return err
		}
		if !value.IsInteger() {
			return node.transError(errors.New("the 'count' expression must evaluate to an integer"))
		}
		count = int64(value.Integer())
	}

	rt := i18nFromContext(ctx.Public)

	// sourceForm selects the singular/plural source text and is computed once
	// from the single count evaluation.
	sourceForm := 0
	if hasCount && count != 1 {
		sourceForm = 1
	}
	// chosenForm is the plural form selected by the translator (it may differ
	// from sourceForm for languages without plural distinction).
	chosenForm := sourceForm
	var translated string
	var invalidErr error
	var missingStrategy FailureStrategy
	var invalidStrategy FailureStrategy
	if rt != nil {
		missingStrategy = rt.MissingStrategy
		invalidStrategy = rt.InvalidStrategy
	}

	switch {
	case rt == nil || rt.Translator == nil:
		invalidErr = errors.New("no translator configured for the trans-tag")
	default:
		req := TranslationRequest{
			Message:  node.message,
			Locale:   rt.Locale,
			HasCount: hasCount,
			Count:    count,
		}
		res, terr := rt.Translator(req)
		switch {
		case errors.Is(terr, ErrNoTranslation):
			cause := fmt.Errorf("%w: %q", ErrNoTranslation, node.message.Singular)
			return node.renderFailure(writer, ctx, transFailure{
				strategy: missingStrategy,
				cause:    cause,
				hasCount: hasCount,
				count:    count,
				form:     sourceForm,
			})
		case terr != nil:
			invalidErr = terr
		default:
			if verr := validateForms(res, node.message); verr != nil {
				invalidErr = verr
			} else {
				chosenForm = res.Form
				translated = res.Forms[res.Form]
			}
		}
	}

	if invalidErr != nil {
		strategy := FailureStrategyError
		if rt != nil {
			strategy = invalidStrategy
		}
		return node.renderFailure(writer, ctx, transFailure{
			strategy: strategy,
			cause:    invalidErr,
			hasCount: hasCount,
			count:    count,
			form:     sourceForm,
		})
	}

	values, missingRequired, err := node.resolveValues(ctx, hasCount, count, chosenForm)
	if err != nil {
		return err
	}
	if missingRequired != "" {
		cause := errors.New("missing value for placeholder {" + missingRequired + "}")
		return node.renderFailure(writer, ctx, transFailure{
			strategy: invalidStrategy,
			cause:    cause,
			hasCount: hasCount,
			count:    count,
			form:     chosenForm,
		})
	}

	out := substitutePlaceholders(translated, values)
	if _, err := writer.WriteString(out); err != nil {
		return err
	}
	return nil
}

// resolveValues evaluates the placeholders required by the selected form
// exactly once. The special "count" placeholder of a counted block is always
// the evaluated count. Values for other declared placeholders are resolved too
// so a source-text fallback can still substitute them. The first required
// placeholder without a context value is reported.
func (node *tagTransNode) resolveValues(
	ctx *ExecutionContext, hasCount bool, count int64, form int,
) (map[string]string, string, error) {
	values := make(map[string]string, len(node.message.Placeholders))

	required := node.singularPH
	if form == 1 && node.pluralPH != nil {
		required = node.pluralPH
	}
	requiredSet := make(map[string]struct{}, len(required))
	for _, name := range required {
		requiredSet[name] = struct{}{}
	}

	for _, name := range node.message.Placeholders {
		if hasCount && name == "count" {
			values[name] = fmt.Sprintf("%d", count)
			continue
		}
		raw, ok := lookupTransValue(ctx, name)
		if !ok {
			if _, need := requiredSet[name]; need {
				return values, name, nil
			}
			continue
		}
		rendered, err := renderTransValue(raw, ctx, node.position)
		if err != nil {
			return nil, "", err
		}
		values[name] = rendered
	}
	return values, "", nil
}

// lookupTransValue resolves a simple placeholder name from the private (scoped)
// and then the public context, mirroring variable resolution.
func lookupTransValue(ctx *ExecutionContext, name string) (any, bool) {
	if value, ok := ctx.Private[name]; ok {
		return value, true
	}
	value, ok := ctx.Public[name]
	return value, ok
}

// renderTransValue formats a placeholder value with the same escaping rules the
// engine applies to {{ variable }} output.
func renderTransValue(raw any, ctx *ExecutionContext, token *Token) (string, error) {
	value, ok := raw.(*Value)
	if !ok {
		value = AsValue(raw)
	}
	if !value.safe && value.IsString() && ctx.Autoescape {
		escapeFn := ctx.template.set.filters["escape"]
		if escapeFn != nil {
			escaped, err := escapeFn(value, nil)
			if err != nil {
				return "", transError(err, token)
			}
			value = escaped
		}
	}
	return value.String(), nil
}

// substitutePlaceholders replaces declared {name} occurrences. A placeholder
// without a resolved value is left untouched. The result is never re-parsed as
// a template.
func substitutePlaceholders(text string, values map[string]string) string {
	return placeholderPattern.ReplaceAllStringFunc(text, func(match string) string {
		name := match[1 : len(match)-1]
		if value, ok := values[name]; ok {
			return value
		}
		return match
	})
}

// renderFailure applies a caller-selected strategy for a failed translation.
// The source fallback substitutes the same placeholders using source text.
func (node *tagTransNode) renderFailure(writer TemplateWriter, ctx *ExecutionContext, f transFailure) error {
	switch f.strategy {
	case FailureStrategyEmpty:
		return nil
	case FailureStrategySource:
		source := node.sourceByForm[0]
		if f.form >= 0 && f.form < len(node.sourceByForm) {
			source = node.sourceByForm[f.form]
		}
		values, missing, err := node.resolveValues(ctx, f.hasCount, f.count, f.form)
		if err != nil {
			return err
		}
		// A missing required placeholder is left as a literal {name} in the
		// source fallback rather than failing the fallback.
		_ = missing
		_, err = writer.WriteString(substitutePlaceholders(source, values))
		return err
	default:
		return node.transError(f.cause)
	}
}

func (node *tagTransNode) transError(cause error) error {
	return transError(cause, node.position)
}

func transError(cause error, token *Token) error {
	return &Error{
		Filename:  token.Filename,
		Line:      token.Line,
		Column:    token.Col,
		Token:     token,
		Sender:    "tag:trans",
		OrigError: cause,
	}
}

func init() {
	mustRegisterTag("trans", tagTransParser)
}
