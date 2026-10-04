package pongo2

import "fmt"

// Execute renders the translation block. The count expression is evaluated
// exactly once; placeholder values follow the surrounding autoescape rules;
// translated text is treated as plain text and never re-parsed.
func (node *tagTransNode) Execute(ctx *ExecutionContext, writer TemplateWriter) error {
	opts := transOptionsFromContext(ctx)

	count, err := node.evalCount(ctx)
	if err != nil {
		return err
	}

	values, err := node.resolvePlaceholders(ctx, count)
	if err != nil {
		return err
	}

	sourceForm := node.sourceForm(count)

	if opts.Translator == nil {
		return node.applyMissing(writer, opts, values, sourceForm,
			fmt.Errorf("no translator configured in this execution context"))
	}

	req := TranslateRequest{
		Singular:     node.singularMsg,
		Plural:       node.pluralMsg,
		HasPlural:    node.hasPlural,
		Count:        count,
		Language:     opts.Language,
		Placeholders: append([]string(nil), node.placeholders...),
	}

	result, pluralIndex, terr := opts.Translator.Translate(req)

	if terr != nil {
		if terr == ErrNoTranslation {
			return node.applyMissing(writer, opts, values, sourceForm, nil)
		}
		return node.applyError(writer, opts, values, sourceForm,
			fmt.Errorf("translator error: %w", terr))
	}

	wantForms := 1
	if node.hasPlural {
		wantForms = 2
	}
	if len(result) != wantForms {
		return node.applyError(writer, opts, values, sourceForm,
			fmt.Errorf("translator returned %d forms, expected %d", len(result), wantForms))
	}
	if pluralIndex < 0 || pluralIndex >= len(result) || (!node.hasPlural && pluralIndex != 0) {
		return node.applyError(writer, opts, values, sourceForm,
			fmt.Errorf("illegal plural index %d (have %d form(s))", pluralIndex, len(result)))
	}

	chosen := result[pluralIndex]
	if verr := validateTransMessage(chosen, node.placeholders); verr != nil {
		return node.applyError(writer, opts, values, sourceForm, verr)
	}

	_, err = writer.WriteString(renderTransMessage(chosen, values))
	return err
}

// evalCount evaluates the count expression exactly once and returns nil when
// no count was declared.
func (node *tagTransNode) evalCount(ctx *ExecutionContext) (*int, error) {
	if node.countExpr == nil {
		return nil, nil
	}
	val, err := node.countExpr.Evaluate(ctx)
	if err != nil {
		return nil, err
	}
	if !val.IsInteger() {
		return nil, ctx.Error(fmt.Sprintf(
			"trans 'count' expression must evaluate to an integer, got %s", typeName(val)), node.position)
	}
	n := val.Integer()
	return &n, nil
}

// resolvePlaceholders evaluates every declared placeholder expression once
// and applies the current autoescape policy to string values. The already
// evaluated count fills a count-bound placeholder without re-evaluation.
func (node *tagTransNode) resolvePlaceholders(ctx *ExecutionContext, count *int) (map[string]string, error) {
	exprByName := make(map[string]IEvaluator, len(node.placeholders))
	for _, segments := range [][]transSegment{node.singular, node.plural} {
		for _, seg := range segments {
			if seg.placeholder != "" && exprByName[seg.placeholder] == nil {
				exprByName[seg.placeholder] = seg.expr
			}
		}
	}

	values := make(map[string]string, len(node.placeholders))
	for _, name := range node.placeholders {
		if node.countExpr != nil && name == node.countName {
			values[name] = fmt.Sprintf("%d", *count)
			continue
		}
		val, err := exprByName[name].Evaluate(ctx)
		if err != nil {
			return nil, err
		}
		if ctx.Autoescape && val.IsString() && !val.safe {
			val, err = ctx.template.set.ApplyFilter("escape", val, nil)
			if err != nil {
				return nil, err
			}
		}
		values[name] = val.String()
	}
	return values, nil
}

// sourceForm selects the singular/plural source message used in fallbacks.
func (node *tagTransNode) sourceForm(count *int) string {
	if node.hasPlural && count != nil && *count != 1 {
		return node.pluralMsg
	}
	return node.singularMsg
}

func (node *tagTransNode) applyMissing(writer TemplateWriter, opts TranslateOptions,
	values map[string]string, sourceForm string, cause error) error {
	switch opts.MissingPolicy {
	case TranslateMissingError:
		return node.executionError(cause)
	case TranslateMissingEmpty:
		return nil
	default:
		_, err := writer.WriteString(renderTransMessage(sourceForm, values))
		return err
	}
}

func (node *tagTransNode) applyError(writer TemplateWriter, opts TranslateOptions,
	values map[string]string, sourceForm string, cause error) error {
	switch opts.ErrorPolicy {
	case TranslateErrorFail:
		return node.executionError(cause)
	case TranslateErrorEmpty:
		return nil
	default:
		_, err := writer.WriteString(renderTransMessage(sourceForm, values))
		return err
	}
}

func (node *tagTransNode) executionError(cause error) error {
	if cause == nil {
		cause = fmt.Errorf("translation unavailable")
	}
	return &Error{
		Sender:    "tag:trans",
		Line:      node.position.Line,
		Column:    node.position.Col,
		Token:     node.position,
		OrigError: cause,
	}
}

func typeName(v *Value) string {
	if v.IsNil() {
		return "nil"
	}
	return v.getResolvedValue().Kind().String()
}
