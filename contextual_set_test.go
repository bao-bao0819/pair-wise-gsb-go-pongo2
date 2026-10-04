package pongo2

import "testing"

func TestSetContextualEscapingRejectedAfterFirstTemplate(t *testing.T) {
	set := NewSet("late-contextual", &memLoader{files: map[string]string{}})
	if _, err := set.FromString(`{{ "x" }}`); err != nil {
		t.Fatalf("seed template failed: %v", err)
	}
	if err := set.SetContextualEscaping(true); err == nil {
		t.Fatalf("enabling contextual mode after compiling a template must fail")
	}
}

func TestSetContextualEscapingRoundTrip(t *testing.T) {
	set := NewSet("roundtrip", &memLoader{files: map[string]string{}})
	if err := set.SetContextualEscaping(true); err != nil {
		t.Fatalf("enable failed: %v", err)
	}
	if !set.contextualEscaping {
		t.Fatalf("contextualEscaping flag not set")
	}
	if err := set.SetContextualEscaping(false); err != nil {
		t.Fatalf("disable failed: %v", err)
	}
	if set.contextualEscaping {
		t.Fatalf("contextualEscaping flag not cleared")
	}
	set.MustSetContextualEscaping(true)
	if !set.contextualEscaping {
		t.Fatalf("MustSetContextualEscaping did not enable")
	}
}
