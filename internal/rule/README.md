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
  - `compound.go`
  - `absoluteSort` is built by `Build` directly as an "always pass"
    evaluator — it affects ordering, which is the algorithm's concern, not
    admission.

## Design notes

- Evaluators are pure functions of `*Candidate`: no internal state, no
  side effects. This makes them trivially safe to share across goroutines
  and easy to test in isolation.
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
