package rule

import (
	"fmt"
	"math"

	"github.com/moepig/flexi/internal/expr"
	"github.com/moepig/flexi/internal/ruleset"
)

type distance struct {
	name        string
	measures    []expr.Node
	ref         expr.Node
	maxDistance *float64
	minDistance *float64
}

func buildDistance(r *ruleset.Rule) (Evaluator, error) {
	ms, err := parseMeasurements(r.Measurements)
	if err != nil {
		return nil, err
	}
	ref, err := parseRef(r.ReferenceValue)
	if err != nil {
		return nil, err
	}
	if ref.Node == nil {
		return nil, fmt.Errorf("distance %q: referenceValue required", r.Name)
	}
	return &distance{
		name: r.Name, measures: ms, ref: ref.Node,
		maxDistance: r.MaxDistance, minDistance: r.MinDistance,
	}, nil
}

func (d *distance) Name() string { return d.name }

func (d *distance) Evaluate(c *Candidate) (bool, error) {
	ctx := c.evalContext()
	refV, err := expr.Eval(d.ref, ctx)
	if err != nil {
		return false, err
	}
	if refV.Kind == expr.KindNone {
		return true, nil
	}
	refN, ok := refV.AsNumber()
	if !ok {
		return false, fmt.Errorf("distance %q: reference is not a number", d.name)
	}
	for _, m := range d.measures {
		v, err := expr.Eval(m, ctx)
		if err != nil {
			return false, err
		}
		if v.Kind == expr.KindNone {
			continue
		}
		nums, ok := v.FlattenNumbers()
		if !ok {
			return false, fmt.Errorf("distance %q: measurement not numeric", d.name)
		}
		for _, n := range nums {
			diff := math.Abs(n - refN)
			if d.maxDistance != nil && diff > *d.maxDistance {
				return false, nil
			}
			if d.minDistance != nil && diff < *d.minDistance {
				return false, nil
			}
		}
	}
	return true, nil
}
