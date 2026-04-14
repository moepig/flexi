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
	switch {
	case strings.HasPrefix(target, "rules["):
		end := strings.Index(target, "]")
		if end < 0 || !strings.HasPrefix(target[end:], "].") {
			return fmt.Errorf("expansion: invalid target %q", target)
		}
		name := target[len("rules[") : end]
		field := target[end+2:]
		for i := range rs.Rules {
			if rs.Rules[i].Name == name {
				return setRuleField(&rs.Rules[i], field, value)
			}
		}
		return fmt.Errorf("expansion: unknown rule %q in target %q", name, target)
	case strings.HasPrefix(target, "algorithm."):
		return setAlgorithmField(&rs.Algorithm, target[len("algorithm."):], value)
	}
	return fmt.Errorf("expansion: unsupported target %q", target)
}

func setRuleField(r *ruleset.Rule, field string, value json.RawMessage) error {
	switch field {
	case "maxDistance":
		return jsonNumberPtr(value, &r.MaxDistance)
	case "minDistance":
		return jsonNumberPtr(value, &r.MinDistance)
	case "maxAttributeDistance":
		return jsonNumberPtr(value, &r.MaxAttributeDistance)
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
	if r.MaxAttributeDistance != nil {
		v := *r.MaxAttributeDistance
		out.MaxAttributeDistance = &v
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
	if r.Statement != nil {
		s := *r.Statement
		s.Rules = append([]string(nil), r.Statement.Rules...)
		out.Statement = &s
	}
	if r.ReferenceValue != nil {
		out.ReferenceValue = append(json.RawMessage(nil), r.ReferenceValue...)
	}
	if r.Measurements != nil {
		out.Measurements = append([]string(nil), r.Measurements...)
	}
	return out
}
