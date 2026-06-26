package rule

import (
	"fmt"

	"github.com/moepig/flexi/internal/ruleset"
)

type compound struct {
	name string
	node *ruleset.CompoundNode
	refs map[string]Evaluator
}

func buildCompound(r *ruleset.Rule, others map[string]Evaluator) (Evaluator, error) {
	if r.Statement == "" {
		return nil, fmt.Errorf("compound %q: statement required", r.Name)
	}
	node, err := ruleset.ParseCompound(r.Statement)
	if err != nil {
		return nil, fmt.Errorf("compound %q: %v", r.Name, err)
	}
	for _, n := range node.RuleNames() {
		if _, ok := others[n]; !ok {
			return nil, fmt.Errorf("compound %q: unknown child rule %q", r.Name, n)
		}
	}
	return &compound{name: r.Name, node: node, refs: others}, nil
}

func (c *compound) Name() string { return c.name }

func (c *compound) Evaluate(cand *Candidate) (bool, error) {
	return c.eval(c.node, cand)
}

func (c *compound) eval(n *ruleset.CompoundNode, cand *Candidate) (bool, error) {
	if n.Op == "" {
		ev, ok := c.refs[n.Rule]
		if !ok {
			return false, fmt.Errorf("compound %q: unknown child rule %q", c.name, n.Rule)
		}
		return ev.Evaluate(cand)
	}
	switch n.Op {
	case "and":
		for _, a := range n.Args {
			ok, err := c.eval(a, cand)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	case "or":
		for _, a := range n.Args {
			ok, err := c.eval(a, cand)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	case "not":
		ok, err := c.eval(n.Args[0], cand)
		if err != nil {
			return false, err
		}
		return !ok, nil
	case "xor":
		trueCount := 0
		for _, a := range n.Args {
			ok, err := c.eval(a, cand)
			if err != nil {
				return false, err
			}
			if ok {
				trueCount++
			}
		}
		return trueCount == 1, nil
	}
	return false, fmt.Errorf("compound %q: unknown operator %q", c.name, n.Op)
}
