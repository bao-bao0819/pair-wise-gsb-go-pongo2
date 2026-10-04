package pongo2

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeTransEntry is one cataloged message keyed by singular source text.
type fakeTransEntry struct {
	forms []string
}

type fakeTranslator struct {
	mu        sync.Mutex
	catalogs  map[string]map[string]fakeTransEntry
	badLocale string // locale that returns malformed results
}

func newFakeTranslator() *fakeTranslator {
	return &fakeTranslator{catalogs: map[string]map[string]fakeTransEntry{}}
}

func (f *fakeTranslator) put(locale, source string, forms ...string) {
	catalog, ok := f.catalogs[locale]
	if !ok {
		catalog = map[string]fakeTransEntry{}
		f.catalogs[locale] = catalog
	}
	catalog[source] = fakeTransEntry{forms: forms}
}

func (f *fakeTranslator) Translate(req TranslationRequest) (TranslationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	catalog, ok := f.catalogs[req.Locale]
	if !ok {
		return TranslationResult{}, ErrNoTranslation
	}
	entry, ok := catalog[req.Message.Singular]
	if !ok {
		return TranslationResult{}, ErrNoTranslation
	}

	form := 0
	if len(entry.forms) == 2 && req.HasCount && req.Count != 1 {
		form = 1
	}
	if req.Locale == f.badLocale {
		switch entry.forms[0] {
		case "ILLEGAL-INDEX":
			return TranslationResult{Forms: []string{"fine"}, Form: 5}, nil
		case "TRANSLATOR-BOOM":
			return TranslationResult{}, errors.New("translator exploded")
		}
	}
	return TranslationResult{Forms: entry.forms, Form: form}, nil
}

func newTransSet(t *testing.T) *TemplateSet {
	t.Helper()
	return NewSet("trans-test", &DummyLoader{})
}

func renderTrans(t *testing.T, set *TemplateSet, source, locale string, rt *I18nRuntime, ctx Context) string {
	t.Helper()
	tpl, err := set.FromString(source)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	out, err := tpl.Execute(WithI18n(ctx, rt))
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	return out
}

func TestTransExtraction(t *testing.T) {
	set := newTransSet(t)
	source := `{% trans count = items() %}{name} has {count} apple.{% plural %}{name} has {count} apples.{% endtrans %}`

	tpl, err := set.FromString(source)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	msgs := tpl.TranslationMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 extracted message, got %d", len(msgs))
	}
	msg := msgs[0]
	if msg.Singular != "{name} has {count} apple." {
		t.Errorf("unexpected singular: %q", msg.Singular)
	}
	if msg.Plural != "{name} has {count} apples." {
		t.Errorf("unexpected plural: %q", msg.Plural)
	}
	if got := strings.Join(msg.Placeholders, ","); got != "name,count" {
		t.Errorf("unexpected placeholders: %q", got)
	}
	if !msg.HasPlural() {
		t.Error("expected HasPlural to be true")
	}

	// Returned messages are copies; mutating them does not affect the template.
	msgs[0].Placeholders[0] = "mutated"
	if tpl.TranslationMessages()[0].Placeholders[0] != "name" {
		t.Error("extracted placeholder slices must be defensive copies")
	}
}

func TestTransWordOrderAndRepeat(t *testing.T) {
	set := newTransSet(t)
	source := `{% trans %}Hello {name}, hello {name}!{% endtrans %}`
	tr := newFakeTranslator()
	tr.put("en", "Hello {name}, hello {name}!", "Hello {name}, hello {name}!")
	// Chinese reorders and repeats the declared placeholder.
	tr.put("zh", "Hello {name}, hello {name}!", "{name}，你好！再叫一次{name}。")

	en := renderTrans(t, set, source, "en",
		&I18nRuntime{Locale: "en", Translator: tr.Translate},
		Context{"name": "<b>Fred</b>"})
	if en != "Hello &lt;b&gt;Fred&lt;/b&gt;, hello &lt;b&gt;Fred&lt;/b&gt;!" {
		t.Errorf("en word order/escape mismatch: %q", en)
	}

	zh := renderTrans(t, set, source, "zh",
		&I18nRuntime{Locale: "zh", Translator: tr.Translate},
		Context{"name": "弗雷德"})
	if zh != "弗雷德，你好！再叫一次弗雷德。" {
		t.Errorf("zh word order/repeat mismatch: %q", zh)
	}
}

