package pongo2_test

import (
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/flosch/pongo2/v7"
)

func TestTransMessageExtraction(t *testing.T) {
	source := "{% trans %}Hello, {{ who }}!{% endtrans %}\n" +
		"{% trans count items %}{{ items }} apple{% plural %}{{ items }} apples{% endtrans %}\n" +
		"{% trans %}Hello, {{ who }}!{% endtrans %}"

	tpl, err := pongo2.FromString(source)
	if err != nil {
		t.Fatal(err)
	}
	msgs := tpl.TranslationMessages()

	if len(msgs) != 2 {
		t.Fatalf("expected 2 deduplicated messages, got %d: %+v", len(msgs), msgs)
	}

	if msgs[0].Singular != "Hello, %(who)s!" || msgs[0].HasPlural {
		t.Fatalf("unexpected message 0: %+v", msgs[0])
	}
	if len(msgs[0].Placeholders) != 1 || msgs[0].Placeholders[0] != "who" {
		t.Fatalf("unexpected placeholders 0: %v", msgs[0].Placeholders)
	}

	if !msgs[1].HasPlural ||
		msgs[1].Singular != "%(items)s apple" ||
		msgs[1].Plural != "%(items)s apples" {
		t.Fatalf("unexpected message 1: %+v", msgs[1])
	}
}

func TestTransExtractionDoesNotExecute(t *testing.T) {
	// Filters inside a trans block must be rejected at compile time, never
	// evaluated during extraction.
	if _, err := pongo2.FromString("{% trans %}Hi {{ name|upper }}{% endtrans %}"); err == nil {
		t.Fatal("filters inside trans blocks must be a compile error")
	} else if !strings.Contains(err.Error(), "placeholders may only be") {
		t.Fatalf("unexpected compile error: %v", err)
	}
}

func TestTransNestedScopeIsolation(t *testing.T) {
	// {% with %} shadows "name" inside; the trans block sees the inner value,
	// and after the block the outer value is restored.
	source := `{{ name }}|` +
		`{% with name="inner" %}{% trans %}Hi {{ name }}{% endtrans %}{% endwith %}` +
		`|{{ name }}`

	out := renderTrans(t, source, pongo2.Context{"name": "outer"},
		pongo2.TranslateOptions{
			Translator: pongo2.TranslatorFunc(func(pongo2.TranslateRequest) ([]string, int, error) {
				return []string{"[%(name)s]"}, 0, nil
			}),
		})
	if out != "outer|[inner]|outer" {
		t.Fatalf("nested scoping wrong: %q", out)
	}
}

func TestTransPerRequestLanguageIsolation(t *testing.T) {
	tpl := pongo2.Must(pongo2.FromString("{% trans %}Hello, {{ name }}!{% endtrans %}"))

	var en, de string
	done := make(chan error, 2)
	go func() {
		out, err := tpl.ExecuteTranslated(pongo2.Context{"name": "A"},
			pongo2.TranslateOptions{Translator: newFakeTranslator(), Language: "en"})
		en = out
		done <- err
	}()
	go func() {
		out, err := tpl.ExecuteTranslated(pongo2.Context{"name": "B"},
			pongo2.TranslateOptions{Translator: newFakeTranslator(), Language: "de"})
		de = out
		done <- err
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if en != "A, hello! A!" || de != "Hallo, B!" {
		t.Fatalf("language/data leaked between requests: en=%q de=%q", en, de)
	}
}

func TestTransCompileErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		msg    string
	}{
		{"nested tag", "{% trans %}{% if x %}a{% endif %}{% endtrans %}", "plain text and simple"},
		{"filter", "{% trans %}{{ x|upper }}{% endtrans %}", "placeholders may only be"},
		{"attribute", "{% trans %}{{ x.y }}{% endtrans %}", "placeholders may only be"},
		{"plural without count", "{% trans %}a{% plural %}b{% endtrans %}", "requires a 'count'"},
		{"missing endtag", "{% trans %}a", "expected 'endtrans'"},
		{"empty block", "{% trans %}   {% endtrans %}", "non-empty"},
		{"empty plural", "{% trans count n %}a{% plural %}   {% endtrans %}", "non-empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pongo2.FromString(tc.source)
			if err == nil {
				t.Fatalf("expected compile error containing %q", tc.msg)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.msg)
			}
		})
	}
}

