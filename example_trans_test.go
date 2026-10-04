package pongo2_test

import (
	"fmt"

	"github.com/flosch/pongo2/v7"
)

// exampleCatalog is a tiny message catalog: locale -> singular source ->
// translated forms (index 0 singular, index 1 plural when applicable).
var exampleCatalog = map[string]map[string][]string{
	"en": {
		"{name} has {count} message.": {
			"{name} has {count} message.",
			"{name} has {count} messages.",
		},
	},
	"zh": {
		"{name} has {count} message.": {
			"{name} 有 {count} 条消息。",
		},
	},
}

func makeTranslator(locale string) pongo2.Translator {
	return func(req pongo2.TranslationRequest) (pongo2.TranslationResult, error) {
		forms, ok := exampleCatalog[locale][req.Message.Singular]
		if !ok {
			return pongo2.TranslationResult{}, pongo2.ErrNoTranslation
		}
		form := 0
		if len(forms) == 2 && req.HasCount && req.Count != 1 {
			form = 1
		}
		return pongo2.TranslationResult{Forms: forms, Form: form}, nil
	}
}

// Example_transBlock first compiles the template, extracts the stable
// singular/plural messages and their placeholders, then renders it for two
// locales with caller-selected fallback strategies.
func Example_transBlock() {
	const source = `{% trans count = unread %}{name} has {count} message.{% plural %}{name} has {count} messages.{% endtrans %}`

	tpl, err := pongo2.FromString(source)
	if err != nil {
		panic(err)
	}

	// Compile-time extraction: no variable, filter or translator is executed.
	for _, msg := range tpl.TranslationMessages() {
		fmt.Printf("extracted: singular=%q plural=%q placeholders=%v\n",
			msg.Singular, msg.Plural, msg.Placeholders)
	}

	render := func(locale string, unread int) string {
		rt := &pongo2.I18nRuntime{
			Locale:          locale,
			Translator:      makeTranslator(locale),
			MissingStrategy: pongo2.FailureStrategySource,
			InvalidStrategy: pongo2.FailureStrategySource,
		}
		ctx := pongo2.WithI18n(pongo2.Context{"name": "Ada", "unread": unread}, rt)
		out, err := tpl.Execute(ctx)
		if err != nil {
			panic(err)
		}
		return out
	}

	fmt.Println(render("en", 1))
	fmt.Println(render("en", 5))
	fmt.Println(render("zh", 5))

	// Output:
	// extracted: singular="{name} has {count} message." plural="{name} has {count} messages." placeholders=[name count]
	// Ada has 1 message.
	// Ada has 5 messages.
	// Ada 有 5 条消息。
}
