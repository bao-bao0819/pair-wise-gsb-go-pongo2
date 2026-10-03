package pongo2

import "fmt"

// errUndefinedConsumed builds the error returned when an undefined value (only
// produced in strict-undefined mode) reaches an operation that needs a concrete
// value: output, arithmetic, comparison, iteration, boolean test or a
// function/filter/macro argument.
//
// The message always contains the full access path and distinguishes where the
// chain actually broke (e.g. a missing "user.profile" vs. an existing
// "profile" with a missing "name").
func errUndefinedConsumed(v *Value, action string) error {
	return fmt.Errorf("%s on an undefined value: %s", action, v.UndefinedError(""))
}

// checkUndefined returns a positioned execution error if v is undefined. It is
// the single entry point used whenever an evaluator or tag consumes a value.
func checkUndefined(ctx *ExecutionContext, v *Value, action string, token *Token) error {
	if v == nil || !v.IsUndefined() {
		return nil
	}
	return ctx.OrigError(errUndefinedConsumed(v, action), token)
}
