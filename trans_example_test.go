package pongo2_test

import (
	"fmt"

	"github.com/flosch/pongo2/v7"
)

// ExampleTranslationMessages compiles a template, extracts the stable
// singular/plural messages without executing anything, and renders it through
// a per-request fake translator.
func ExampleTemplate_TranslationMessages() {
	source := "{% trans count items %}" +
		"You have {{ items }} new message" +
		"{% plural %}" +
		"You have {{ items }} new messages" +
		"{% endtrans %}"

	tpl, err := pongo2.FromString(source)
	if err != nil {
		panic(err)
	}

	// 1. Extract messages for the translation catalogue (no execution).
	for _, msg := range tpl.TranslationMessages() {
		fmt.Printf("extract: singular=%q plural=%q placeholders=%v\n",
			msg.Singular, msg.Plural, msg.Placeholders)
	}

	// 2. Render with the translator chosen for this request.
	translator := pongo2.TranslatorFunc(func(req pongo2.TranslateRequest) ([]string, int, error) {
		forms := []string{
			"Vous avez %(items)s nouveau message",
			"Vous avez %(items)s nouveaux messages",
		}
		idx := 0
		if *req.Count != 1 {
			idx = 1
		}
		return forms, idx, nil
	})

	for _, n := range []int{1, 5} {
		out, err := tpl.ExecuteTranslated(
			pongo2.Context{"items": n},
			pongo2.TranslateOptions{Translator: translator, Language: "fr"},
		)
		if err != nil {
			panic(err)
		}
		fmt.Println(out)
	}

	// Output:
	// extract: singular="You have %(items)s new message" plural="You have %(items)s new messages" placeholders=[items]
	// Vous avez 1 nouveau message
	// Vous avez 5 nouveaux messages
}
