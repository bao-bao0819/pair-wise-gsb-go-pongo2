package pongo2

import (
	"errors"
	"strings"
	"testing"
)

func newStrictSet(t *testing.T) *TemplateSet {
	t.Helper()
	set := NewSet("strict-undefined", &DummyLoader{})
	set.SetStrictUndefined(true)
	return set
}

func renderStrict(t *testing.T, tpl string, ctx Context) (string, error) {
	t.Helper()
	tmpl, err := newStrictSet(t).FromString(tpl)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	return tmpl.Execute(ctx)
}

// A strict-set rendering that expects an error containing all fragments.
func mustErrContain(t *testing.T, tpl string, ctx Context, fragments ...string) {
	t.Helper()
	_, err := renderStrict(t, tpl, ctx)
	if err == nil {
		t.Fatalf("expected error for %q, got none", tpl)
	}
	for _, f := range fragments {
		if !strings.Contains(err.Error(), f) {
			t.Fatalf("error %q for %q should contain %q", err.Error(), tpl, f)
		}
	}
}

// A strict-set rendering that expects a concrete output.
func mustRender(t *testing.T, tpl string, ctx Context, want string) {
	t.Helper()
	got, err := renderStrict(t, tpl, ctx)
	if err != nil {
		t.Fatalf("unexpected error for %q: %v", tpl, err)
	}
	if got != want {
		t.Fatalf("for %q got %q, want %q", tpl, got, want)
	}
}

type strictProfile struct {
	Name string
}

// TestStrictUndefinedChainedAccess covers the core requirement: missing
// roots, map keys and struct fields produce an undefined value carrying the
// full access path, and the error distinguishes where the chain broke.
func TestStrictUndefinedChainedAccess(t *testing.T) {
	// Missing intermediate map key vs. missing leaf.
	mustErrContain(t,
		"{{ user.profile.name }}",
		Context{"user": map[string]any{}},
		"map key 'user.profile' does not exist",
		"(accessed as user.profile.name)")

	mustErrContain(t,
		"{{ user.profile.name }}",
		Context{"user": map[string]any{"profile": map[string]any{}}},
		"map key 'user.profile.name' does not exist")

	// Missing root variable.
	mustErrContain(t,
		"{{ missing.deep.path }}",
		Context{},
		"root variable 'missing' is not defined",
		"(accessed as missing.deep.path)")

	// Missing struct field, with profile present.
	mustErrContain(t,
		"{{ p.Age }}",
		Context{"p": strictProfile{Name: "x"}},
		"field 'p.Age' does not exist")

	// Dynamic map subscript with a missing key.
	mustErrContain(t,
		"{{ m[k] }}",
		Context{"m": map[string]string{}, "k": "nope"},
		"map key 'm[nope]' does not exist")

	// Out-of-range index.
	mustErrContain(t,
		"{{ items.5 }}",
		Context{"items": []int{1, 2}},
		"index 'items.5' is out of range")

	// Nil pointer container: reports the container as nil, full path present.
	var nilProfile *strictProfile
	mustErrContain(t,
		"{{ p.name }}",
		Context{"p": nilProfile},
		"'p' is nil",
		"(accessed as p.name)")
}

// TestStrictUndefinedDefinedValues ensures explicit nil/false/0/"" remain
// defined and render normally.
func TestStrictUndefinedDefinedValues(t *testing.T) {
	mustRender(t, "{{ v }}", Context{"v": nil}, "")
	mustRender(t, "{{ v }}", Context{"v": false}, "False")
	mustRender(t, "{{ v }}", Context{"v": 0}, "0")
	mustRender(t, "{{ v }}", Context{"v": ""}, "")

	// Defining an explicit nil root and then default_if_none works.
	mustRender(t, `{{ v|default_if_none:"N/A" }}`, Context{"v": nil}, "N/A")

	// Defined falsy values are detected by "defined" as defined.
	for _, val := range []any{nil, false, 0, ""} {
		mustRender(t, "{% if defined v %}yes{% else %}no{% endif %}",
			Context{"v": val}, "yes")
	}
}

// TestStrictDefinedOperator covers prefix/suffix presence tests and
// negation, which intentionally consume undefined without erroring.
func TestStrictDefinedOperator(t *testing.T) {
	mustRender(t,
		"{% if defined user.profile.name %}yes{% else %}no{% endif %}",
		Context{"user": map[string]any{}}, "no")

	mustRender(t,
		"{% if user.profile.name is defined %}yes{% else %}no{% endif %}",
		Context{"user": map[string]any{"profile": map[string]any{"name": "x"}}}, "yes")

	mustRender(t,
		"{% if user.x is not defined %}undef{% endif %}",
		Context{"user": map[string]any{}}, "undef")

	mustRender(t,
		"{% if not defined missing %}undef{% endif %}",
		Context{}, "undef")

	// "defined" stays usable as an ordinary variable name (backwards compat).
	mustRender(t, "{{ defined }}", Context{"defined": "value"}, "value")
}

