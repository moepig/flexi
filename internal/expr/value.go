// Package expr implements a parser and evaluator for FlexMatch property
// expressions used inside rule measurements and reference values, e.g.
// "avg(flatten(teams[*].players.attributes[skill]))" or
// "teams[red].players.attributes[skill]".
//
// Values are modelled as a recursive tree (scalars or lists of Values) so that
// nested results such as List<List<number>> — produced by multi-team scopes
// like teams[*] — can be aggregated per sublist, matching FlexMatch semantics
// ("operations on a nested list operate on each sublist individually").
package expr

import "fmt"

// Kind identifies a Value's variant.
type Kind int

const (
	KindNone Kind = iota
	KindNumber
	KindString
	KindList
)

// Value is the result of evaluating an expression node. A Value is either a
// scalar (KindNumber/KindString), an absent value (KindNone), or a list of
// Values (KindList) which may itself contain lists for nested scopes.
type Value struct {
	Kind Kind
	Num  float64
	Str  string
	List []Value
}

// Number wraps a float64.
func Number(v float64) Value { return Value{Kind: KindNumber, Num: v} }

// String wraps a string.
func String(v string) Value { return Value{Kind: KindString, Str: v} }

// ListOf wraps a slice of Values (no copy).
func ListOf(v []Value) Value { return Value{Kind: KindList, List: v} }

// NumberList wraps a []float64 as a list of Number values.
func NumberList(v []float64) Value {
	out := make([]Value, len(v))
	for i, n := range v {
		out[i] = Number(n)
	}
	return Value{Kind: KindList, List: out}
}

// StringList wraps a []string as a list of String values.
func StringList(v []string) Value {
	out := make([]Value, len(v))
	for i, s := range v {
		out[i] = String(s)
	}
	return Value{Kind: KindList, List: out}
}

func (v Value) String() string {
	switch v.Kind {
	case KindNumber:
		return fmt.Sprintf("%v", v.Num)
	case KindString:
		return fmt.Sprintf("%q", v.Str)
	case KindList:
		return fmt.Sprintf("%v", v.List)
	}
	return "<none>"
}

// AsNumber returns v as a float64 if it is a scalar number.
func (v Value) AsNumber() (float64, bool) {
	if v.Kind == KindNumber {
		return v.Num, true
	}
	return 0, false
}

// AsString returns v as a string if it is a scalar string.
func (v Value) AsString() (string, bool) {
	if v.Kind == KindString {
		return v.Str, true
	}
	return "", false
}

// FlattenNumbers deeply flattens any nesting of numeric values into a single
// []float64. It returns false if the value contains a non-numeric scalar.
func (v Value) FlattenNumbers() ([]float64, bool) {
	out := []float64{}
	ok := true
	var walk func(x Value)
	walk = func(x Value) {
		switch x.Kind {
		case KindNumber:
			out = append(out, x.Num)
		case KindList:
			for _, e := range x.List {
				walk(e)
			}
		case KindNone:
			// skip
		default:
			ok = false
		}
	}
	walk(v)
	if !ok {
		return nil, false
	}
	return out, true
}

// FlattenStrings deeply flattens any nesting of string values into a single
// []string. It returns false if the value contains a non-string scalar.
func (v Value) FlattenStrings() ([]string, bool) {
	out := []string{}
	ok := true
	var walk func(x Value)
	walk = func(x Value) {
		switch x.Kind {
		case KindString:
			out = append(out, x.Str)
		case KindList:
			for _, e := range x.List {
				walk(e)
			}
		case KindNone:
			// skip
		default:
			ok = false
		}
	}
	walk(v)
	if !ok {
		return nil, false
	}
	return out, true
}

// isListOfLists reports whether v is a list whose elements are themselves
// lists. Used to decide whether an aggregation should map over sublists.
func isListOfLists(v Value) bool {
	if v.Kind != KindList {
		return false
	}
	for _, e := range v.List {
		if e.Kind != KindList {
			return false
		}
	}
	return len(v.List) > 0
}
