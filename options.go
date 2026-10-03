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

	// StrictUndefined makes missing root variables, map keys and struct fields
	// resolve to a special undefined value carrying the full access path
	// (e.g. "user.profile.name") instead of being silently treated as nil.
	//
	// Merely accessing attributes of an undefined value keeps extending the
	// path and never errors. An error is only raised when the undefined value
	// is consumed by output, arithmetic, comparison, iteration, a boolean test
	// or passed as a concrete function/macro argument. The "default"/
	// "default_if_none" filters and the "defined" operator handle it on
	// purpose.
	//
	// Explicit nil, false, 0 and empty strings remain defined values.
	// Defaults to false (the historical lenient behavior).
	StrictUndefined bool
}

func newOptions() *Options {
	return &Options{
		TrimBlocks:   false,
		LStripBlocks: false,
	}
}

// Update updates this options from another options.
func (opt *Options) Update(other *Options) *Options {
	opt.TrimBlocks = other.TrimBlocks
	opt.LStripBlocks = other.LStripBlocks
	opt.StrictUndefined = other.StrictUndefined

	return opt
}
