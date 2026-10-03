package pongo2_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/flosch/pongo2/v7"
)

// strictSet returns an isolated TemplateSet with StrictUndefined enabled.
func strictSet(t *testing.T) *pongo2.TemplateSet {
	t.Helper()
	set := pongo2.NewSet("strict-undefined", &DummyLoader{})
	set.Options.StrictUndefined = true
	return set
}

func mustRenderStrict(t *testing.T, set *pongo2.TemplateSet, src string, ctx pongo2.Context) string {
	t.Helper()
	tpl, err := set.FromString(src)
	if err != nil {
		t.Fatalf("failed to parse %q: %v", src, err)
	}
	out, err := tpl.Execute(ctx)
	if err != nil {
		t.Fatalf("failed to execute %q: %v", src, err)
	}
	return out
}

func renderStrictErr(t *testing.T, set *pongo2.TemplateSet, src string, ctx pongo2.Context) error {
	t.Helper()
	tpl, err := set.FromString(src)
	if err != nil {
		t.Fatalf("failed to parse %q: %v", src, err)
	}
	_, err = tpl.Execute(ctx)
	if err == nil {
		t.Fatalf("expected an error executing %q, got none", src)
	}
	return err
}

func errContainsAll(t *testing.T, err error, parts ...string) {
	t.Helper()
	msg := err.Error()
	for _, part := range parts {
		if !strings.Contains(msg, part) {
			t.Fatalf("error %q does not contain %q", msg, part)
		}
	}
}

