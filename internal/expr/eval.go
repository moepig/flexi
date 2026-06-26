package expr

import (
	"fmt"
	"math"
	"sort"

	"github.com/moepig/flexi/internal/core"
)

// EvalContext supplies the player population an expression operates over.
//
// Players is the full pool (used when no team scope is given). TeamPlayers maps
// a team name to its members for teams[<name>] accesses. TeamOrder lists team
// names in a deterministic order, used to expand teams[*].
type EvalContext struct {
	Players     []core.Player
	TeamPlayers map[string][]core.Player
	TeamOrder   []string
}

// Eval evaluates a parsed expression node against ctx.
func Eval(n Node, ctx *EvalContext) (Value, error) {
	switch v := n.(type) {
	case NumberLit:
		return Number(v.V), nil
	case StringLit:
		return String(v.V), nil
	case PlayerAccess:
		return evalPlayerAccess(v, ctx)
	case FuncCall:
		return evalFunc(v, ctx)
	}
	return Value{}, fmt.Errorf("expr: unknown node %T", n)
}

func evalPlayerAccess(pa PlayerAccess, ctx *EvalContext) (Value, error) {
	if !pa.grouped() {
		players, err := selectPlayers(pa, ctx)
		if err != nil {
			return Value{}, err
		}
		return collect(players, pa)
	}

	// Grouped: produce one sub-list per team.
	teams := pa.Teams
	if pa.AllTeams {
		teams = ctx.TeamOrder
	}
	out := make([]Value, 0, len(teams))
	for _, name := range teams {
		tp, ok := ctx.TeamPlayers[name]
		if !ok {
			return Value{}, fmt.Errorf("expr: unknown team %q", name)
		}
		v, err := collect(tp, pa)
		if err != nil {
			return Value{}, err
		}
		out = append(out, v)
	}
	return ListOf(out), nil
}

func selectPlayers(pa PlayerAccess, ctx *EvalContext) ([]core.Player, error) {
	if len(pa.Teams) == 0 {
		return ctx.Players, nil
	}
	name := pa.Teams[0]
	tp, ok := ctx.TeamPlayers[name]
	if !ok {
		return nil, fmt.Errorf("expr: unknown team %q", name)
	}
	return tp, nil
}

// collect builds a Value from each player's selected data. The shape depends on
// the attribute kind:
//
//	(no attr)           -> StringList of player IDs
//	number              -> list of Number (one per player)
//	string              -> list of String
//	string_list         -> list of (list of String) (one sublist per player)
//	string_number_map   -> list of Number for the indexed key, or error if no
//	                       key was supplied
func collect(players []core.Player, pa PlayerAccess) (Value, error) {
	if pa.Attr == "" {
		ids := make([]string, 0, len(players))
		for _, pl := range players {
			ids = append(ids, pl.ID)
		}
		return StringList(ids), nil
	}

	if len(players) == 0 {
		return ListOf(nil), nil
	}

	var firstKind core.AttributeKind
	for _, pl := range players {
		if a, ok := pl.Attributes[pa.Attr]; ok {
			firstKind = a.Kind
			break
		}
	}

	switch firstKind {
	case core.AttrNumber:
		out := make([]float64, 0, len(players))
		for _, pl := range players {
			if a, ok := pl.Attributes[pa.Attr]; ok {
				out = append(out, a.N)
			}
		}
		return NumberList(out), nil
	case core.AttrString:
		out := make([]string, 0, len(players))
		for _, pl := range players {
			if a, ok := pl.Attributes[pa.Attr]; ok {
				out = append(out, a.S)
			}
		}
		return StringList(out), nil
	case core.AttrStringList:
		out := make([]Value, 0, len(players))
		for _, pl := range players {
			if a, ok := pl.Attributes[pa.Attr]; ok {
				out = append(out, StringList(a.SL))
			}
		}
		return ListOf(out), nil
	case core.AttrStringNumberMap:
		if !pa.HasIndex {
			return Value{}, fmt.Errorf("expr: attribute %q is a map and requires [key] index", pa.Attr)
		}
		out := make([]float64, 0, len(players))
		for _, pl := range players {
			if a, ok := pl.Attributes[pa.Attr]; ok {
				if v, ok2 := a.SDM[pa.MapKey]; ok2 {
					out = append(out, v)
				}
			}
		}
		return NumberList(out), nil
	}
	return ListOf(nil), nil
}

func evalFunc(fc FuncCall, ctx *EvalContext) (Value, error) {
	arg, err := Eval(fc.Arg, ctx)
	if err != nil {
		return Value{}, err
	}
	switch fc.Name {
	case "flatten":
		return flatten(arg), nil
	case "count":
		return count(arg), nil
	case "set_intersection":
		return setIntersection(arg)
	case "avg":
		return reduceNumbers(arg, reduceAvg)
	case "sum":
		return reduceNumbers(arg, reduceSum)
	case "min":
		return reduceNumbers(arg, reduceMin)
	case "max":
		return reduceNumbers(arg, reduceMax)
	case "median":
		return reduceNumbers(arg, reduceMedian)
	case "stddev":
		return reduceNumbers(arg, reduceStddev)
	}
	return Value{}, fmt.Errorf("expr: unknown function %q", fc.Name)
}

