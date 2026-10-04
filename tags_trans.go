package pongo2

import (
	"errors"
	"fmt"
	"strings"
)

// transImplicitCountName is the placeholder name bound by "count EXPR"
// without an explicit "as NAME".
const transImplicitCountName = "count"

// TranslationMessage is a stable, extracted translatable message.
type TranslationMessage struct {
	// Singular is the canonical singular source message with "%(name)s"
	// placeholders.
	Singular string

	// Plural is the canonical plural source message. It is empty when the
	// block has no "plural" section.
	Plural string

	// HasPlural reports whether the block has a plural form.
	HasPlural bool

	// Placeholders are the declared placeholder names in first-occurrence
	// order, without duplicates.
	Placeholders []string

	// Line and Column point at the {% trans %} tag in the template source.
	Line   int
	Column int
}

// tagTransNode represents the {% trans %} block.
//
// Syntax:
//
//	{% trans [count EXPR [as NAME]] %}
//	    Hello, {{ name }}!
//	{% plural %}
//	    {{ n }} new messages for {{ name }}
//	{% endtrans %}
//
// Inside the block only literal text and simple variables are allowed; no
// filters, calls, attribute access, subscripts or nested tags are permitted.
// Message extraction happens at compile time and never evaluates variables,
// filters or a translator.
type tagTransNode struct {
	position *Token

	singular  []transSegment
	plural    []transSegment
	hasPlural bool

	// countExpr is evaluated exactly once per render; nil without count.
	countExpr IEvaluator
	countName string

	placeholders []string
	declared     map[string]bool
	singularMsg  string
	pluralMsg    string
}

// TranslationMessages returns the stable messages of all {% trans %} blocks
// in the template, in source order, deduplicated by their singular/plural
// pair. Extraction does not execute variables, filters or a translator.
func (tpl *Template) TranslationMessages() []TranslationMessage {
	seen := map[string]bool{}
	out := make([]TranslationMessage, 0, len(tpl.transNodes))

	for _, node := range tpl.transNodes {
		key := node.singularMsg + "\x00" + node.pluralMsg
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, TranslationMessage{
			Singular:     node.singularMsg,
			Plural:       node.pluralMsg,
			HasPlural:    node.hasPlural,
			Placeholders: append([]string(nil), node.placeholders...),
			Line:         node.position.Line,
			Column:       node.position.Col,
		})
	}
	return out
}

func tagTransParser(doc *Parser, start *Token, arguments *Parser) (INodeTag, error) {
	node := &tagTransNode{
		position: start,
		declared: map[string]bool{},
	}

	if arguments.Remaining() > 0 {
		if arguments.Match(TokenIdentifier, "count") == nil {
			return nil, arguments.Error("Trans tag only accepts an optional 'count EXPR [as NAME]' clause.", nil)
		}
		expr, err := arguments.ParseExpression()
		if err != nil {
			return nil, err
		}
		node.countExpr = expr
		node.countName = transImplicitCountName
		if name, ok := simpleIdentName(expr); ok {
			node.countName = name
		}

		if arguments.Match(TokenKeyword, "as") != nil {
			name := arguments.MatchType(TokenIdentifier)
			if name == nil {
				return nil, arguments.Error("Expected a placeholder name after 'as'.", nil)
			}
			node.countName = name.Val
		}

		if arguments.Remaining() > 0 {
			return nil, arguments.Error("Malformed 'trans' tag arguments.", nil)
		}
	}

	// First body section (singular).
	if err := parseTransBody(doc, node, false); err != nil {
		return nil, err
	}

	if err := node.finish(start); err != nil {
		return nil, err
	}

	doc.template.transNodes = append(doc.template.transNodes, node)
	return node, nil
}

// parseTransBody consumes one section of the block. For the singular
// section it stops at "{% plural %}" or "{% endtrans %}"; for the plural
// section it stops at "{% endtrans %}". It consumes the terminating tag.
func parseTransBody(doc *Parser, node *tagTransNode, pluralSection bool) error {
	var segments []transSegment

	for doc.Remaining() > 0 {
		t := doc.Current()

		if t.Typ == TokenSymbol && t.Val == "{%" {
			nameToken := doc.PeekTypeN(1, TokenIdentifier)
			if nameToken != nil {
				if nameToken.Val == "endtrans" {
					if err := consumeBlockTag(doc, "endtrans"); err != nil {
						return err
					}
					if pluralSection {
						node.plural = segments
						return nil
					}
					node.singular = segments
					return nil
				}
				if !pluralSection && nameToken.Val == "plural" {
					if err := consumeBlockTag(doc, "plural"); err != nil {
						return err
					}
					node.singular = segments
					node.hasPlural = true
					if node.countExpr == nil {
						return doc.Error("A 'plural' section requires a 'count' expression on the 'trans' tag.",
							nameToken)
					}
					return parseTransBody(doc, node, true)
				}
			}
			return doc.Error("Only plain text and simple {{ placeholders }} are allowed inside 'trans' blocks.", t)
		}

		switch t.Typ {
		case TokenHTML:
			segments = append(segments, transSegment{literal: transHTMLText(doc)})
			doc.Consume()
		case TokenSymbol:
			if t.Val != "{{" {
				return doc.Error("Only plain text and simple {{ placeholders }} are allowed inside 'trans' blocks.", t)
			}
			name, expr, err := parseTransPlaceholder(doc)
			if err != nil {
				return err
			}
			segments = append(segments, transSegment{placeholder: name, expr: expr})
		default:
			return doc.Error("Only plain text and simple {{ placeholders }} are allowed inside 'trans' blocks.", t)
		}
	}

	return doc.Error("Unexpected EOF, expected 'endtrans'.", doc.lastToken)
}

