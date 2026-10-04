package pongo2

import "testing"

// FuzzContextualCompile drives the contextual analyzer with arbitrary
// template bytes. Any output position must either be classified or produce a
// clean error; the analyzer must never panic.
func FuzzContextualCompile(f *testing.F) {
	f.Add(`<a href="{{ x }}">{{ x }}</a><script>var s="{{ x }}";</script>`)
	f.Add(`<a title="{% if c %}{{ x }}{% endif %}">`)
	f.Add(`<style>{{ x }}</style>`)
	f.Add(`<script type="application/json">{{ x }}</script>`)

	f.Fuzz(func(t *testing.T, src string) {
		set := NewSet("fuzz-contextual", &DummyLoader{})
		set.SetContextualEscaping(true)

		tpl, err := set.FromString(src)
		if err != nil {
			return
		}
		_, _ = tpl.Execute(Context{"x": `a"b'<>:javascript:alert(1)`})
	})
}