// TestStrictDefaultFilter verifies default tolerates missing fields while
// default_if_none reacts only to explicit nil.
func TestStrictDefaultFilter(t *testing.T) {
	mustRender(t,
		`{{ user.profile.name|default:"guest" }}`,
		Context{"user": map[string]any{}}, "guest")

	// Explicit zero/falsy defined value is still replaced by default.
	mustRender(t, `{{ v|default:"guest" }}`, Context{"v": 0}, "guest")

	// default_if_none must not swallow an undefined sentinel.
	mustErrContain(t,
		`{{ user.profile.name|default_if_none:"guest" }}`,
		Context{"user": map[string]any{}},
		"map key 'user.profile' does not exist")
}

// TestStrictFiltersDontSwallowSentinel ensures ordinary filters surface
// undefined inputs rather than converting them silently to a zero value.
func TestStrictFiltersDontSwallowSentinel(t *testing.T) {
	for _, tpl := range []string{
		"{{ missing|upper }}",
		`{{ missing|join:"-" }}`,
		"{{ missing|length }}",
	} {
		mustErrContain(t, tpl, Context{}, "missing")
	}

	// An undefined filter argument also errors (it is consumed concretely).
	mustErrContain(t,
		`{{ "x"|default:missing }}`,
		Context{},
		"argument of filter 'default'",
		"missing")

	// An undefined element inside an array literal cannot be flattened to nil.
	mustErrContain(t,
		`{{ [missing]|join:"-" }}`,
		Context{},
		"join element",
		"root variable 'missing'")

	// Same for iteration: an undefined element errors when its body consumes it.
	mustErrContain(t,
		`{% for x in [missing] %}{{ x }}{% endfor %}`,
		Context{},
		"root variable 'missing'")
}

// TestStrictShortCircuit verifies and/or never evaluate the skipped branch,
// so an undefined value there must not produce an error.
func TestStrictShortCircuit(t *testing.T) {
	mustRender(t,
		"{% if false and user.missing %}y{% else %}n{% endif %}",
		Context{}, "n")
	mustRender(t,
		"{% if true or user.missing %}y{% else %}n{% endif %}",
		Context{}, "y")
	// The taken branch with a real undefined value still errors.
	mustErrContain(t,
		"{% if true and user.missing %}y{% endif %}",
		Context{"user": map[string]any{}}, "map key 'user.missing'")
	mustErrContain(t,
		"{% if false or user.missing %}y{% endif %}",
		Context{"user": map[string]any{}}, "map key 'user.missing'")
}

// TestStrictArithmeticComparisonIteration covers the other consumers.
func TestStrictArithmeticComparisonIteration(t *testing.T) {
	mustErrContain(t, "{{ missing + 1 }}", Context{}, "arithmetic", "missing")
	mustErrContain(t, "{{ 1 * missing }}", Context{}, "arithmetic", "missing")
	mustErrContain(t, "{% if missing == 1 %}x{% endif %}", Context{}, "comparison", "missing")
	mustErrContain(t, "{% if missing %}x{% endif %}", Context{}, "if-condition", "missing")
	mustErrContain(t, "{% if not missing %}x{% endif %}", Context{}, "boolean test", "missing")
	mustErrContain(t,
		"{% for x in missing %}{{ x }}{% endfor %}",
		Context{}, "for-loop iteration", "missing")
	mustErrContain(t, "{% firstof missing %}", Context{}, "firstof", "missing")
}

// TestStrictMacroArguments verifies undefined values propagate into macro
// arguments, can be tested with "defined", and error only when consumed.
func TestStrictMacroArguments(t *testing.T) {
	// Consuming an undefined argument inside the macro errors with path.
	mustErrContain(t,
		"{% macro greet(name) %}Hi {{ name }}{% endmacro %}{{ greet(user.missing) }}",
		Context{"user": map[string]any{}}, "map key 'user.missing'")

	// The macro can intentionally detect an undefined argument.
	mustRender(t,
		"{% macro greet(name) %}Hi {% if defined name %}{{ name }}{% else %}anon{% endif %}{% endmacro %}"+
			"{{ greet(user.missing) }}",
		Context{}, "Hi anon")

	// A missing macro argument with no default is an explicit nil (defined).
	mustRender(t,
		"{% macro greet(name) %}[{% if defined name %}d{% else %}u{% endif %}]{% endmacro %}{{ greet() }}",
		Context{}, "[d]")

	// A default expression is evaluated lazily only for missing args. Here
	// the default is itself undefined, so supplying no argument yields an
	// (intentionally detectable) undefined b.
	mustRender(t,
		"{% macro f(a, b=missing) %}{{ a }}-{% if defined b %}d{% else %}u{% endif %}{% endmacro %}{{ f(1) }}",
		Context{}, "1-u")

	// Supplying b positionally uses the supplied value, not the default.
	mustRender(t,
		"{% macro f(a, b=missing) %}{{ a }}-{{ b }}{% endmacro %}{{ f(1, 2) }}",
		Context{}, "1-2")

	// But calling without the argument evaluates the default and surfaces it.
	mustErrContain(t,
		"{% macro f(a, b=missing) %}{{ b }}{% endmacro %}{{ f(1) }}",
		Context{}, "root variable 'missing'")
}

