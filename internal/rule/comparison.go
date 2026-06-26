package rule

import (
	"fmt"

	"github.com/moepig/flexi/internal/expr"
	"github.com/moepig/flexi/internal/ruleset"
)

type comparison struct {
	name     string
	measures []expr.Node
	ref      parsedRef
	op       string
	partyAgg string
	// acrossPlayers selects the "compare across players" form (no referenceValue,
	// only = or !=): every measured value must be equal (=) or all distinct (!=).
	acrossPlayers bool
}

func buildComparison(r *ruleset.Rule) (Evaluator, error) {
	ms, err := parseMeasurements(r.Measurements)
	if err != nil {
		return nil, err
	}
	ref, err := parseRef(r.ReferenceValue)
	if err != nil {
		return nil, err
	}
	c := &comparison{name: r.Name, measures: ms, ref: ref, op: r.Operation, partyAgg: r.PartyAggregation}
	if ref.Node == nil {
		// "Compare across players" form: FlexMatch allows only = or != when no
		// referenceValue is supplied.
		if r.Operation != "=" && r.Operation != "!=" {
			return nil, fmt.Errorf("comparison %q: referenceValue required for operation %q", r.Name, r.Operation)
		}
		c.acrossPlayers = true
	}
	return c, nil
}

func (c *comparison) Name() string { return c.name }

func (c *comparison) Evaluate(cand *Candidate) (bool, error) {
	// partyAggregation defaults to "avg" per the FlexMatch spec; aggregateCandidate
	// applies that default and is a no-op when the candidate carries no parties.
	cand = aggregateCandidate(cand, c.partyAgg)
	ctx := cand.evalContext()
	if c.acrossPlayers {
		return c.evaluateAcrossPlayers(ctx)
	}
	refV, err := expr.Eval(c.ref.Node, ctx)
	if err != nil {
		return false, err
	}
	if refV.Kind == expr.KindNone {
		return true, nil
	}
	for _, m := range c.measures {
		v, err := expr.Eval(m, ctx)
		if err != nil {
			return false, err
		}
		if v.Kind == expr.KindNone {
			continue
		}
		ok, err := compareValues(v, refV, c.op)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// evaluateAcrossPlayers implements the referenceValue-less = / != form: each
// measurement is flattened to the values of all players, then required to be all
// equal (=) or all distinct (!=).
func (c *comparison) evaluateAcrossPlayers(ctx *expr.EvalContext) (bool, error) {
	for _, m := range c.measures {
		v, err := expr.Eval(m, ctx)
		if err != nil {
			return false, err
		}
		if nums, ok := v.FlattenNumbers(); ok {
			if !crossCompareNum(nums, c.op) {
				return false, nil
			}
			continue
		}
		if strs, ok := v.FlattenStrings(); ok {
			if !crossCompareStr(strs, c.op) {
				return false, nil
			}
			continue
		}
		return false, fmt.Errorf("comparison %q: measurement is neither numeric nor string", c.name)
	}
	return true, nil
}

func crossCompareNum(vals []float64, op string) bool {
	if op == "=" {
		for _, v := range vals {
			if v != vals[0] {
				return false
			}
		}
		return true
	}
	seen := make(map[float64]struct{}, len(vals))
	for _, v := range vals {
		if _, dup := seen[v]; dup {
			return false
		}
		seen[v] = struct{}{}
	}
	return true
}

func crossCompareStr(vals []string, op string) bool {
	if op == "=" {
		for _, v := range vals {
			if v != vals[0] {
				return false
			}
		}
		return true
	}
	seen := make(map[string]struct{}, len(vals))
	for _, v := range vals {
		if _, dup := seen[v]; dup {
			return false
		}
		seen[v] = struct{}{}
	}
	return true
}

func compareValues(a, b expr.Value, op string) (bool, error) {
	if nums, ok := a.FlattenNumbers(); ok {
		bn, ok := b.AsNumber()
		if !ok {
			if bnums, ok2 := b.FlattenNumbers(); ok2 && len(bnums) == 1 {
				bn = bnums[0]
				ok = true
			}
		}
		if !ok {
			return false, fmt.Errorf("comparison: rhs not numeric (got kind %v)", b.Kind)
		}
		for _, n := range nums {
			if !cmpNumber(n, bn, op) {
				return false, nil
			}
		}
		return true, nil
	}
	if strs, ok := a.FlattenStrings(); ok {
		bs, ok := b.AsString()
		if !ok {
			return false, fmt.Errorf("comparison: rhs not string")
		}
		for _, s := range strs {
			if !cmpString(s, bs, op) {
				return false, nil
			}
		}
		return true, nil
	}
	return false, fmt.Errorf("comparison: unsupported lhs kind %v", a.Kind)
}

func cmpNumber(a, b float64, op string) bool {
	switch op {
	case "=":
		return a == b
	case "!=":
		return a != b
	case "<":
		return a < b
	case "<=":
		return a <= b
	case ">":
		return a > b
	case ">=":
		return a >= b
	}
	return false
}

func cmpString(a, b string, op string) bool {
	switch op {
	case "=":
		return a == b
	case "!=":
		return a != b
	}
	return false
}
