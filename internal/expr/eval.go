package expr

import (
	"fmt"

	"github.com/moepig/flexi/internal/core"
)

// EvalContext supplies the player population an expression operates over.
//
// Players is the full pool (used when a Scope is empty or "*"). TeamPlayers
// maps a team name to its members for `teams[<name>]` accesses.
type EvalContext struct {
	Players     []core.Player
	TeamPlayers map[string][]core.Player
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
	players, err := selectPlayers(pa.Scope, ctx)
	if err != nil {
		return Value{}, err
	}
	return collect(players, pa)
}

func selectPlayers(scope string, ctx *EvalContext) ([]core.Player, error) {
	if scope == "" || scope == "*" {
		return ctx.Players, nil
	}
	tp, ok := ctx.TeamPlayers[scope]
	if !ok {
		return nil, fmt.Errorf("expr: unknown team %q", scope)
	}
	return tp, nil
}

// collect builds a Value from each player's named attribute. The shape of the
// returned Value depends on the underlying attribute kind:
//
//	number              -> NumberList (one entry per player)
//	string              -> StringList
//	string_list         -> StringMatrix
//	string_number_map   -> NumberList (taking the indexed key) or error if
//	                       no key was supplied
func collect(players []core.Player, pa PlayerAccess) (Value, error) {
	if len(players) == 0 {
		return Value{Kind: KindNumberList}, nil
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
		out := make([][]string, 0, len(players))
		for _, pl := range players {
			if a, ok := pl.Attributes[pa.Attr]; ok {
				out = append(out, a.SL)
			}
		}
		return Value{Kind: KindStringMatrix, SM: out}, nil
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
	return Value{Kind: KindNumberList}, nil
}

func evalFunc(fc FuncCall, ctx *EvalContext) (Value, error) {
	arg, err := Eval(fc.Arg, ctx)
	if err != nil {
		return Value{}, err
	}
	switch fc.Name {
	case "flatten":
		if nums, ok := arg.FlattenNumbers(); ok {
			return NumberList(nums), nil
		}
		if strs, ok := arg.FlattenStrings(); ok {
			return StringList(strs), nil
		}
		return Value{}, fmt.Errorf("expr: flatten unsupported on %v", arg.Kind)
	case "avg":
		nums, ok := arg.FlattenNumbers()
		if !ok {
			return Value{}, fmt.Errorf("expr: avg requires numbers")
		}
		if len(nums) == 0 {
			return Value{Kind: KindNone}, nil
		}
		var s float64
		for _, n := range nums {
			s += n
		}
		return Number(s / float64(len(nums))), nil
	case "sum":
		nums, ok := arg.FlattenNumbers()
		if !ok {
			return Value{}, fmt.Errorf("expr: sum requires numbers")
		}
		var s float64
		for _, n := range nums {
			s += n
		}
		return Number(s), nil
	case "min":
		nums, ok := arg.FlattenNumbers()
		if !ok {
			return Value{}, fmt.Errorf("expr: min requires numbers")
		}
		if len(nums) == 0 {
			return Value{Kind: KindNone}, nil
		}
		m := nums[0]
		for _, n := range nums[1:] {
			if n < m {
				m = n
			}
		}
		return Number(m), nil
	case "max":
		nums, ok := arg.FlattenNumbers()
		if !ok {
			return Value{}, fmt.Errorf("expr: max requires numbers")
		}
		if len(nums) == 0 {
			return Value{Kind: KindNone}, nil
		}
		m := nums[0]
		for _, n := range nums[1:] {
			if n > m {
				m = n
			}
		}
		return Number(m), nil
	case "count":
		switch arg.Kind {
		case KindNumberList:
			return Number(float64(len(arg.NL))), nil
		case KindStringList:
			return Number(float64(len(arg.SL))), nil
		case KindStringMatrix:
			return Number(float64(len(arg.SM))), nil
		case KindNumberMatrix:
			return Number(float64(len(arg.NM))), nil
		}
		return Number(1), nil
	case "set_intersection":
		// intersection of every player's string list
		if arg.Kind != KindStringMatrix {
			if arg.Kind == KindStringList {
				return StringList(arg.SL), nil
			}
			return Value{}, fmt.Errorf("expr: set_intersection requires string lists per player")
		}
		if len(arg.SM) == 0 {
			return StringList(nil), nil
		}
		acc := make(map[string]struct{}, len(arg.SM[0]))
		for _, s := range arg.SM[0] {
			acc[s] = struct{}{}
		}
		for _, row := range arg.SM[1:] {
			next := make(map[string]struct{}, len(row))
			for _, s := range row {
				if _, ok := acc[s]; ok {
					next[s] = struct{}{}
				}
			}
			acc = next
		}
		out := make([]string, 0, len(acc))
		for s := range acc {
			out = append(out, s)
		}
		return StringList(out), nil
	}
	return Value{}, fmt.Errorf("expr: unknown function %q", fc.Name)
}
