# internal/expansion

Applies the FlexMatch `expansions` block: time-driven loosening of rule and
algorithm values.

## Responsibility

Given an immutable `*ruleset.RuleSet` and how long the oldest queued ticket
has been waiting, return a deep copy whose values reflect every expansion
step whose `waitTimeSeconds` threshold has been reached. The matchmaker
calls this once per `Tick` so each pass evaluates against an
appropriately-relaxed rule set, while the original rule set stays
untouched for later ticks.

## Contents

- `Apply(rs *ruleset.RuleSet, elapsed time.Duration) (*ruleset.RuleSet, error)`
  — the only export. Walks `rs.Expansions`, picks the latest step whose
  `waitTimeSeconds <= elapsed.Seconds()` for each target, and writes the
  new value into a clone of `rs`.

## Supported targets

- `rules[<name>].<field>` for the numeric rule fields the FlexMatch
  reference declares as expansion targets:
  `maxDistance`, `minDistance`, `maxAttributeDistance`, `maxLatency`,
  `minCount`, `maxCount`, plus `referenceValue` (passed through verbatim).
- `algorithm.<field>` for `strategy`, `batchingPreference`,
  `balancedAttribute`, `backfillPriority`.

Targets outside this set return an error so the matchmaker surfaces the
misconfiguration instead of silently ignoring it.

## Design notes

- `Apply` always returns a new `*RuleSet`; the input is never mutated. This
  keeps `flexi.Matchmaker.Tick` safe to call concurrently with no extra
  locking around the rule set itself.
- Expansion steps are assumed to be ordered by `waitTimeSeconds`
  (validated in `internal/ruleset`). The picker walks them and keeps the
  last one that qualifies.
- Cloning is deep enough to cover every pointer/slice field on `Rule`. Add
  to `cloneRule` whenever a new optional field shows up in `ruleset.Rule`.
