# internal/expr

Tiny parser and evaluator for the subset of FlexMatch property expressions
used inside rule `measurements` and `referenceValue` strings.

## Responsibility

Turn strings like

    avg(flatten(players.skill))
    set_intersection(players.modes)
    teams[red].players.skill
    avg(players.items[sword])

into an AST (`Parse`) and evaluate that AST against a player roster
(`Eval`). Rule evaluators (`internal/rule`) compose these calls when they
need to compute a number or a set from the candidate match.

## Contents

- `Parse(src string) (Node, error)` — recursive-descent parser. Recognised
  forms:
  - numeric and quoted-string literals
  - `players.<attr>` and `players.<attr>[<key>]` (the latter for
    `string_number_map` attributes)
  - `teams[<name>].players.<attr>`, including `teams[*]`
  - one-argument function calls: `flatten`, `avg`, `min`, `max`, `sum`,
    `count`, `set_intersection`
- `Eval(n Node, ctx *EvalContext) (Value, error)` — evaluates a parsed
  node. `EvalContext` supplies the unsorted player pool plus a
  team-name → players map.
- `Value` — tagged union returned by Eval. Notable kinds:
  - `KindNumber`, `KindString`
  - `KindNumberList`, `KindStringList`
  - `KindStringMatrix` (one string list per player, used by
    `set_intersection`)
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
