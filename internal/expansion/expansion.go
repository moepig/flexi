// Package expansion applies time-driven loosening of rule set values, as
// declared by the FlexMatch "expansions" block.
package expansion

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/moepig/flexi/internal/ruleset"
)

// Apply returns a copy of rs with each expansion's matching step (largest
// waitTimeSeconds <= elapsed seconds) applied to its target. The original is
// not modified.
func Apply(rs *ruleset.RuleSet, elapsed time.Duration) (*ruleset.RuleSet, error) {
	out := cloneRuleSet(rs)
	secs := int(elapsed / time.Second)
	for _, exp := range rs.Expansions {
		step := pickStep(exp.Steps, secs)
		if step == nil {
			continue
		}
		if err := applyTarget(out, exp.Target, step.Value); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func pickStep(steps []ruleset.ExpansionStep, secs int) *ruleset.ExpansionStep {
	var chosen *ruleset.ExpansionStep
	for i := range steps {
		if steps[i].WaitTimeSeconds <= secs {
			chosen = &steps[i]
		}
	}
	return chosen
}

func applyTarget(rs *ruleset.RuleSet, target string, value json.RawMessage) error {
	if strings.HasPrefix(target, "algorithm.") {
		return setAlgorithmField(&rs.Algorithm, target[len("algorithm."):], value)
	}

	// A component target reads "<component>[<name>,...].<field>".
	comp, rest, ok := strings.Cut(target, "[")
	if !ok {
		return fmt.Errorf("expansion: invalid target %q", target)
	}
	nameList, field, ok := strings.Cut(rest, "].")
	if !ok {
		return fmt.Errorf("expansion: invalid target %q", target)
	}
	names := splitNames(nameList)
	if len(names) == 0 {
		return fmt.Errorf("expansion: target %q names no elements", target)
	}

	switch comp {
	case "rules":
		return applyNamed(rs.Rules, names, target, "rule",
			func(r *ruleset.Rule) string { return r.Name },
			func(r *ruleset.Rule) error { return setRuleField(r, field, value) })
	case "teams":
		return applyNamed(rs.Teams, names, target, "team",
			func(t *ruleset.Team) string { return t.Name },
			func(t *ruleset.Team) error { return setTeamField(t, field, value) })
	}
	return fmt.Errorf("expansion: unsupported target %q", target)
}

// applyNamed calls set on every element of items whose name matches one of
// names, reporting an error naming the unmatched element (as kind, e.g. "rule")
// if any name matches nothing. Elements are addressed by index so set mutates
// the slice in place.
func applyNamed[T any](items []T, names []string, target, kind string, nameOf func(*T) string, set func(*T) error) error {
	for _, name := range names {
		found := false
		for i := range items {
			if nameOf(&items[i]) != name {
				continue
			}
			if err := set(&items[i]); err != nil {
				return err
			}
			found = true
		}
		if !found {
			return fmt.Errorf("expansion: unknown %s %q in target %q", kind, name, target)
		}
	}
	return nil
}

func splitNames(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func setTeamField(t *ruleset.Team, field string, value json.RawMessage) error {
	var i int
	if err := json.Unmarshal(value, &i); err != nil {
		return fmt.Errorf("expansion: team field %q requires an integer: %v", field, err)
	}
	switch field {
	case "minPlayers":
		t.MinPlayers = i
	case "maxPlayers":
		t.MaxPlayers = i
	default:
		return fmt.Errorf("expansion: unsupported team field %q", field)
	}
	return nil
}

func setRuleField(r *ruleset.Rule, field string, value json.RawMessage) error {
	switch field {
	case "maxDistance":
		return jsonNumberPtr(value, &r.MaxDistance)
	case "minDistance":
		return jsonNumberPtr(value, &r.MinDistance)
	case "maxLatency":
		return jsonIntPtr(value, &r.MaxLatency)
	case "minCount":
		return jsonIntPtr(value, &r.MinCount)
	case "maxCount":
		return jsonIntPtr(value, &r.MaxCount)
	case "referenceValue":
		r.ReferenceValue = slices.Clone(value)
		return nil
	}
	return fmt.Errorf("expansion: unsupported rule field %q", field)
}

func setAlgorithmField(a *ruleset.Algorithm, field string, value json.RawMessage) error {
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		return fmt.Errorf("expansion: algorithm.%s requires string: %v", field, err)
	}
	switch field {
	case "strategy":
		a.Strategy = s
	case "batchingPreference":
		a.BatchingPreference = s
	case "balancedAttribute":
		a.BalancedAttribute = s
	case "backfillPriority":
		a.BackfillPriority = s
	case "expansionAgeSelection":
		a.ExpansionAgeSelection = s
	default:
		return fmt.Errorf("expansion: unsupported algorithm field %q", field)
	}
	return nil
}

func jsonNumberPtr(value json.RawMessage, dst **float64) error {
	// Accept string-encoded numbers too, matching ruleset.Rule's maxDistance /
	// minDistance parsing (the FlexMatch docs use both forms).
	f, err := ruleset.ParseNumber(value)
	if err != nil {
		return err
	}
	*dst = &f
	return nil
}

func jsonIntPtr(value json.RawMessage, dst **int) error {
	var i int
	if err := json.Unmarshal(value, &i); err != nil {
		return err
	}
	*dst = &i
	return nil
}

func cloneRuleSet(rs *ruleset.RuleSet) *ruleset.RuleSet {
	cp := *rs
	cp.Teams = slices.Clone(rs.Teams)
	cp.PlayerAttributes = slices.Clone(rs.PlayerAttributes)
	cp.Rules = make([]ruleset.Rule, len(rs.Rules))
	for i, r := range rs.Rules {
		cp.Rules[i] = cloneRule(r)
	}
	cp.Expansions = slices.Clone(rs.Expansions)
	return &cp
}

// cloneRule deep-copies the fields an expansion can overwrite, so applying one
// to the copy never mutates the caller's rule set.
func cloneRule(r ruleset.Rule) ruleset.Rule {
	out := r
	out.MaxDistance = clonePtr(r.MaxDistance)
	out.MinDistance = clonePtr(r.MinDistance)
	out.MaxLatency = clonePtr(r.MaxLatency)
	out.MinCount = clonePtr(r.MinCount)
	out.MaxCount = clonePtr(r.MaxCount)
	out.ReferenceValue = slices.Clone(r.ReferenceValue)
	out.Measurements = slices.Clone(r.Measurements)
	return out
}

// clonePtr returns a pointer to a copy of *p, or nil when p is nil.
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
