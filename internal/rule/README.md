# internal/rule

Per-kind rule evaluators. Given a parsed `ruleset.Rule`, this package builds
an `Evaluator` that answers "does this candidate match satisfy me?".

## Responsibility

Convert the declarative `ruleset.Rule` records into runnable filters that
the algorithm consults while assembling matches. Each rule kind lives in
its own file so the implementations stay small and obvious.

## Contents

- `Build(r *ruleset.Rule, compounds map[string]Evaluator) (Evaluator, error)`
  — the factory dispatched by rule type. `compounds` carries the already
  built evaluators that compound rules can reference.
- `BuildSet(rs)` constructs evaluators in dependency order, validates parsed expressions and compound cycles, and returns top-level evaluators in declaration order with AST-based placement dependencies.
- `Candidate` — the tentative match passed to evaluators: full player
  roster, per-team roster, and (optionally) a chosen region for latency
  evaluation.
- `Evaluator` interface: `Name()` + `Evaluate(*Candidate) (bool, error)`.
- One implementation file per kind:
  - `comparison.go`
  - `distance.go`
  - `batch_distance.go`
  - `collection.go`
  - `latency.go`
  - `compound.go` (parses the `statement` string via
    `ruleset.ParseCompound` and evaluates `and`/`or`/`not`/`xor`)
  - `party.go` (collapses multi-player tickets per `partyAggregation`
    before evaluation: numeric min/max/avg, or union/intersection for
    collection rules)
  - `absoluteSort` and `distanceSort` are built by `Build` as "always pass"
    evaluators — they affect ordering, which is the algorithm's concern
    (see `internal/algorithm/sort.go`), not admission.

## Design notes

- Evaluators are read-only after construction. A collection rule eligible for partial evaluation checks its upper bound during placement and defers an unmet lower bound until the complete candidate.
- A `false` return means "this candidate does not pass right now". An
  error means the rule was misconfigured in a way the validator did not
  catch (e.g. an attribute referenced by the wrong type).
- Rules that reference team aggregates (most distance and comparison
  rules) tolerate empty teams: when the underlying expression returns
  `expr.KindNone`, the rule is skipped rather than failed. This is what
  lets the algorithm grow a partial candidate without prematurely
  rejecting it.
- Latency is evaluated by trying every region present in the candidate's
  players when `Candidate.Region` is empty; if any region satisfies the
  threshold for every player, the rule passes. Set `Candidate.Region`
  when you want to pin evaluation to a single region.