// consumeBlockTag consumes "{% name [whitespace-trim] %}" assuming the "{%"
// and name tokens have already been identified.
func consumeBlockTag(doc *Parser, name string) error {
	doc.ConsumeN(2) // "{%", name
	for doc.Remaining() > 0 {
		if doc.Match(TokenSymbol, "%}") != nil || doc.Match(TokenSymbol, "-%}") != nil {
			return nil
		}
		doc.Consume()
	}
	return doc.Error(fmt.Sprintf("Malformed '%s' tag: expected '%%}'.", name), doc.Current())
}

// transHTMLText returns the text of the current TokenHTML node honouring
// adjacent whitespace-trim delimiters.
func transHTMLText(doc *Parser) string {
	t := doc.Current()
	res := t.Val
	left := doc.PeekTypeN(-1, TokenSymbol)
	right := doc.PeekTypeN(1, TokenSymbol)
	if left != nil && left.TrimWhitespaces {
		res = strings.TrimLeft(res, tokenSpaceChars)
	}
	if right != nil && right.TrimWhitespaces {
		res = strings.TrimRight(res, tokenSpaceChars)
	}
	return res
}

// parseTransPlaceholder parses "{{ IDENT }}" and rejects filters, calls,
// subscripts and attribute access. The caller has ensured "{{" is current.
func parseTransPlaceholder(doc *Parser) (string, IEvaluator, error) {
	startTok := doc.Current()
	doc.Consume() // "{{"

	nameTok := doc.MatchType(TokenIdentifier)
	if nameTok == nil {
		return "", nil, doc.Error("Translation placeholders must be a simple identifier.", doc.Current())
	}
	if doc.Match(TokenSymbol, "}}") == nil && doc.Match(TokenSymbol, "-}}") == nil {
		return "", nil, doc.Error("Translation placeholders may only be a simple '{{ name }}' without filters.",
			doc.Current())
	}

	resolver := &variableResolver{locationToken: startTok}
	resolver.parts = append(resolver.parts, &variablePart{typ: varTypeIdent, s: nameTok.Val})
	return nameTok.Val, resolver, nil
}

// finish canonicalizes messages and validates the block.
func (node *tagTransNode) finish(start *Token) error {
	node.singularMsg = canonicalizeTransText(serializeTransSegments(node.singular))
	if node.hasPlural {
		node.pluralMsg = canonicalizeTransText(serializeTransSegments(node.plural))
	}

	node.placeholders = nil
	node.declared = map[string]bool{}
	if node.countExpr != nil {
		node.declared[node.countName] = true
		node.placeholders = append(node.placeholders, node.countName)
	}
	addPlaceholders := func(segments []transSegment) {
		for _, seg := range segments {
			if seg.placeholder != "" && !node.declared[seg.placeholder] {
				node.declared[seg.placeholder] = true
				node.placeholders = append(node.placeholders, seg.placeholder)
			}
		}
	}
	addPlaceholders(node.singular)
	addPlaceholders(node.plural)

	if node.singularMsg == "" || (node.hasPlural && node.pluralMsg == "") {
		return transParseError(start, "Trans blocks must contain a non-empty message.")
	}

	return nil
}

func serializeTransSegments(segments []transSegment) string {
	var b strings.Builder
	for _, seg := range segments {
		if seg.placeholder != "" {
			b.WriteString("%(" + seg.placeholder + ")s")
		} else {
			b.WriteString(seg.literal)
		}
	}
	return b.String()
}

func transParseError(tok *Token, msg string) error {
	return &Error{
		Filename:  tok.Filename,
		Sender:    "parser",
		Line:      tok.Line,
		Column:    tok.Col,
		Token:     tok,
		OrigError: errors.New(msg),
	}
}

// simpleIdentName returns the identifier name when expr is a bare
// single-part variable without filters (e.g. the "n" in "count n").
func simpleIdentName(expr IEvaluator) (string, bool) {
	nf, ok := expr.(*nodeFilteredVariable)
	if !ok || len(nf.filterChain) > 0 {
		return "", false
	}
	vr, ok := nf.resolver.(*variableResolver)
	if !ok || len(vr.parts) != 1 || vr.parts[0].typ != varTypeIdent {
		return "", false
	}
	return vr.parts[0].s, true
}

func init() {
	mustRegisterTag("trans", tagTransParser)
}
