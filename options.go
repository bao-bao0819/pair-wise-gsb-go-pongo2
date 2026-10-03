package pongo2

// Options allow you to change the behavior of template-engine. You can change
// the options before calling the Execute method.
type Options struct {
	// If this is set to true the first newline after a block is removed (block,
	// not variable tag!). Defaults to false.
	TrimBlocks bool

	// If this is set to true leading spaces and tabs are stripped from the
	// start of a line to a block. Defaults to false
	LStripBlocks bool

	// StrictUndefined enables strict handling of missing variables, map keys
	// and struct fields. When enabled, an access such as user.profile.name
	// returns an "undefined" value carrying the full access path instead of
	// silently becoming an empty string.
	//
	// An undefined value is falsy (so plain {% if user.name %} checks keep
	// working), but consuming it through output ({{ ... }}), arithmetic,
	// comparisons or iteration returns an execution error that reports both
	// the template position and the missing access path. Explicit nil, false,
	// 0 and empty strings are still considered defined values.
	//
	// Undefined values can be handled intentionally with the "default" and
	// "default_if_none" filters, and inspected with the "is_defined" and
	// "is_undefined" filters. Short-circuited "and"/"or" branches are never
	// evaluated and therefore never raise an error.
	//
	// Defaults to false (lenient Django-compatible behavior).
	StrictUndefined bool
}

func newOptions() *Options {
	return &Options{
		TrimBlocks:      false,
		LStripBlocks:    false,
		StrictUndefined: false,
	}
}

// Update updates this options from another options.
func (opt *Options) Update(other *Options) *Options {
	opt.TrimBlocks = other.TrimBlocks
	opt.LStripBlocks = other.LStripBlocks
	opt.StrictUndefined = other.StrictUndefined

	return opt
}