// TestStrictUndefinedDisabledPreservesLenientBehavior makes sure the existing
// lenient behavior (missing variable renders as empty) remains the default.
func TestStrictUndefinedDisabledPreservesLenientBehavior(t *testing.T) {
	tpl, err := pongo2.FromString("[{{ user.profile.name }}]")
	if err != nil {
		t.Fatal(err)
	}
	out, err := tpl.Execute(pongo2.Context{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "[]" {
		t.Fatalf("expected empty rendering, got %q", out)
	}
}

// TestStrictUndefinedChainPaths verifies that chained access keeps the full
// access path and that error messages distinguish the concrete missing step.
func TestStrictUndefinedChainPaths(t *testing.T) {
	set := strictSet(t)

	// Root variable is missing.
	err := renderStrictErr(t, set, "{{ user.profile.name }}", pongo2.Context{})
	errContainsAll(t, err, "user.profile.name", "'user' is missing")

	// user exists, but profile is missing.
	err = renderStrictErr(t, set, "{{ user.profile.name }}", pongo2.Context{
		"user": map[string]any{},
	})
	errContainsAll(t, err, "user.profile.name", "'profile' is missing")

	// profile exists, but name is missing.
	err = renderStrictErr(t, set, "{{ user.profile.name }}", pongo2.Context{
		"user": map[string]any{"profile": map[string]any{}},
	})
	errContainsAll(t, err, "user.profile.name", "'name' is missing")

	// Out-of-range index keeps the full path and index segment.
	err = renderStrictErr(t, set, "{{ user.items[2] }}", pongo2.Context{
		"user": map[string]any{"items": []int{1}},
	})
	errContainsAll(t, err, "user.items[2]", "'2' is missing", "out of range")

	// Map subscript by a string key.
	err = renderStrictErr(t, set, "{{ m['profile'] }}", pongo2.Context{
		"m": map[string]any{},
	})
	errContainsAll(t, err, "m['profile']", "'profile' is missing", "map key")
}

// TestStrictUndefinedStructFields covers struct field resolution.
func TestStrictUndefinedStructFields(t *testing.T) {
	type profile struct{ Email string }
	type user struct{ Profile *profile }

	set := strictSet(t)

	err := renderStrictErr(t, set, "{{ u.Profile.Email }}", pongo2.Context{
		"u": user{}, // Profile is an explicit nil pointer
	})
	errContainsAll(t, err, "u.Profile.Email", "nil")

	err = renderStrictErr(t, set, "{{ u.Missing }}", pongo2.Context{
		"u": user{Profile: &profile{}},
	})
	errContainsAll(t, err, "u.Missing", "struct field")

	out := mustRenderStrict(t, set, "{{ u.Profile.Email|default:\"no-email\" }}",
		pongo2.Context{"u": user{Profile: &profile{}}})
	if out != "no-email" {
		t.Fatalf("expected fallback, got %q", out)
	}
}

// TestStrictUndefinedExplicitValuesAreDefined ensures explicit nil, false, 0
// and empty strings are not treated as undefined.
func TestStrictUndefinedExplicitValuesAreDefined(t *testing.T) {
	set := strictSet(t)

	cases := []struct {
		name string
		src  string
		ctx  pongo2.Context
		want string
	}{
		{"explicit nil", "{{ x|is_defined }}:{{ x|is_undefined }}", pongo2.Context{"x": nil}, "True:False"},
		{"false", "{{ x|is_defined }}:{{ x|is_undefined }}", pongo2.Context{"x": false}, "True:False"},
		{"zero", "{{ x|is_defined }}:{{ x|is_undefined }}", pongo2.Context{"x": 0}, "True:False"},
		{"empty string", "{{ x|is_defined }}:{{ x|is_undefined }}", pongo2.Context{"x": ""}, "True:False"},
		{"nil default_if_none", "[{{ x|default_if_none:\"D\" }}]", pongo2.Context{"x": nil}, "[D]"},
		{"false keeps default semantics", "{{ x|default:\"D\" }}", pongo2.Context{"x": false}, "D"},
		{"zero keeps default semantics", "{{ x|default:\"D\" }}", pongo2.Context{"x": 0}, "D"},
		{"empty map value renders empty", "{{ m.k }}", pongo2.Context{"m": map[string]any{"k": ""}}, ""},
		{"nil map value renders empty", "{{ m.k }}", pongo2.Context{"m": map[string]*string{"k": nil}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRenderStrict(t, set, tc.src, tc.ctx); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// TestStrictUndefinedFilters covers default fallbacks, definedness filters
// and rejection of ordinary filters that would swallow the sentinel.
func TestStrictUndefinedFilters(t *testing.T) {
	set := strictSet(t)

	if got := mustRenderStrict(t, set,
		"{{ user.name|default:\"Guest\" }}", pongo2.Context{}); got != "Guest" {
		t.Fatalf("expected default fallback, got %q", got)
	}

	if got := mustRenderStrict(t, set,
		"{% if user.name|is_defined %}{{ user.name }}{% else %}none{% endif %}",
		pongo2.Context{}); got != "none" {
		t.Fatalf("expected is_defined branch, got %q", got)
	}

	if got := mustRenderStrict(t, set,
		"{% if user.name|is_undefined %}missing{% endif %}",
		pongo2.Context{}); got != "missing" {
		t.Fatalf("expected is_undefined branch, got %q", got)
	}

	// Defined value through is_defined stays defined and renders normally.
	if got := mustRenderStrict(t, set,
		"{{ user.name|is_defined }}", pongo2.Context{"user": map[string]string{"name": "Jo"}}); got != "True" {
		t.Fatalf("expected True, got %q", got)
	}

	// Ordinary filters must not silently turn the sentinel into a zero value.
	err := renderStrictErr(t, set, "{{ user.name|upper }}", pongo2.Context{})
	errContainsAll(t, err, "user.name", "upper")

	err = renderStrictErr(t, set, "{{ user.name|add:2 }}", pongo2.Context{})
	errContainsAll(t, err, "user.name", "add")

	// An undefined filter parameter must not be swallowed as a zero value.
	err = renderStrictErr(t, set, "{{ 3|add:missing }}", pongo2.Context{})
	errContainsAll(t, err, "missing", "add")
}

// TestStrictUndefinedConditionShortCircuit verifies plain if checks keep
// working with undefined values and that unevaluated and/or branches never
// raise an error.
func TestStrictUndefinedConditionShortCircuit(t *testing.T) {
	set := strictSet(t)

	cases := []struct {
		name string
		src  string
		want string
	}{
		{"bare if", "{% if user.name %}y{% else %}n{% endif %}", "n"},
		{"not", "{% if not user.name %}N{% endif %}", "N"},
		{"and short circuit", "{% if false and user.name %}x{% else %}ok{% endif %}", "ok"},
		{"or short circuit", "{% if true or user.name %}ok{% endif %}", "ok"},
		{"and reaches defined", "{% if true and flag %}y{% endif %}", "y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := pongo2.Context{"flag": true}
			if got := mustRenderStrict(t, set, tc.src, ctx); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}

	// A reached and/or branch whose value is only used as the if-condition is
	// still a truthiness test (undefined is falsy); it is output and
	// arithmetic inside the branch that consume it and must error.
	err := renderStrictErr(t, set,
		"{% if true or user.name %}{{ user.name }}{% endif %}", pongo2.Context{})
	errContainsAll(t, err, "user.name")

	err = renderStrictErr(t, set,
		"{% if flag and other %}{{ user.name }}{% endif %}",
		pongo2.Context{"flag": true, "other": true})
	errContainsAll(t, err, "user.name")

	// A reached branch consumed by arithmetic errors even inside a condition.
	err = renderStrictErr(t, set, "{% if flag and user.age == 1 %}x{% endif %}",
		pongo2.Context{"flag": true})
	errContainsAll(t, err, "user.age")
}

// TestStrictUndefinedArithmeticComparisonIteration ensures all consuming
// operations report both the location and the access path.
func TestStrictUndefinedArithmeticComparisonIteration(t *testing.T) {
	set := strictSet(t)

	err := renderStrictErr(t, set, "{{ user.age + 1 }}", pongo2.Context{})
	errContainsAll(t, err, "user.age", "Line 1")

	err = renderStrictErr(t, set, "{{ 1 + user.age }}", pongo2.Context{})
	errContainsAll(t, err, "user.age")

	err = renderStrictErr(t, set, "{{ user.age * 2 }}", pongo2.Context{})
	errContainsAll(t, err, "user.age")

	err = renderStrictErr(t, set, "{% if user.age == 1 %}x{% endif %}", pongo2.Context{})
	errContainsAll(t, err, "user.age", "compare")

	err = renderStrictErr(t, set, "{% for x in user.items %}[{{ x }}]{% endfor %}", pongo2.Context{})
	errContainsAll(t, err, "user.items", "iterate over")

	// Membership tests are definitional and tolerate undefined operands.
	if got := mustRenderStrict(t, set,
		"{% if 'k' in missing %}y{% else %}n{% endif %}", pongo2.Context{}); got != "n" {
		t.Fatalf("expected in-test to tolerate undefined, got %q", got)
	}
}

// TestStrictUndefinedMacroArguments verifies undefined values survive macro
// argument passing and can either be handled with default inside the macro
// or surface with their original path when rendered directly.
func TestStrictUndefinedMacroArguments(t *testing.T) {
	set := strictSet(t)

	src := `{% macro greet(name) %}[{{ name|default:"anon" }}]{% endmacro %}{{ greet(user.name) }}`
	if got := mustRenderStrict(t, set, src, pongo2.Context{}); got != "[anon]" {
		t.Fatalf("expected macro default, got %q", got)
	}

	// Omitted macro argument (explicit nil) is defined and handled by
	// default_if_none, but not by default on "".
	src = `{% macro greet(name) %}[{{ name|default_if_none:"anon" }}]{% endmacro %}{{ greet() }}`
	if got := mustRenderStrict(t, set, src, pongo2.Context{}); got != "[anon]" {
		t.Fatalf("expected omitted arg default, got %q", got)
	}

	// Rendering an undefined argument directly keeps the caller's path.
	src = `{% macro greet(name) %}{{ name }}{% endmacro %}{{ greet(user.name) }}`
	err := renderStrictErr(t, set, src, pongo2.Context{})
	errContainsAll(t, err, "user.name")

	// A default value expression that is itself undefined propagates and can
	// be rescued inside the macro body.
	src = `{% macro greet(name=user.name) %}{{ name|default:"anon" }}{% endmacro %}{{ greet() }}`
	if got := mustRenderStrict(t, set, src, pongo2.Context{}); got != "anon" {
		t.Fatalf("expected undefined default to be rescued, got %q", got)
	}
}

// TestStrictUndefinedHostErrorsNotMasked ensures errors returned by host
// functions propagate as themselves instead of being turned into undefined.
func TestStrictUndefinedHostErrorsNotMasked(t *testing.T) {
	set := strictSet(t)

	hostErr := errors.New("boom host failure")
	ctx := pongo2.Context{
		"boom": func() (string, error) { return "", hostErr },
	}
	err := renderStrictErr(t, set, "{{ boom() }}", ctx)
	if !strings.Contains(err.Error(), "boom host failure") {
		t.Fatalf("expected host error, got %v", err)
	}
	if !errors.Is(err, hostErr) {
		t.Fatalf("expected errors.Is to find the host error, got %v", err)
	}
}

// TestStrictUndefinedMapKeyUndefinedArgument ensures an undefined value used
// as a map key is reported rather than silently matching nothing.
func TestStrictUndefinedMapKeyUndefinedArgument(t *testing.T) {
	set := strictSet(t)
	err := renderStrictErr(t, set, "{{ m[key] }}", pongo2.Context{
		"m": map[string]string{"a": "x"},
	})
	errContainsAll(t, err, "key", "map key")
}