func TestTransCountRequiresInteger(t *testing.T) {
	tpl := pongo2.Must(pongo2.FromString(
		"{% trans count n %}{{ n }} a{% plural %}{{ n }} b{% endtrans %}"))
	_, err := tpl.ExecuteTranslated(pongo2.Context{"n": "many"},
		pongo2.TranslateOptions{Translator: newFakeTranslator(), Language: "de"})
	if err == nil || !strings.Contains(err.Error(), "must evaluate to an integer") {
		t.Fatalf("expected integer error, got %v", err)
	}
}

func TestTransCountOnlyPlaceholder(t *testing.T) {
	// "count total" binds the count to placeholder %(total)s even though the
	// source body never prints it; the translation may still use it.
	source := "{% trans count total %}one item{% plural %}many items{% endtrans %}"
	tpl := pongo2.Must(pongo2.FromString(source))

	msgs := tpl.TranslationMessages()
	if len(msgs) != 1 || len(msgs[0].Placeholders) != 1 ||
		msgs[0].Placeholders[0] != "total" {
		t.Fatalf("unexpected extraction: %+v", msgs)
	}

	out, err := tpl.ExecuteTranslated(pongo2.Context{"total": 3},
		pongo2.TranslateOptions{
			Translator: pongo2.TranslatorFunc(func(req pongo2.TranslateRequest) ([]string, int, error) {
				idx := 0
				if *req.Count != 1 {
					idx = 1
				}
				return []string{"%(total)s item", "%(total)s items"}, idx, nil
			}),
		})
	if err != nil {
		t.Fatal(err)
	}
	if out != "3 items" {
		t.Fatalf("count-only placeholder wrong: %q", out)
	}
}

func TestTransWhitespaceCanonicalization(t *testing.T) {
	source := "{% trans %}  Hello,\n   {{ name }}!  \t{% endtrans %}"
	tpl := pongo2.Must(pongo2.FromString(source))
	msg := tpl.TranslationMessages()[0]
	if msg.Singular != "Hello, %(name)s!" {
		t.Fatalf("canonicalization wrong: %q", msg.Singular)
	}

	out, err := tpl.ExecuteTranslated(pongo2.Context{"name": "Ada"},
		pongo2.TranslateOptions{
			Translator: pongo2.TranslatorFunc(func(req pongo2.TranslateRequest) ([]string, int, error) {
				return []string{req.Singular}, 0, nil
			}),
		})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Hello, Ada!" {
		t.Fatalf("canonical rendering wrong: %q", out)
	}
}