// TestStrictHostMethodErrorNotMasked verifies a genuine error returned by a
// host function is propagated as-is, not turned into an undefined value.
func TestStrictHostMethodErrorNotMasked(t *testing.T) {
	hostErr := errors.New("boom sentinel")
	mustErrContain(t,
		"{{ boom() }}",
		Context{"boom": func() (string, error) { return "", hostErr }},
		"boom sentinel")
}

// TestStrictUndefinedStoredInContext checks set/with can hold an undefined
// value without immediately failing; it errors only when consumed.
func TestStrictUndefinedStoredInContext(t *testing.T) {
	mustErrContain(t,
		"{% set x = user.missing %}{{ x }}",
		Context{"user": map[string]any{}}, "map key 'user.missing'")

	mustRender(t,
		"{% set x = user.missing %}{% if defined x %}y{% else %}n{% endif %}",
		Context{"user": map[string]any{}}, "n")

	mustRender(t,
		"{% with x=user.missing %}{% if defined x %}y{% else %}n{% endif %}{% endwith %}",
		Context{"user": map[string]any{}}, "n")
}

// TestLenientModeUnchanged makes sure the historical behavior is preserved
// when strict mode is off: missing values stay empty and never error.
func TestLenientModeUnchanged(t *testing.T) {
	set := NewSet("lenient", &DummyLoader{})

	render := func(tpl string, ctx Context) string {
		t.Helper()
		tmpl, err := set.FromString(tpl)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		out, err := tmpl.Execute(ctx)
		if err != nil {
			t.Fatalf("lenient render failed for %q: %v", tpl, err)
		}
		return out
	}

	cases := map[string]string{
		"{{ missing }}":                                     "",
		"{{ user.profile.name }}":                           "",
		"{{ missing|upper }}":                               "",
		"{{ missing|default:'x' }}":                         "x",
		"{% if missing %}y{% else %}n{% endif %}":           "n",
		"{% if false and missing %}y{% else %}n{% endif %}": "n",
		"{% for x in missing %}[{{ x }}]{% endfor %}":       "",
		"{{ missing + 1 }}":                                 "1",
		"{% if missing == nil %}eq{% else %}ne{% endif %}":  "eq",
	}
	ctx := Context{"user": map[string]any{}}
	for tpl, want := range cases {
		if got := render(tpl, ctx); got != want {
			t.Errorf("lenient %q = %q, want %q", tpl, got, want)
		}
	}
}

// TestStrictOptionPropagation ensures the set option is copied onto parsed
// templates and the per-template option behaves like the set option.
func TestStrictOptionPropagation(t *testing.T) {
	if DefaultSet.Options.StrictUndefined {
		t.Fatal("default set must remain lenient")
	}

	set := NewSet("opt", &DummyLoader{})
	set.SetStrictUndefined(true)
	tmpl, err := set.FromString("{{ missing }}")
	if err != nil {
		t.Fatal(err)
	}
	if !tmpl.Options.StrictUndefined {
		t.Fatal("StrictUndefined should propagate from set to template")
	}
	if _, err := tmpl.Execute(Context{}); err == nil {
		t.Fatal("expected strict error from set-configured template")
	}

	// Per-template opt-in on a lenient set.
	lenientSet := NewSet("opt-lenient", &DummyLoader{})
	tmpl2, err := lenientSet.FromString("{{ missing }}")
	if err != nil {
		t.Fatal(err)
	}
	out, err := tmpl2.Execute(Context{})
	if err != nil || out != "" {
		t.Fatalf("expected lenient render, got %q / %v", out, err)
	}
	tmpl2.Options.StrictUndefined = true
	if _, err := tmpl2.Execute(Context{}); err == nil {
		t.Fatal("expected strict error after enabling option on template")
	}
}