// reduceNumbers applies a scalar reducer to a numeric value. If the value is a
// list of scalars it reduces directly; if it is a list of lists it maps the
// reducer over each sublist, producing a list of results (FlexMatch's
// per-sublist aggregation semantics).
func reduceNumbers(v Value, reduce func([]float64) Value) (Value, error) {
	switch v.Kind {
	case KindNone:
		return Value{Kind: KindNone}, nil
	case KindNumber:
		return reduce([]float64{v.Num}), nil
	case KindList:
		if isListOfLists(v) {
			out := make([]Value, len(v.List))
			for i, e := range v.List {
				r, err := reduceNumbers(e, reduce)
				if err != nil {
					return Value{}, err
				}
				out[i] = r
			}
			return ListOf(out), nil
		}
		nums := make([]float64, 0, len(v.List))
		for _, e := range v.List {
			n, ok := e.AsNumber()
			if !ok {
				return Value{}, fmt.Errorf("expr: numeric aggregation on non-number %v", e.Kind)
			}
			nums = append(nums, n)
		}
		return reduce(nums), nil
	}
	return Value{}, fmt.Errorf("expr: numeric aggregation on %v", v.Kind)
}

func reduceSum(nums []float64) Value {
	var s float64
	for _, n := range nums {
		s += n
	}
	return Number(s)
}

func reduceAvg(nums []float64) Value {
	if len(nums) == 0 {
		return Value{Kind: KindNone}
	}
	var s float64
	for _, n := range nums {
		s += n
	}
	return Number(s / float64(len(nums)))
}

func reduceMin(nums []float64) Value {
	if len(nums) == 0 {
		return Value{Kind: KindNone}
	}
	m := nums[0]
	for _, n := range nums[1:] {
		if n < m {
			m = n
		}
	}
	return Number(m)
}

func reduceMax(nums []float64) Value {
	if len(nums) == 0 {
		return Value{Kind: KindNone}
	}
	m := nums[0]
	for _, n := range nums[1:] {
		if n > m {
			m = n
		}
	}
	return Number(m)
}

func reduceMedian(nums []float64) Value {
	if len(nums) == 0 {
		return Value{Kind: KindNone}
	}
	s := append([]float64(nil), nums...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return Number(s[n/2])
	}
	return Number((s[n/2-1] + s[n/2]) / 2)
}

func reduceStddev(nums []float64) Value {
	if len(nums) == 0 {
		return Value{Kind: KindNone}
	}
	var mean float64
	for _, n := range nums {
		mean += n
	}
	mean /= float64(len(nums))
	var sq float64
	for _, n := range nums {
		d := n - mean
		sq += d * d
	}
	return Number(math.Sqrt(sq / float64(len(nums))))
}

// count returns the number of elements in a list. For a list of lists it
// returns the per-sublist counts as a list.
func count(v Value) Value {
	if v.Kind != KindList {
		return Number(1)
	}
	if isListOfLists(v) {
		out := make([]Value, len(v.List))
		for i, e := range v.List {
			out[i] = count(e)
		}
		return ListOf(out)
	}
	return Number(float64(len(v.List)))
}

// flatten turns a list of lists into a single list by concatenating one level.
func flatten(v Value) Value {
	if v.Kind != KindList {
		return v
	}
	var out []Value
	for _, e := range v.List {
		if e.Kind == KindList {
			out = append(out, e.List...)
		} else {
			out = append(out, e)
		}
	}
	return ListOf(out)
}

// setIntersection returns the strings common to every string list in a
// List<List<string>>. If the argument is nested an extra level (per team), it
// maps over each sublist.
func setIntersection(v Value) (Value, error) {
	if v.Kind != KindList {
		return Value{}, fmt.Errorf("expr: set_intersection requires a list of string lists")
	}
	// Per-team nesting: List<List<List<string>>> -> map over teams.
	if len(v.List) > 0 && isListOfLists(v.List[0]) {
		out := make([]Value, len(v.List))
		for i, e := range v.List {
			r, err := setIntersection(e)
			if err != nil {
				return Value{}, err
			}
			out[i] = r
		}
		return ListOf(out), nil
	}
	if len(v.List) == 0 {
		return StringList(nil), nil
	}
	first, ok := v.List[0].FlattenStrings()
	if !ok {
		return Value{}, fmt.Errorf("expr: set_intersection requires string lists")
	}
	acc := make(map[string]struct{}, len(first))
	for _, s := range first {
		acc[s] = struct{}{}
	}
	for _, row := range v.List[1:] {
		strs, ok := row.FlattenStrings()
		if !ok {
			return Value{}, fmt.Errorf("expr: set_intersection requires string lists")
		}
		next := make(map[string]struct{}, len(strs))
		for _, s := range strs {
			if _, ok := acc[s]; ok {
				next[s] = struct{}{}
			}
		}
		acc = next
	}
	out := make([]string, 0, len(acc))
	for _, s := range first {
		if _, ok := acc[s]; ok {
			out = append(out, s)
			delete(acc, s)
		}
	}
	return StringList(out), nil
}
