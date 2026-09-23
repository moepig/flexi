package rule

import (
	"fmt"

	"github.com/moepig/flexi/internal/expr"
	"github.com/moepig/flexi/internal/ruleset"
)

// Records the teams whose minimum size must be reached before a
// count rule can be evaluated during placement.
type Dependency struct {
	Count      bool
	All        bool
	Teams      map[string]struct{}
	DeferFinal bool
}

// Contains the top-level evaluators in declaration order and their
// placement dependencies. Evaluators are immutable after construction.
type Set struct {
	Evaluators   []Evaluator
	Dependencies map[string]Dependency
}

// Validates and constructs every rule, including compound references.
func BuildSet(rs *ruleset.RuleSet) (*Set, error) {
	byName := make(map[string]*ruleset.Rule, len(rs.Rules))
	for i := range rs.Rules {
		byName[rs.Rules[i].Name] = &rs.Rules[i]
	}
	validTeams := make(map[string]struct{})
	attrTypes := make(map[string]string)
	for _, attr := range rs.PlayerAttributes {
		attrTypes[attr.Name] = attr.Type
	}
	for _, team := range rs.Teams {
		validTeams[team.Name] = struct{}{}
		for _, name := range ruleset.ExpandedTeamNames(team) {
			validTeams[name] = struct{}{}
		}
	}
	built := make(map[string]Evaluator, len(rs.Rules))
	states := make(map[string]uint8, len(rs.Rules))
	deps := make(map[string]Dependency, len(rs.Rules))
	referenced := make(map[string]struct{})
	var visit func(string) error
	visit = func(name string) error {
		if states[name] == 2 {
			return nil
		}
		if states[name] == 1 {
			return fmt.Errorf("rule %q has a compound cycle", name)
		}
		r := byName[name]
		if r == nil {
			return fmt.Errorf("unknown rule %q", name)
		}
		states[name] = 1
		dep := Dependency{Teams: make(map[string]struct{})}
		if r.Type == ruleset.RuleCompound {
			node, err := ruleset.ParseCompound(r.Statement)
			if err != nil {
				return fmt.Errorf("rule %q statement: %w", name, err)
			}
			for _, child := range node.RuleNames() {
				if err := visit(child); err != nil {
					return err
				}
				referenced[child] = struct{}{}
				mergeDependency(&dep, deps[child])
			}
		}
		ev, err := Build(r, built)
		if err != nil {
			return fmt.Errorf("rule %q: %w", name, err)
		}
		if r.Type != ruleset.RuleCompound {
			for _, n := range evaluatorNodes(ev) {
				if err := expr.Validate(n, validTeams, attrTypes); err != nil {
					return fmt.Errorf("rule %q expression: %w", name, err)
				}
				collectDependency(n, &dep)
			}
			if c, ok := ev.(*collection); ok && c.partialEligible() {
				dep.DeferFinal = true
			}
		}
		built[name], deps[name], states[name] = ev, dep, 2
		return nil
	}
	for _, r := range rs.Rules {
		if err := visit(r.Name); err != nil {
			return nil, err
		}
	}
	set := &Set{Dependencies: deps}
	for _, r := range rs.Rules {
		if _, isChild := referenced[r.Name]; !isChild {
			set.Evaluators = append(set.Evaluators, built[r.Name])
		}
	}
	return set, nil
}

func evaluatorNodes(ev Evaluator) []expr.Node {
	switch e := ev.(type) {
	case *comparison:
		return appendNode(e.measures, e.ref.Node)
	case *distance:
		return appendNode(e.measures, e.ref)
	case *collection:
		return appendNode(e.measures, e.ref.Node)
	}
	return nil
}

func appendNode(nodes []expr.Node, extra expr.Node) []expr.Node {
	if extra == nil {
		return nodes
	}
	return append(append([]expr.Node(nil), nodes...), extra)
}

func collectDependency(n expr.Node, dep *Dependency) {
	switch v := n.(type) {
	case expr.FuncCall:
		if v.Name == "count" {
			dep.Count = true
			collectScope(v.Arg, dep)
		}
		collectDependency(v.Arg, dep)
	}
}

func collectScope(n expr.Node, dep *Dependency) {
	switch v := n.(type) {
	case expr.FuncCall:
		collectScope(v.Arg, dep)
	case expr.PlayerAccess:
		if v.AllTeams || len(v.Teams) == 0 {
			dep.All = true
		}
		for _, name := range v.Teams {
			dep.Teams[name] = struct{}{}
		}
	}
}

func mergeDependency(dst *Dependency, src Dependency) {
	dst.Count = dst.Count || src.Count
	dst.All = dst.All || src.All
	dst.DeferFinal = dst.DeferFinal || src.DeferFinal
	for name := range src.Teams {
		dst.Teams[name] = struct{}{}
	}
}
