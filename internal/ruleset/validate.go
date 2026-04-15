package ruleset

import (
	"fmt"
	"strings"
)

// Validate performs semantic checks beyond JSON well-formedness.
func (rs *RuleSet) Validate() error {
	if len(rs.Teams) == 0 {
		return fmt.Errorf("%w: at least one team is required", ErrInvalidRuleSet)
	}

	teamNames := make(map[string]struct{}, len(rs.Teams))
	for i, t := range rs.Teams {
		if t.Name == "" {
			return fmt.Errorf("%w: teams[%d].name is required", ErrInvalidRuleSet, i)
		}
		if _, dup := teamNames[t.Name]; dup {
			return fmt.Errorf("%w: duplicate team name %q", ErrInvalidRuleSet, t.Name)
		}
		teamNames[t.Name] = struct{}{}
		if t.MaxPlayers <= 0 {
			return fmt.Errorf("%w: team %q maxPlayers must be > 0", ErrInvalidRuleSet, t.Name)
		}
		if t.MinPlayers < 0 || t.MinPlayers > t.MaxPlayers {
			return fmt.Errorf("%w: team %q minPlayers out of range", ErrInvalidRuleSet, t.Name)
		}
	}

	attrNames := make(map[string]string, len(rs.PlayerAttributes))
	for i, pa := range rs.PlayerAttributes {
		if pa.Name == "" {
			return fmt.Errorf("%w: playerAttributes[%d].name is required", ErrInvalidRuleSet, i)
		}
		switch pa.Type {
		case "string", "number", "string_list", "string_number_map":
		default:
			return fmt.Errorf("%w: playerAttributes[%d] unknown type %q", ErrInvalidRuleSet, i, pa.Type)
		}
		if _, dup := attrNames[pa.Name]; dup {
			return fmt.Errorf("%w: duplicate playerAttribute %q", ErrInvalidRuleSet, pa.Name)
		}
		attrNames[pa.Name] = pa.Type
	}

	switch rs.Algorithm.Strategy {
	case "", "exhaustiveSearch", "balanced":
	default:
		return fmt.Errorf("%w: unknown algorithm.strategy %q", ErrInvalidRuleSet, rs.Algorithm.Strategy)
	}
	switch rs.Algorithm.BatchingPreference {
	case "", "largestPopulation", "fastestRegion", "balanced":
	default:
		return fmt.Errorf("%w: unknown algorithm.batchingPreference %q", ErrInvalidRuleSet, rs.Algorithm.BatchingPreference)
	}
	if rs.Algorithm.Strategy == "balanced" && rs.Algorithm.BalancedAttribute == "" {
		return fmt.Errorf("%w: balanced strategy requires balancedAttribute", ErrInvalidRuleSet)
	}

	ruleNames := make(map[string]RuleType, len(rs.Rules))
	for i, r := range rs.Rules {
		if r.Name == "" {
			return fmt.Errorf("%w: rules[%d].name is required", ErrInvalidRuleSet, i)
		}
		if _, dup := ruleNames[r.Name]; dup {
			return fmt.Errorf("%w: duplicate rule %q", ErrInvalidRuleSet, r.Name)
		}
		ruleNames[r.Name] = r.Type
		if err := validateRule(&r); err != nil {
			return fmt.Errorf("%w: rule %q: %v", ErrInvalidRuleSet, r.Name, err)
		}
	}

	for i, r := range rs.Rules {
		if r.Type != RuleCompound {
			continue
		}
		if r.Statement == nil {
			return fmt.Errorf("%w: rules[%d] compound requires statement", ErrInvalidRuleSet, i)
		}
		switch r.Statement.Condition {
		case "and", "or", "not":
		default:
			return fmt.Errorf("%w: rule %q unknown condition %q", ErrInvalidRuleSet, r.Name, r.Statement.Condition)
		}
		for _, child := range r.Statement.Rules {
			if _, ok := ruleNames[child]; !ok {
				return fmt.Errorf("%w: rule %q references unknown rule %q", ErrInvalidRuleSet, r.Name, child)
			}
		}
	}

	if rs.AcceptanceTimeoutSeconds < 0 {
		return fmt.Errorf("%w: acceptanceTimeoutSeconds must be >= 0", ErrInvalidRuleSet)
	}

	for i, exp := range rs.Expansions {
		if exp.Target == "" {
			return fmt.Errorf("%w: expansions[%d].target is required", ErrInvalidRuleSet, i)
		}
		if !strings.HasPrefix(exp.Target, "rules[") && !strings.HasPrefix(exp.Target, "algorithm.") {
			return fmt.Errorf("%w: expansions[%d].target %q must reference rules[name].field or algorithm.field", ErrInvalidRuleSet, i, exp.Target)
		}
		if len(exp.Steps) == 0 {
			return fmt.Errorf("%w: expansions[%d] has no steps", ErrInvalidRuleSet, i)
		}
		for j := 1; j < len(exp.Steps); j++ {
			if exp.Steps[j].WaitTimeSeconds < exp.Steps[j-1].WaitTimeSeconds {
				return fmt.Errorf("%w: expansions[%d].steps must be ordered by waitTimeSeconds", ErrInvalidRuleSet, i)
			}
		}
	}

	return nil
}

func validateRule(r *Rule) error {
	switch r.Type {
	case RuleComparison:
		if len(r.Measurements) == 0 {
			return fmt.Errorf("comparison requires measurements")
		}
		switch r.Operation {
		case "=", "!=", "<", "<=", ">", ">=":
		default:
			return fmt.Errorf("comparison unknown operation %q", r.Operation)
		}
	case RuleDistance:
		if len(r.Measurements) == 0 {
			return fmt.Errorf("distance requires measurements")
		}
		if r.MaxDistance == nil && r.MinDistance == nil {
			return fmt.Errorf("distance requires maxDistance or minDistance")
		}
	case RuleAbsoluteSort:
		switch r.SortDirection {
		case "ascending", "descending":
		default:
			return fmt.Errorf("absoluteSort unknown sortDirection %q", r.SortDirection)
		}
		if r.SortAttribute == "" {
			return fmt.Errorf("absoluteSort requires sortAttribute")
		}
	case RuleBatchDistance:
		if r.BatchAttribute == "" {
			return fmt.Errorf("batchDistance requires batchAttribute")
		}
		if r.MaxAttributeDistance == nil {
			return fmt.Errorf("batchDistance requires maxAttributeDistance")
		}
	case RuleCollection:
		if len(r.Measurements) == 0 {
			return fmt.Errorf("collection requires measurements")
		}
		switch r.Operation {
		case "contains", "not_contains", "intersection", "reference_intersection_count":
		default:
			return fmt.Errorf("collection unknown operation %q", r.Operation)
		}
	case RuleLatency:
		if r.MaxLatency == nil {
			return fmt.Errorf("latency requires maxLatency")
		}
	case RuleCompound:
		// validated at the top level once all rule names are known
	default:
		return fmt.Errorf("unknown rule type %q", r.Type)
	}
	return nil
}
