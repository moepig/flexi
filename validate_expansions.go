package flexi

import (
	"fmt"
	"slices"
	"time"

	"github.com/moepig/flexi/internal/expansion"
	"github.com/moepig/flexi/internal/rule"
	"github.com/moepig/flexi/internal/ruleset"
)

func validateExpansions(rs *ruleset.RuleSet) error {
	var times []int
	for i, exp := range rs.Expansions {
		if err := expansion.ValidateTarget(rs, exp.Target); err != nil {
			return fmt.Errorf("%w: expansions[%d]: %v", ErrInvalidRuleSet, i, err)
		}
		for j, step := range exp.Steps {
			if step.WaitTimeSeconds < 0 {
				return fmt.Errorf("%w: expansions[%d].steps[%d] waitTimeSeconds must be >= 0", ErrInvalidRuleSet, i, j)
			}
			times = append(times, step.WaitTimeSeconds)
		}
	}
	slices.Sort(times)
	times = slices.Compact(times)
	for _, seconds := range times {
		at, err := expansion.Apply(rs, time.Duration(seconds)*time.Second)
		if err != nil {
			return fmt.Errorf("%w: expansion at %d seconds: %v", ErrInvalidRuleSet, seconds, err)
		}
		if err := at.Validate(); err != nil {
			return fmt.Errorf("%w: expansion at %d seconds: %v", ErrInvalidRuleSet, seconds, err)
		}
		if _, err := rule.BuildSet(at); err != nil {
			return fmt.Errorf("%w: expansion at %d seconds: %v", ErrInvalidRuleSet, seconds, err)
		}
	}
	return nil
}
