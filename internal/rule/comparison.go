package rule

import (
	"fmt"

	"github.com/moepig/flexi/internal/expr"
	"github.com/moepig/flexi/internal/ruleset"
)

type comparison struct {
	name      string
	measures  []expr.Node
	ref       parsedRef
	op        string
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
	if ref.Node == nil {
		return nil, fmt.Errorf("comparison %q: referenceValue required", r.Name)
	}
	return &comparison{name: r.Name, measures: ms, ref: ref, op: r.Operation}, nil
}

func (c *comparison) Name() string { return c.name }

func (c *comparison) Evaluate(cand *Candidate) (bool, error) {
	ctx := cand.evalContext()
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
