// Package expansion applies time-driven loosening of rule set values, as
// declared by the FlexMatch "expansions" block.
package expansion

import (
	"encoding/json"
	"fmt"
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

	open := strings.Index(target, "[")
	closeIdx := strings.Index(target, "]")
	if open < 0 || closeIdx < open || !strings.HasPrefix(target[closeIdx:], "].") {
		return fmt.Errorf("expansion: invalid target %q", target)
	}
	comp := target[:open]
	names := splitNames(target[open+1 : closeIdx])
	field := target[closeIdx+2:]
	if len(names) == 0 {
		return fmt.Errorf("expansion: target %q names no elements", target)
	}

	switch comp {
	case "rules":
		for _, name := range names {
			found := false
			for i := range rs.Rules {
				if rs.Rules[i].Name == name {
					if err := setRuleField(&rs.Rules[i], field, value); err != nil {
						return err
					}
					found = true
				}
			}
			if !found {
				return fmt.Errorf("expansion: unknown rule %q in target %q", name, target)
			}
		}
		return nil
	case "teams":
		for _, name := range names {
			found := false
			for i := range rs.Teams {
				if rs.Teams[i].Name == name {
					if err := setTeamField(&rs.Teams[i], field, value); err != nil {
						return err
					}
					found = true
				}
			}
			if !found {
				return fmt.Errorf("expansion: unknown team %q in target %q", name, target)
			}
		}
		return nil
	}
	return fmt.Errorf("expansion: unsupported target %q", target)
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
		r.ReferenceValue = append(json.RawMessage(nil), value...)
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
	var f float64
	if err := json.Unmarshal(value, &f); err != nil {
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
	cp.Teams = append([]ruleset.Team(nil), rs.Teams...)
	cp.PlayerAttributes = append([]ruleset.PlayerAttribute(nil), rs.PlayerAttributes...)
	cp.Rules = make([]ruleset.Rule, len(rs.Rules))
	for i, r := range rs.Rules {
		cp.Rules[i] = cloneRule(r)
	}
	cp.Expansions = append([]ruleset.Expansion(nil), rs.Expansions...)
	return &cp
}

func cloneRule(r ruleset.Rule) ruleset.Rule {
	out := r
	if r.MaxDistance != nil {
		v := *r.MaxDistance
		out.MaxDistance = &v
	}
	if r.MinDistance != nil {
		v := *r.MinDistance
		out.MinDistance = &v
	}
	if r.MaxLatency != nil {
		v := *r.MaxLatency
		out.MaxLatency = &v
	}
	if r.MinCount != nil {
		v := *r.MinCount
		out.MinCount = &v
	}
	if r.MaxCount != nil {
		v := *r.MaxCount
		out.MaxCount = &v
	}
	if r.ReferenceValue != nil {
		out.ReferenceValue = append(json.RawMessage(nil), r.ReferenceValue...)
	}
	if r.Measurements != nil {
		out.Measurements = append([]string(nil), r.Measurements...)
	}
	return out
}
