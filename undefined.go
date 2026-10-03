package pongo2

import (
	"fmt"
)

// undefinedInfo describes why and where a value turned out to be undefined.
// It is only created when Options.StrictUndefined is enabled.
type undefinedInfo struct {
	// path contains the already-rendered access segments, for example
	// ["user", "profile", "name"]. Integer indices are rendered as "[0]" and
	// subscripts with non-identifier keys as "['key']".
	path []string

	// detail explains which part of the path was missing.
	detail undefinedDetail

	// missingSegment is the rendered segment of path at which the value
	// became undefined (the last segment in path).
	missingSegment string

	// missingName is the bare name/index used in human-readable messages,
	// without path-rendering brackets (e.g. "profile" rather than "['profile']").
	missingName string
}

type undefinedDetail int

const (
	undefinedRootVariable undefinedDetail = iota
	undefinedMapKey
	undefinedStructField
	undefinedNilDereference
	undefinedIndex
)

func (d undefinedDetail) describe() string {
	switch d {
	case undefinedRootVariable:
		return "the variable is not defined"
	case undefinedMapKey:
		return "the map key is not defined"
	case undefinedStructField:
		return "the struct field is not defined"
	case undefinedNilDereference:
		return "cannot access an attribute on a nil value"
	case undefinedIndex:
		return "the index is out of range"
	default:
		return "the value is not defined"
	}
}

// undefinedPath renders the full access path, e.g. "user.profile.name".
func (u *undefinedInfo) undefinedPath() string {
	path := ""
	for _, segment := range u.path {
		if len(segment) > 0 && segment[0] == '[' {
			path += segment
		} else {
			if path != "" {
				path += "."
			}
			path += segment
		}
	}
	return path
}

func (u *undefinedInfo) clone() *undefinedInfo {
	path := make([]string, len(u.path))
	copy(path, u.path)
	return &undefinedInfo{
		path:           path,
		detail:         u.detail,
		missingSegment: u.missingSegment,
		missingName:    u.missingName,
	}
}

// extend returns a copy of u with one more access segment appended.
func (u *undefinedInfo) extend(segment string) *undefinedInfo {
	extended := u.clone()
	extended.path = append(extended.path, segment)
	return extended
}

// newUndefinedValue creates an undefined value carrying its access path.
func newUndefinedValue(path []string, detail undefinedDetail) *Value {
	return &Value{
		undef: &undefinedInfo{
			path:           path,
			detail:         detail,
			missingSegment: lastSegment(path),
		},
	}
}

func lastSegment(path []string) string {
	if len(path) == 0 {
		return ""
	}
	return path[len(path)-1]
}

// IsUndefined reports whether this value is a missing variable, map key or
// struct field in strict undefined mode.
//
// Explicit nil values (for example a context containing "name": nil) are
// defined, so IsUndefined returns false for them. The same applies to false,
// 0 and empty strings.
func (v *Value) IsUndefined() bool {
	return v.undef != nil
}

// IsDefined is the inverse of IsUndefined. It provides templates with an
// explicit way to test whether a value exists:
//
//	{% if user.name|is_defined %}...{% endif %}
func (v *Value) IsDefined() bool {
	return !v.IsUndefined()
}

// UndefinedPath returns the full access path of an undefined value, e.g.
// "user.profile.name". It returns an empty string for defined values.
func (v *Value) UndefinedPath() string {
	if v.undef == nil {
		return ""
	}
	return v.undef.undefinedPath()
}

// undefinedConsumptionError builds the error raised when an undefined value
// is consumed by output, arithmetic, comparisons or iteration. action
// describes the kind of consumption (e.g. "render" or "iterate over").
func undefinedConsumptionError(v *Value, action string) error {
	path := v.UndefinedPath()
	where := path
	if v.undef.missingSegment != "" {
		name := v.undef.missingName
		if name == "" {
			name = v.undef.missingSegment
		}
		where = fmt.Sprintf("%s ('%s' is missing)", path, name)
	}
	return fmt.Errorf(
		"cannot %s an undefined value at '%s': %s",
		action, where, v.undef.detail.describe(),
	)
}

// ensureDefined returns a template-positioned execution error if v is
// undefined. action describes the consuming operation ("add", "compare", ...).
func ensureDefined(v *Value, token *Token, ctx *ExecutionContext, action string) error {
	if !v.IsUndefined() {
		return nil
	}
	return ctx.OrigError(undefinedConsumptionError(v, action), token)
}
