// Package expr implements a small parser and evaluator for the subset of
// FlexMatch property expressions used inside rule measurements and reference
// values, e.g. "avg(flatten(players.skill))" or "teams[red].players.skill".
package expr

import "fmt"

// Kind identifies a Value's variant.
type Kind int

const (
	KindNone Kind = iota
	KindNumber
	KindString
	KindNumberList
	KindStringList
	KindNumberMatrix // []NumberList - one entry per player (used by set/avg ops)
	KindStringMatrix // []StringList - one entry per player
)

// Value is the result of evaluating an expression node.
type Value struct {
	Kind Kind
	Num  float64
	Str  string
	NL   []float64
	SL   []string
	NM   [][]float64
	SM   [][]string
}

// Number wraps a float64.
func Number(v float64) Value { return Value{Kind: KindNumber, Num: v} }

// String wraps a string.
func String(v string) Value { return Value{Kind: KindString, Str: v} }

// NumberList wraps a []float64 (no copy).
func NumberList(v []float64) Value { return Value{Kind: KindNumberList, NL: v} }

// StringList wraps a []string (no copy).
func StringList(v []string) Value { return Value{Kind: KindStringList, SL: v} }

func (v Value) String() string {
	switch v.Kind {
	case KindNumber:
		return fmt.Sprintf("%v", v.Num)
	case KindString:
		return fmt.Sprintf("%q", v.Str)
	case KindNumberList:
		return fmt.Sprintf("%v", v.NL)
	case KindStringList:
		return fmt.Sprintf("%v", v.SL)
	case KindNumberMatrix:
		return fmt.Sprintf("%v", v.NM)
	case KindStringMatrix:
		return fmt.Sprintf("%v", v.SM)
	}
	return "<none>"
}

// AsNumber returns v as a float64 if it is numeric.
func (v Value) AsNumber() (float64, bool) {
	if v.Kind == KindNumber {
		return v.Num, true
	}
	return 0, false
}

// AsString returns v as a string if it is a string.
func (v Value) AsString() (string, bool) {
	if v.Kind == KindString {
		return v.Str, true
	}
	return "", false
}

// FlattenNumbers turns a number-shaped value into a single []float64.
func (v Value) FlattenNumbers() ([]float64, bool) {
	switch v.Kind {
	case KindNumber:
		return []float64{v.Num}, true
	case KindNumberList:
		return v.NL, true
	case KindNumberMatrix:
		out := make([]float64, 0)
		for _, row := range v.NM {
			out = append(out, row...)
		}
		return out, true
	}
	return nil, false
}

// FlattenStrings turns a string-shaped value into a single []string.
func (v Value) FlattenStrings() ([]string, bool) {
	switch v.Kind {
	case KindString:
		return []string{v.Str}, true
	case KindStringList:
		return v.SL, true
	case KindStringMatrix:
		out := make([]string, 0)
		for _, row := range v.SM {
			out = append(out, row...)
		}
		return out, true
	}
	return nil, false
}