func TestTransPlural(t *testing.T) {
	set := newTransSet(t)
	source := `{% trans count = n %}{count} apple for {name}.{% plural %}{count} apples for {name}.{% endtrans %}`
	tr := newFakeTranslator()
	tr.put("en",
		"{count} apple for {name}.",
		"{count} apple for {name}.",
		"{count} apples for {name}.")
	// Chinese has no plural distinction: a single form is legal for any count.
	tr.put("zh",
		"{count} apple for {name}.",
		"{name} 有 {count} 个苹果。")

	cases := []struct {
		locale string
		n      int
		want   string
	}{
		{"en", 1, "1 apple for Fred."},
		{"en", 5, "5 apples for Fred."},
		{"zh", 1, "Fred 有 1 个苹果。"},
		{"zh", 5, "Fred 有 5 个苹果。"},
	}
	for _, tc := range cases {
		got := renderTrans(t, set, source, tc.locale,
			&I18nRuntime{Locale: tc.locale, Translator: tr.Translate},
			Context{"n": tc.n, "name": "Fred"})
		if got != tc.want {
			t.Errorf("%s n=%d: got %q want %q", tc.locale, tc.n, got, tc.want)
		}
	}
}

func TestTransStrategies(t *testing.T) {
	set := newTransSet(t)
	source := `{% trans %}Hi {name}.{% endtrans %}`

	unknown := newFakeTranslator()
	unknown.put("xx", "Hi {name}.", "Hola {attacker}.") // unknown placeholder

	missing := newFakeTranslator()
	missing.put("xx", "Hi {name}.", "Hola.") // drops required {name}

	illegal := newFakeTranslator()
	illegal.badLocale = "xx"
	illegal.put("xx", "Hi {name}.", "ILLEGAL-INDEX", "other")

	boom := newFakeTranslator()
	boom.badLocale = "xx"
	boom.put("xx", "Hi {name}.", "TRANSLATOR-BOOM", "other")

	absent := newFakeTranslator() // no catalog entry -> ErrNoTranslation

	// present-but-unbound has a valid translation, so rendering reaches value
	// resolution and fails because {name} is not provided by the context.
	presentButUnbound := newFakeTranslator()
	presentButUnbound.put("xx", "Hi {name}.", "Hola {name}.")

	cases := []struct {
		name    string
		tr      *fakeTranslator
		missing FailureStrategy
		invalid FailureStrategy
		ctx     Context
		want    string
		wantErr string
	}{
		{
			name:    "unknown placeholder defaults to error",
			tr:      unknown,
			wantErr: "unknown placeholder",
			ctx:     Context{"name": "Fred"},
		},
		{
			name:    "unknown placeholder empty fallback",
			tr:      unknown,
			invalid: FailureStrategyEmpty,
			want:    "",
			ctx:     Context{"name": "Fred"},
		},
		{
			name:    "unknown placeholder source fallback",
			tr:      unknown,
			invalid: FailureStrategySource,
			want:    "Hi Fred.",
			ctx:     Context{"name": "Fred"},
		},
		{
			name:    "missing required placeholder source fallback",
			tr:      missing,
			invalid: FailureStrategySource,
			want:    "Hi Fred.",
			ctx:     Context{"name": "Fred"},
		},
		{
			name:    "illegal plural index error",
			tr:      illegal,
			wantErr: "illegal plural form",
			ctx:     Context{"name": "Fred"},
		},
		{
			name:    "illegal plural index empty fallback",
			tr:      illegal,
			invalid: FailureStrategyEmpty,
			want:    "",
			ctx:     Context{"name": "Fred"},
		},
		{
			name:    "translator error source fallback",
			tr:      boom,
			invalid: FailureStrategySource,
			want:    "Hi Fred.",
			ctx:     Context{"name": "Fred"},
		},
		{
			name:    "no translation error",
			tr:      absent,
			wantErr: "no translation available",
			ctx:     Context{"name": "Fred"},
		},
		{
			name:    "no translation empty fallback",
			tr:      absent,
			missing: FailureStrategyEmpty,
			want:    "",
			ctx:     Context{"name": "Fred"},
		},
		{
			name:    "no translation source fallback",
			tr:      absent,
			missing: FailureStrategySource,
			want:    "Hi Fred.",
			ctx:     Context{"name": "Fred"},
		},
		{
			name:    "missing placeholder value keeps literal on source fallback",
			tr:      absent,
			missing: FailureStrategySource,
			want:    "Hi {name}.",
			ctx:     Context{},
		},
		{
			name:    "missing placeholder value error under error strategy",
			tr:      presentButUnbound,
			invalid: FailureStrategyError,
			wantErr: "missing value for placeholder {name}",
			ctx:     Context{},
		},
		{
			name:    "no translator configured error",
			tr:      nil,
			wantErr: "no translator configured",
			ctx:     Context{"name": "Fred"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpl, err := set.FromString(source)
			if err != nil {
				t.Fatalf("compile error: %v", err)
			}
			var rt *I18nRuntime
			if tc.tr != nil {
				rt = &I18nRuntime{
					Locale:          "xx",
					Translator:      tc.tr.Translate,
					MissingStrategy: tc.missing,
					InvalidStrategy: tc.invalid,
				}
			}
			out, err := tpl.Execute(WithI18n(tc.ctx, rt))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v (out=%q)", tc.wantErr, err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out != tc.want {
				t.Errorf("got %q want %q", out, tc.want)
			}
		})
	}
}