func TestTransInsideLoop(t *testing.T) {
	source := "{% for n in nums %}" +
		"{% trans count n %}{{ n }} item{% plural %}{{ n }} items{% endtrans %};" +
		"{% endfor %}"
	tpl := pongo2.Must(pongo2.FromString(source))

	out, err := tpl.ExecuteTranslated(pongo2.Context{
		"nums": []int{1, 2, 3},
	}, pongo2.TranslateOptions{
		Translator: pongo2.TranslatorFunc(func(req pongo2.TranslateRequest) ([]string, int, error) {
			idx := 0
			if *req.Count != 1 {
				idx = 1
			}
			return []string{"%(n)s x", "%(n)s xs"}, idx, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "1 x;2 xs;3 xs;" {
		t.Fatalf("loop rendering wrong: %q", out)
	}
}

type includeLoader struct{}

func (includeLoader) Abs(_, name string) string { return name }
func (includeLoader) Get(path string) (io.Reader, error) {
	return strings.NewReader(map[string]string{
		"main.tpl":    `Main: {% include "partial.tpl" %}`,
		"partial.tpl": `{% trans %}Hello, {{ name }}!{% endtrans %}`,
	}[path]), nil
}

func TestTransPropagatesThroughInclude(t *testing.T) {
	set := pongo2.NewSet("inc", includeLoader{})
	tpl, err := set.FromFile("main.tpl")
	if err != nil {
		t.Fatal(err)
	}
	out, err := tpl.ExecuteTranslated(pongo2.Context{"name": "Ada"},
		pongo2.TranslateOptions{Translator: newFakeTranslator(), Language: "de"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Main: Hallo, Ada!" {
		t.Fatalf("include translation wrong: %q", out)
	}
}

func TestTransLiteralPercentAndBraces(t *testing.T) {
	source := "{% trans %}Progress {{ pct }}% done (100%(x)){% endtrans %}"

	// Translation keeps a stray '%' and a malformed '%(x)' as literal text.
	out := renderTrans(t, source, pongo2.Context{"pct": 50},
		pongo2.TranslateOptions{
			Translator: pongo2.TranslatorFunc(func(pongo2.TranslateRequest) ([]string, int, error) {
				return []string{"%(pct)s%% fertig (100%(x))"}, 0, nil
			}),
		})
	if out != "50%% fertig (100%(x))" {
		t.Fatalf("literal percent handling wrong: %q", out)
	}
}

func TestTransRepeatedPlaceholderEscapedOnce(t *testing.T) {
	source := "{% trans %}{{ name }} and {{ name }}{% endtrans %}"
	out := renderTrans(t, source, pongo2.Context{"name": "<i>"},
		pongo2.TranslateOptions{
			Translator: pongo2.TranslatorFunc(func(pongo2.TranslateRequest) ([]string, int, error) {
				return []string{"%(name)s + %(name)s"}, 0, nil
			}),
		})
	if out != "&lt;i&gt; + &lt;i&gt;" {
		t.Fatalf("repeated placeholder escaping wrong: %q", out)
	}
}

// fakeTranslator is an in-memory catalogue keyed by the singular source
// message. It demonstrates reordering/repeating placeholders and per-language
// plural selection without interpreting translation strings as templates.
type fakeTranslator struct {
	catalog map[string]map[string][]string
}

func (t *fakeTranslator) Translate(req pongo2.TranslateRequest) ([]string, int, error) {
	forms, ok := t.catalog[req.Singular][req.Language]
	if !ok {
		return nil, 0, pongo2.ErrNoTranslation
	}
	if !req.HasPlural {
		return forms, 0, nil
	}
	if req.Count == nil {
		return nil, 0, errors.New("missing count")
	}
	idx := 0
	if *req.Count != 1 {
		idx = 1
	}
	return forms, idx, nil
}

func newFakeTranslator() *fakeTranslator {
	return &fakeTranslator{
		catalog: map[string]map[string][]string{
			// Greeting: German keeps the order, English reverses "name" and the
			// greeting word to exercise placeholder reordering/repetition.
			"Hello, %(name)s!": {
				"de": {"Hallo, %(name)s!"},
				"en": {"%(name)s, hello! %(name)s!"},
			},
			"You have %(n)s new message": {
				"de": {
					"Sie haben %(n)s neue Nachricht",
					"Sie haben %(n)s neue Nachrichten",
				},
				"en": {
					"You have %(n)s new message",
					"You have %(n)s new messages",
				},
			},
		},
	}
}

func renderTrans(t *testing.T, source string, ctx pongo2.Context,
	opts pongo2.TranslateOptions) string {
	t.Helper()
	tpl, err := pongo2.FromString(source)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	out, err := tpl.ExecuteTranslated(ctx, opts)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	return out
}

func TestTransWordOrderAcrossLanguages(t *testing.T) {
	tpl := pongo2.Must(pongo2.FromString("{% trans %}Hello, {{ name }}!{% endtrans %}"))

	out, err := tpl.ExecuteTranslated(pongo2.Context{"name": "Ada"},
		pongo2.TranslateOptions{Translator: newFakeTranslator(), Language: "de"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Hallo, Ada!" {
		t.Fatalf("German word order wrong: %q", out)
	}

	out, err = tpl.ExecuteTranslated(pongo2.Context{"name": "Ada"},
		pongo2.TranslateOptions{Translator: newFakeTranslator(), Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	// Placeholder reordered and repeated by the translation.
	if out != "Ada, hello! Ada!" {
		t.Fatalf("English word order/repetition wrong: %q", out)
	}
}

func TestTransPlural(t *testing.T) {
	source := "{% trans count n %}You have {{ n }} new message{% plural %}" +
		"You have {{ n }} new messages{% endtrans %}"

	for _, tc := range []struct {
		n    int
		want string
	}{
		{0, "Sie haben 0 neue Nachrichten"},
		{1, "Sie haben 1 neue Nachricht"},
		{2, "Sie haben 2 neue Nachrichten"},
		{-3, "Sie haben -3 neue Nachrichten"},
	} {
		out := renderTrans(t, source, pongo2.Context{"n": tc.n},
			pongo2.TranslateOptions{Translator: newFakeTranslator(), Language: "de"})
		if out != tc.want {
			t.Fatalf("n=%d: got %q want %q", tc.n, out, tc.want)
		}
	}
}

func TestTransCountEvaluatedOnce(t *testing.T) {
	var calls int32
	// The count expression supplies both the plural decision and the
	// %(n)s placeholder; it must be evaluated a single time per render.
	source := "{% trans count next() as n %}{{ n }} item{% plural %}" +
		"{{ n }} items{% endtrans %}"

	tpl := pongo2.Must(pongo2.FromString(source))
	out, err := tpl.ExecuteTranslated(pongo2.Context{
		"next": func() int {
			atomic.AddInt32(&calls, 1)
			return 5
		},
	}, pongo2.TranslateOptions{
		Translator: pongo2.TranslatorFunc(func(req pongo2.TranslateRequest) ([]string, int, error) {
			idx := 0
			if *req.Count != 1 {
				idx = 1
			}
			return []string{"%(n)s thing", "%(n)s things"}, idx, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("count expression should evaluate once, got %d calls", calls)
	}
	if out != "5 things" {
		t.Fatalf("unexpected rendered output: %q", out)
	}
}

func TestTransMissingTranslationPolicies(t *testing.T) {
	source := "{% trans %}Hello, {{ name }}!{% endtrans %}"
	opts := pongo2.TranslateOptions{Translator: newFakeTranslator(), Language: "fr"}

	// Default: fall back to canonical source.
	out := renderTrans(t, source, pongo2.Context{"name": "Ada"}, opts)
	if out != "Hello, Ada!" {
		t.Fatalf("default source fallback wrong: %q", out)
	}

	// Empty policy.
	opts.MissingPolicy = pongo2.TranslateMissingEmpty
	out = renderTrans(t, source, pongo2.Context{"name": "Ada"}, opts)
	if out != "" {
		t.Fatalf("empty missing policy wrong: %q", out)
	}

	// Error policy.
	opts.MissingPolicy = pongo2.TranslateMissingError
	tpl := pongo2.Must(pongo2.FromString(source))
	_, err := tpl.ExecuteTranslated(pongo2.Context{"name": "Ada"}, opts)
	if err == nil {
		t.Fatal("expected error for TranslateMissingError")
	}
}

func TestTransNoTranslatorConfigured(t *testing.T) {
	tpl := pongo2.Must(pongo2.FromString("{% trans %}Hi {{ name }}{% endtrans %}"))

	// With plain Execute (no options) the block falls back to source.
	out, err := tpl.Execute(pongo2.Context{"name": "Bo"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Hi Bo" {
		t.Fatalf("plain Execute fallback wrong: %q", out)
	}

	// Strict policy surfaces a clear error even without a translator.
	_, err = tpl.ExecuteTranslated(pongo2.Context{"name": "Bo"}, pongo2.TranslateOptions{
		MissingPolicy: pongo2.TranslateMissingError,
	})
	if err == nil || !strings.Contains(err.Error(), "no translator configured") {
		t.Fatalf("expected no-translator error, got %v", err)
	}
}

func TestTransErrorPolicies(t *testing.T) {
	source := "{% trans %}Hello, {{ name }}!{% endtrans %}"
	ctx := pongo2.Context{"name": "Ada"}

	// Unknown placeholder -> error policy.
	badUnknown := pongo2.TranslatorFunc(func(pongo2.TranslateRequest) ([]string, int, error) {
		return []string{"Hola %(hacker)s!"}, 0, nil
	})
	out := renderTrans(t, source, ctx, pongo2.TranslateOptions{Translator: badUnknown})
	if out != "Hello, Ada!" {
		t.Fatalf("unknown placeholder should fall back to source by default: %q", out)
	}

	_, err := pongo2.Must(pongo2.FromString(source)).ExecuteTranslated(ctx,
		pongo2.TranslateOptions{Translator: badUnknown, ErrorPolicy: pongo2.TranslateErrorFail})
	if err == nil || !strings.Contains(err.Error(), "unknown placeholder") {
		t.Fatalf("expected unknown placeholder error, got %v", err)
	}

	// Missing required placeholder.
	missing := pongo2.TranslatorFunc(func(pongo2.TranslateRequest) ([]string, int, error) {
		return []string{"Hola!"}, 0, nil
	})
	_, err = pongo2.Must(pongo2.FromString(source)).ExecuteTranslated(ctx,
		pongo2.TranslateOptions{Translator: missing, ErrorPolicy: pongo2.TranslateErrorFail})
	if err == nil || !strings.Contains(err.Error(), "missing required placeholder") {
		t.Fatalf("expected missing placeholder error, got %v", err)
	}

	// Illegal plural index.
	pluralSource := "{% trans count n %}{{ n }} a{% plural %}{{ n }} b{% endtrans %}"
	illegal := pongo2.TranslatorFunc(func(pongo2.TranslateRequest) ([]string, int, error) {
		return []string{"%(n)s a", "%(n)s b"}, 7, nil
	})
	_, err = pongo2.Must(pongo2.FromString(pluralSource)).ExecuteTranslated(pongo2.Context{"n": 1},
		pongo2.TranslateOptions{Translator: illegal, ErrorPolicy: pongo2.TranslateErrorFail})
	if err == nil || !strings.Contains(err.Error(), "illegal plural index") {
		t.Fatalf("expected illegal plural index error, got %v", err)
	}

	// Wrong number of forms.
	wrongForms := pongo2.TranslatorFunc(func(pongo2.TranslateRequest) ([]string, int, error) {
		return []string{}, 0, nil
	})
	_, err = pongo2.Must(pongo2.FromString(source)).ExecuteTranslated(ctx,
		pongo2.TranslateOptions{Translator: wrongForms, ErrorPolicy: pongo2.TranslateErrorFail})
	if err == nil || !strings.Contains(err.Error(), "expected 1") {
		t.Fatalf("expected forms-count error, got %v", err)
	}

	// Hard translator error.
	hardErr := pongo2.TranslatorFunc(func(pongo2.TranslateRequest) ([]string, int, error) {
		return nil, 0, errors.New("backend down")
	})
	out = renderTrans(t, source, ctx, pongo2.TranslateOptions{Translator: hardErr})
	if out != "Hello, Ada!" {
		t.Fatalf("hard error should fall back to source by default: %q", out)
	}
}

func TestTransHTMLEscaping(t *testing.T) {
	source := "<p>{% trans %}Hi {{ name }}{% endtrans %}</p>"

	// Autoescape on: placeholder escaped once; translated literal text and
	// surrounding markup are emitted verbatim (no double escaping).
	out := renderTrans(t, source, pongo2.Context{"name": "<b>Ada</b>"},
		pongo2.TranslateOptions{
			Translator: pongo2.TranslatorFunc(func(pongo2.TranslateRequest) ([]string, int, error) {
				return []string{"<b>Hallo</b> %(name)s &amp; co"}, 0, nil
			}),
		})
	want := "<p><b>Hallo</b> &lt;b&gt;Ada&lt;/b&gt; &amp; co</p>"
	if out != want {
		t.Fatalf("escaping wrong:\n got %q\nwant %q", out, want)
	}

	// Autoescape off: value passes through untouched.
	set := pongo2.NewSet("noescape", pongo2.MustNewLocalFileSystemLoader(""))
	set.SetAutoescape(false)
	tpl, err := set.FromString("{% trans %}Hi {{ name }}{% endtrans %}")
	if err != nil {
		t.Fatal(err)
	}
	out, err = tpl.ExecuteTranslated(pongo2.Context{"name": "<b>Ada</b>"},
		pongo2.TranslateOptions{
			Translator: pongo2.TranslatorFunc(func(pongo2.TranslateRequest) ([]string, int, error) {
				return []string{"Hallo %(name)s"}, 0, nil
			}),
		})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Hallo <b>Ada</b>" {
		t.Fatalf("autoescape-off output wrong: %q", out)
	}
}
