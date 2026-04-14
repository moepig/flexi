package rule

import (
	"fmt"

	"github.com/moepig/flexi/internal/core"
	"github.com/moepig/flexi/internal/ruleset"
)

type batchDistance struct {
	name    string
	attr    string
	maxDist float64
}

func buildBatchDistance(r *ruleset.Rule) (Evaluator, error) {
	if r.MaxAttributeDistance == nil {
		return nil, fmt.Errorf("batchDistance %q: maxAttributeDistance required", r.Name)
	}
	return &batchDistance{name: r.Name, attr: r.BatchAttribute, maxDist: *r.MaxAttributeDistance}, nil
}

func (b *batchDistance) Name() string { return b.name }

func (b *batchDistance) Evaluate(c *Candidate) (bool, error) {
	if len(c.Players) < 2 {
		return true, nil
	}
	min, max := 0.0, 0.0
	first := true
	for _, p := range c.Players {
		a, ok := p.Attributes[b.attr]
		if !ok || a.Kind != core.AttrNumber {
			return false, fmt.Errorf("batchDistance %q: player %q lacks numeric attr %q", b.name, p.ID, b.attr)
		}
		if first {
			min, max = a.N, a.N
			first = false
			continue
		}
		if a.N < min {
			min = a.N
		}
		if a.N > max {
			max = a.N
		}
	}
	return (max - min) <= b.maxDist, nil
}
