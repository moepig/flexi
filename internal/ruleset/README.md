# internal/ruleset

JSON parser and semantic validator for AWS GameLift FlexMatch rule sets.

## Responsibility

Turn a `[]byte` containing the FlexMatch rule set JSON document — the same
payload accepted by AWS's `CreateMatchmakingRuleSet` API (`RuleSetBody`) —
into a typed `*RuleSet` and return descriptive errors for anything that is
malformed or contradictory.

This package only **describes** the rule set; it does not evaluate any
rules. The evaluator packages (`expr`, `rule`, `algorithm`, `expansion`)
consume the structures defined here.

## Contents

- `RuleSet`, `Team`, `PlayerAttribute`, `Algorithm`, `Rule`,
  `Expansion`, `ExpansionStep` — the parsed structures that mirror the
  FlexMatch JSON schema. `RuleSet` also carries the top-level
  `AcceptanceRequired` / `AcceptanceTimeoutSeconds` fields that drive the
  player-acceptance flow, and `RequestTimeoutSeconds` which bounds a
  ticket's overall time in matchmaking, in the public package.
- `RuleType` and its constants (`RuleComparison`, `RuleDistance`, …,
  `RuleDistanceSort`, `RuleCompound`) for switching on rule kind.
- `CompoundNode` and `ParseCompound(s string)` — parser for a compound
  rule's `statement` string (e.g. `"or(and(A,B), not(C))"`) into a tree of
  `and`/`or`/`not`/`xor` operators and leaf rule references.
- `Parse(body []byte) (*RuleSet, error)` — entry point. Returns errors that
  wrap `ErrInvalidRuleSet`.
- `(*RuleSet).Validate()` — the semantic checks Parse runs after JSON
  decoding (uniqueness, references, enum values, expansion target shape,
  etc.). Exposed for tests and for callers that build a `RuleSet` by hand.

## Design notes

- Fields whose value type varies across rule kinds (most notably
  `referenceValue`) are kept as `json.RawMessage`. Each rule evaluator
  decodes the raw bytes when it knows the expected shape.
- Optional numeric fields are pointer types (`*float64`, `*int`) so the
  validator and evaluators can distinguish "absent" from "zero".
- Unknown JSON fields are silently ignored on purpose — AWS may add new
  optional fields and we don't want a strict decoder to reject them.
- Compound rule references are validated against the set of rule names in a
  second pass, so forward references work.
