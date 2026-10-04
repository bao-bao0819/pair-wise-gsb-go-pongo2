package pongo2

import (
	"strings"
	"testing"
)

func TestScannerSlotClassification(t *testing.T) {
	cases := []struct {
		name     string
		html     string
		wantKind escapeKind
		wantOK   bool
	}{
		{"text", `<p>{{`, escapeHTMLText, true},
		{"double attr", `<input value="{{`, escapeHTMLAttr, true},
		{"single attr", `<input value='{{`, escapeHTMLAttr, true},
		{"data attr", `<input data-x="{{`, escapeHTMLAttr, true},
		{"href", `<a href="{{`, escapeURLAttr, true},
		{"src", `<img src="{{`, escapeURLAttr, true},
		{"js double", `<script>var s = "{{`, escapeJSString, true},
		{"js single", `<script>var s = '{{`, escapeJSString, true},
		{"after bool attr", `<input disabled value="{{`, escapeHTMLAttr, true},
		{"multi attr", `<input id="a" title="{{`, escapeHTMLAttr, true},
		{"after close", `</p><b>{{`, escapeHTMLText, true},
		{"self closed", `<br/><p>{{`, escapeHTMLText, true},

		{"unquoted", `<input value={{`, 0, false},
		{"on attr", `<a onclick="{{`, 0, false},
		{"style attr", `<a style="{{`, 0, false},
		{"dynamic tag", `<{{`, 0, false},
		{"dynamic attr", `<input {{`, 0, false},
		{"comment", `<!-- {{`, 0, false},
		{"doctype", `<!DOCTYPE {{`, 0, false},
		{"css", `<style>{{`, 0, false},
		{"script code", `<script>{{`, 0, false},
		{"script slash", `<script>var a=1/2;var s="{{`, 0, false},
		{"template literal", "<script>var s=`{{", 0, false},
		{"close tag", `</{{`, 0, false},
		{"script closing gap", "<script>x</script {{", 0, false},
		{"after script close", `<script>x</script>{{`, escapeHTMLText, true},
		{"after style close", `<style>x</style>{{`, escapeHTMLText, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &htmlScanner{}
			var kind escapeKind
			var ok bool
			tokens, err := lex("t", tc.html)
			if err != nil {
				t.Fatal(err)
			}
			for _, tok := range tokens {
				if tok.Typ == TokenHTML {
					s.feedText(tok.Val)
				} else {
					kind, ok, _ = s.feedSlot()
				}
			}
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (state=%d)", ok, tc.wantOK, s.state)
			}
			if ok && kind != tc.wantKind {
				t.Fatalf("kind=%d want %d (state=%d)", kind, tc.wantKind, s.state)
			}
		})
	}
}

func TestScannerCommentThenText(t *testing.T) {
	s := &htmlScanner{}
	tokens, err := lex("t", `<!-- c --><p>{{</p>`)
	if err != nil {
		t.Fatal(err)
	}
	var kind escapeKind
	var ok bool
	for _, tok := range tokens {
		if tok.Typ == TokenHTML {
			s.feedText(tok.Val)
		} else {
			kind, ok, _ = s.feedSlot()
		}
	}
	if !ok || kind != escapeHTMLText {
		t.Fatalf("comment should return to text, got kind=%d ok=%v state=%d", kind, ok, s.state)
	}
}

func TestScannerURLProtocolCheck(t *testing.T) {
	safe := []string{"/a", "https://x", "Https://x", "#x", "?a=b", "//h/x", "a/b:c", "mailto:a@b", "a/b?x:y"}
	bad := []string{"javascript:1", " javascript:1", "data:text/html,x", "vbscript:x", "java\x00script:x"}

	for _, u := range safe {
		if err := checkSafeURL(u); err != nil {
			t.Errorf("safe URL %q rejected: %v", u, err)
		}
	}
	for _, u := range bad {
		if err := checkSafeURL(u); err == nil {
			t.Errorf("dangerous URL %q accepted", u)
		}
	}
}

func TestScannerJSEscape(t *testing.T) {
	got := escapeJSStringLiteral(`a"b'c\d<e>&`)
	for _, want := range []string{`\u0022`, `\u0027`, `\u005C`, `\u003C`, `\u003E`, `\u0026`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %q", want, got)
		}
	}
	got = escapeJSStringLiteral("a\nb\rc\t")
	for _, want := range []string{`\u000A`, `\u000D`, `\u0009`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %q", want, got)
		}
	}
}