func TestTransHTMLEscaping(t *testing.T) {
	set := newTransSet(t)
	tr := newFakeTranslator()
	// The translation itself contains trusted markup and a template payload that
	// must survive as literal text instead of being parsed again.
	tr.put("en", "Hi {name}.", `<b>Hi {name}</b>; literal: {{ 7*7 }}`)

	source := `{% trans %}Hi {name}.{% endtrans %}`

	t.Run("string placeholder escaped once, translation markup kept", func(t *testing.T) {
		got := renderTrans(t, set, source, "en",
			&I18nRuntime{Locale: "en", Translator: tr.Translate},
			Context{"name": `<img src=x onerror=alert(1)>`})
		want := `<b>Hi &lt;img src=x onerror=alert(1)&gt;</b>; literal: {{ 7*7 }}`
		if got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	})

	t.Run("safe placeholder not escaped", func(t *testing.T) {
		got := renderTrans(t, set, source, "en",
			&I18nRuntime{Locale: "en", Translator: tr.Translate},
			Context{"name": AsSafeValue(`<i>safe</i>`)})
		want := `<b>Hi <i>safe</i></b>; literal: {{ 7*7 }}`
		if got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	})

	t.Run("autoescape off keeps raw value", func(t *testing.T) {
		local := NewSet("trans-html-off", &DummyLoader{})
		local.SetAutoescape(false)
		tpl, err := local.FromString(source)
		if err != nil {
			t.Fatalf("compile error: %v", err)
		}
		rt := &I18nRuntime{Locale: "en", Translator: tr.Translate}
		got, err := tpl.Execute(WithI18n(Context{"name": `<u>raw</u>`}, rt))
		if err != nil {
			t.Fatalf("execute error: %v", err)
		}
		want := `<b>Hi <u>raw</u></b>; literal: {{ 7*7 }}`
		if got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	})
}

func TestTransCountEvaluatedOnce(t *testing.T) {
	set := newTransSet(t)
	tr := newFakeTranslator()
	tr.put("zh", "{count} apple for {name}.", "{name} 有 {count} 个苹果。")

	calls := 0
	items := func() int {
		calls++
		return 3
	}
	source := `{% trans count = items() %}{count} apple for {name}.{% plural %}{name} has {count} apples.{% endtrans %}`
	tpl, err := set.FromString(source)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	if calls != 0 {
		t.Fatalf("count expression must not run at compile time, ran %d times", calls)
	}
	rt := &I18nRuntime{Locale: "zh", Translator: tr.Translate}
	out, err := tpl.Execute(WithI18n(Context{"items": items, "name": "Fred"}, rt))
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if calls != 1 {
		t.Errorf("count expression must evaluate once, ran %d times", calls)
	}
	if out != "Fred 有 3 个苹果。" {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestTransNestedScopeAndIsolation(t *testing.T) {
	set := newTransSet(t)
	tr := newFakeTranslator()
	tr.put("en", "You have {count} item.", "You have {count} item.", "You have {count} items.")

	source := `{% with n = inner_count %}{% trans count = n %}You have {count} item.{% plural %}You have {count} items.{% endtrans %}{% endwith %}`
	tpl, err := set.FromString(source)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan string, 2)
	render := func(inner int, want string) {
		defer wg.Done()
		rt := &I18nRuntime{Locale: "en", Translator: tr.Translate}
		got, err := tpl.Execute(WithI18n(Context{"inner_count": inner}, rt))
		if err != nil {
			errs <- err.Error()
			return
		}
		if got != want {
			errs <- "got " + got + " want " + want
		}
	}
	wg.Add(2)
	go render(1, "You have 1 item.")
	go render(42, "You have 42 items.")
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}

func TestTransCompileErrors(t *testing.T) {
	set := newTransSet(t)
	cases := []struct {
		name    string
		source  string
		wantErr string
	}{
		{"expression inside", `{% trans %}Hello {{ name }}!{% endtrans %}`, "not allowed inside"},
		{"nested tag", `{% trans %}{% if x %}a{% endif %}{% endtrans %}`, "only 'plural'"},
		{"plural without count", `{% trans %}one{% plural %}many{% endtrans %}`, "declares a count"},
		{"count without plural", `{% trans count = n %}one{% endtrans %}`, "requires a 'plural'"},
		{"unknown argument", `{% trans foo %}one{% endtrans %}`, "count = <expression>"},
		{"unclosed", `{% trans %}one`, "expected 'endtrans'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := set.FromString(tc.source)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErr)) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestTransNonIntegerCount(t *testing.T) {
	set := newTransSet(t)
	tpl, err := set.FromString(`{% trans count = n %}{count} item{% plural %}{count} items{% endtrans %}`)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	rt := &I18nRuntime{Locale: "en", Translator: newFakeTranslator().Translate}
	_, err = tpl.Execute(WithI18n(Context{"n": 2.5}, rt))
	if err == nil || !strings.Contains(err.Error(), "integer") {
		t.Fatalf("expected integer count error, got %v", err)
	}
}
