package pongo2_test

import (
	"strings"
	"testing"

	"github.com/flosch/pongo2/v7"
)

// TestStrictUndefinedSetAndWith covers value propagation through the set and
// with tags, plus a global-set-level StrictUndefined configuration.
func TestStrictUndefinedSetAndWith(t *testing.T) {
	set := pongo2.NewSet("strict-edge", &DummyLoader{})
	set.Options.StrictUndefined = true

	// {% set %} keeps the undefined sentinel; default can rescue it.
	if got := mustRenderStrict(t, set,
		"{% set n = user.name %}{{ n|default:\"G\" }}", pongo2.Context{}); got != "G" {
		t.Fatalf("expected G, got %q", got)
	}

	// Consuming a set undefined value directly still errors with the path.
	err := renderStrictErr(t, set, "{% set n = user.name %}{{ n }}", pongo2.Context{})
	if !strings.Contains(err.Error(), "user.name") {
		t.Fatalf("expected user.name in error, got %v", err)
	}

	// {% with %} propagation.
	if got := mustRenderStrict(t, set,
		"{% with n=user.name %}{{ n|is_undefined }}{% endwith %}", pongo2.Context{}); got != "True" {
		t.Fatalf("expected True, got %q", got)
	}

	// Chained defaults: the second default rescues an undefined fallback.
	if got := mustRenderStrict(t, set,
		`{{ user.name|default:other|default:"final" }}`, pongo2.Context{}); got != "final" {
		t.Fatalf("expected final, got %q", got)
	}

	// Comparing an undefined value is a consuming operation and errors
	// (use is_defined to distinguish it from an explicit nil).
	errCmp := renderStrictErr(t, set,
		"{% if user.name == nil %}eq{% else %}ne{% endif %}", pongo2.Context{})
	if !strings.Contains(errCmp.Error(), "user.name") {
		t.Fatalf("expected user.name comparison error, got %v", errCmp)
	}
}

// TestStrictUndefinedArrayDefinition ensures array literals propagate
// undefined members so later consumption errors.
func TestStrictUndefinedArrayDefinition(t *testing.T) {
	set := pongo2.NewSet("strict-array", &DummyLoader{})
	set.Options.StrictUndefined = true

	// Definedness check inside a loop tolerates the falsy undefined member.
	out := mustRenderStrict(t, set,
		"{% for x in [a, b] %}{% if x|is_defined %}Y{% else %}N{% endif %}{% endfor %}",
		pongo2.Context{"a": "v"})
	if out != "YN" {
		t.Fatalf("expected YN, got %q", out)
	}

	// The undefined member propagates and is rescued by default when iterated
	// (direct "[...][i]" subscript chaining is an existing parser limitation).
	out2 := mustRenderStrict(t, set,
		"{% for x in [a, missing] %}{{ x|default:\"D\" }}{% endfor %}",
		pongo2.Context{"a": "v"})
	if out2 != "vD" {
		t.Fatalf("expected vD, got %q", out2)
	}
}
