package rule

import (
	"fmt"

	"github.com/moepig/flexi/internal/ruleset"
)

type compound struct {
	name      string
	condition string
	children  []Evaluator
}

func buildCompound(r *ruleset.Rule, others map[string]Evaluator) (Evaluator, error) {
	if r.Statement == nil {
		return nil, fmt.Errorf("compound %q: statement required", r.Name)
	}
	c := &compound{name: r.Name, condition: r.Statement.Condition}
	for _, n := range r.Statement.Rules {
		ev, ok := others[n]
		if !ok {
			return nil, fmt.Errorf("compound %q: unknown child rule %q", r.Name, n)
		}
		c.children = append(c.children, ev)
	}
	if c.condition == "not" && len(c.children) != 1 {
		return nil, fmt.Errorf("compound %q: not requires exactly one child", r.Name)
	}
	return c, nil
}

func (c *compound) Name() string { return c.name }

func (c *compound) Evaluate(cand *Candidate) (bool, error) {
	switch c.condition {
	case "and":
		for _, child := range c.children {
			ok, err := child.Evaluate(cand)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	case "or":
		for _, child := range c.children {
			ok, err := child.Evaluate(cand)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	case "not":
		ok, err := c.children[0].Evaluate(cand)
		if err != nil {
			return false, err
		}
		return !ok, nil
	}
	return false, fmt.Errorf("compound %q: unknown condition %q", c.name, c.condition)
}
