package rule

import (
	"encoding/json"
	"fmt"

	"github.com/moepig/flexi/internal/expr"
	"github.com/moepig/flexi/internal/ruleset"
)

type collection struct {
	name     string
	measures []expr.Node
	op       string
	refStr   string
	refSet   []string
	minCount *int
	maxCount *int
}

func buildCollection(r *ruleset.Rule) (Evaluator, error) {
	ms, err := parseMeasurements(r.Measurements)
	if err != nil {
		return nil, err
	}
	c := &collection{
		name: r.Name, measures: ms, op: r.Operation,
		minCount: r.MinCount, maxCount: r.MaxCount,
	}
	if len(r.ReferenceValue) > 0 {
		switch r.ReferenceValue[0] {
		case '"':
			if err := json.Unmarshal(r.ReferenceValue, &c.refStr); err != nil {
				return nil, err
			}
		case '[':
			if err := json.Unmarshal(r.ReferenceValue, &c.refSet); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("collection %q: referenceValue must be string or array", r.Name)
		}
	}
	return c, nil
}

func (c *collection) Name() string { return c.name }

func (c *collection) Evaluate(cand *Candidate) (bool, error) {
	ctx := cand.evalContext()
	for _, m := range c.measures {
		v, err := expr.Eval(m, ctx)
		if err != nil {
			return false, err
		}
		set, ok := v.FlattenStrings()
		if !ok {
			return false, fmt.Errorf("collection %q: measurement is not a string set", c.name)
		}
		ok, err = c.evalOne(set)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (c *collection) evalOne(set []string) (bool, error) {
	switch c.op {
	case "contains":
		return contains(set, c.refStr), nil
	case "not_contains":
		return !contains(set, c.refStr), nil
	case "intersection":
		return intersectCount(set, c.refSet) > 0, nil
	case "reference_intersection_count":
		n := intersectCount(set, c.refSet)
		if c.minCount != nil && n < *c.minCount {
			return false, nil
		}
		if c.maxCount != nil && n > *c.maxCount {
			return false, nil
		}
		return true, nil
	}
	return false, fmt.Errorf("collection %q: unknown operation %q", c.name, c.op)
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func intersectCount(a, b []string) int {
	idx := make(map[string]struct{}, len(b))
	for _, s := range b {
		idx[s] = struct{}{}
	}
	n := 0
	for _, s := range a {
		if _, ok := idx[s]; ok {
			n++
		}
	}
	return n
}
