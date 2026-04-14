package flexi

import "github.com/moepig/flexi/internal/core"

// AttributeKind identifies which variant an [Attribute] holds. It mirrors the
// four player attribute types FlexMatch supports: string, number,
// string_list, and string_number_map.
type AttributeKind = core.AttributeKind

// AttributeKind constants. Use [String], [Number], [StringList], or
// [StringNumberMap] to construct attributes rather than setting Kind by hand.
const (
	// AttrUnknown is the zero value and should not appear in well-formed input.
	AttrUnknown = core.AttrUnknown
	// AttrString corresponds to FlexMatch's "string" attribute type.
	AttrString = core.AttrString
	// AttrNumber corresponds to FlexMatch's "number" attribute type.
	AttrNumber = core.AttrNumber
	// AttrStringList corresponds to FlexMatch's "string_list" attribute type.
	AttrStringList = core.AttrStringList
	// AttrStringNumberMap corresponds to FlexMatch's "string_number_map" type.
	AttrStringNumberMap = core.AttrStringNumberMap
)

// Attribute is a tagged union mirroring a single FlexMatch player attribute
// value. Only the field selected by Kind is meaningful; other fields are
// zero.
//
// Construct attributes with [String], [Number], [StringList], or
// [StringNumberMap] rather than building the struct literally.
type Attribute = core.Attribute

// Attributes is a player's attribute bag, keyed by attribute name. The keys
// must match the names declared in the rule set's playerAttributes block for
// the rules to find them.
type Attributes = core.Attributes

// String returns an [Attribute] of kind [AttrString] holding v.
func String(v string) Attribute { return Attribute{Kind: AttrString, S: v} }

// Number returns an [Attribute] of kind [AttrNumber] holding v.
func Number(v float64) Attribute { return Attribute{Kind: AttrNumber, N: v} }

// StringList returns an [Attribute] of kind [AttrStringList] holding a copy
// of v. Subsequent mutations of v will not affect the returned attribute.
func StringList(v ...string) Attribute {
	cp := make([]string, len(v))
	copy(cp, v)
	return Attribute{Kind: AttrStringList, SL: cp}
}

// StringNumberMap returns an [Attribute] of kind [AttrStringNumberMap]
// holding a copy of v. Subsequent mutations of v will not affect the
// returned attribute.
func StringNumberMap(v map[string]float64) Attribute {
	cp := make(map[string]float64, len(v))
	for k, vv := range v {
		cp[k] = vv
	}
	return Attribute{Kind: AttrStringNumberMap, SDM: cp}
}
