# internal/expansion

Applies the FlexMatch `expansions` block: time-driven loosening of rule and
algorithm values.

## Responsibility

Given an immutable `*ruleset.RuleSet` and the selected queue age, return a deep copy whose values reflect every active expansion step. The matchmaker keeps the base definition and the most recently applied step combination so repeated ticks can reuse the validated definition.

## Contents

- `Apply(rs *ruleset.RuleSet, elapsed time.Duration) (*ruleset.RuleSet, error)` selects each target's latest active step and applies it to a clone.
- `ValidateTarget(rs, target)` checks the referenced declaration and field before the matchmaker is constructed.

## Supported targets

- `rules[<name>].<field>` for the numeric rule fields the FlexMatch
  reference declares as expansion targets:
  `maxDistance`, `minDistance`, `maxLatency`,
  `minCount`, `maxCount`, plus `referenceValue` (passed through verbatim).
- `algorithm.<field>` for `strategy`, `batchingPreference`, `balancedAttribute`, `backfillPriority`, and `expansionAgeSelection`.

Targets outside this set return an error so the matchmaker surfaces the
misconfiguration instead of silently ignoring it.

## Design notes

- `Apply` always returns a new `*RuleSet`; the input is never mutated. The matchmaker serializes cache updates under its mutex.
- Expansion steps are assumed to be ordered by `waitTimeSeconds`
  (validated in `internal/ruleset`). The picker walks them and keeps the
  last one that qualifies.
- Cloning is deep enough to cover every pointer/slice field on `Rule`. Add
  to `cloneRule` whenever a new optional field shows up in `ruleset.Rule`.
