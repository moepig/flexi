# internal/expr

Tiny parser and evaluator for the subset of FlexMatch property expressions
used inside rule `measurements` and `referenceValue` strings.

## Responsibility

Turn strings like

    avg(flatten(teams[*].players.attributes[skill]))
    set_intersection(players.attributes[modes])
    teams[red].players.attributes[skill]
    avg(players.attributes[items][sword])

into an AST (`Parse`) and evaluate that AST against a player roster
(`Eval`). The syntax follows the AWS FlexMatch property-expression dialect.
Rule evaluators (`internal/rule`) compose these calls when they need to
compute a number or a set from the candidate match.

## Contents

- `Parse(src string) (Node, error)` — recursive-descent parser. Recognised
  forms:
  - numeric and quoted-string literals
  - `players.attributes[<attr>]` and `players.attributes[<attr>][<key>]`
    (the latter for `string_number_map` attributes)
  - `players[playerId]` and bare `players` (player IDs, for `count`)
  - `teams[<name>].players...`, including multiple names `teams[a,b]` and
    `teams[*]`; multi-team scopes group results per team (nested lists)
  - one-argument function calls: `flatten`, `avg`, `min`, `max`, `sum`,
    `median`, `stddev`, `count`, `set_intersection`
- `Eval(n Node, ctx *EvalContext) (Value, error)` — evaluates a parsed
  node. `EvalContext` supplies the unsorted player pool, a team-name →
  players map, and a deterministic team order for `teams[*]`.
- `Value` — recursive value tree returned by Eval. Kinds:
  - `KindNumber`, `KindString`
  - `KindList` (a list of `Value`s, which may itself contain lists for
    per-team nesting; numeric/string aggregations applied to a list of
    lists operate on each sublist individually, per FlexMatch)
  - `KindNone` — produced when an aggregate (`avg`, `min`, `max`) is
    applied to an empty list. Rule evaluators interpret this as "the rule
    is not yet evaluable" and skip the comparison rather than failing it,
    which is what lets the matchmaker grow a candidate match step by step.

## Design notes

- The grammar is intentionally narrow. The FlexMatch reference contains
  many more compositions than we strictly need; add them here as concrete
  rule examples motivate them, not speculatively.
- The parser is hand-written and zero-dependency; benchmark before reaching
  for a parser generator.
- Values share backing slices with the input where practical. Callers must
  not mutate slices read out of a `Value`.
