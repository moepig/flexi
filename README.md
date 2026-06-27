# flexi

[![Go Reference](https://pkg.go.dev/badge/github.com/moepig/flexi.svg)](https://pkg.go.dev/github.com/moepig/flexi)

`flexi` is an in-memory matchmaking library for Go that is compatible with Amazon GameLift FlexMatch rule sets.

It accepts the same JSON rule set you would pass to AWS's `CreateMatchmakingRuleSet` API and evaluates tickets locally — no GameLift hosting, no AWS connectivity, no networking. Drop the rule set in, enqueue tickets, drive the matchmaker, get matches.

## Features

- **AWS-compatible JSON**: feed the same rule set documented in the FlexMatch developer guide, including the AWS property-expression dialect (`teams[red].players.attributes[skill]`). `ruleLanguageVersion` is required and must be `"1.0"`.
- **All eight rule kinds**: `comparison`, `distance`, `absoluteSort`, `distanceSort`, `batchDistance`, `collection`, `latency`, `compound`.
- **All four player attribute types**: `string`, `number`, `string_list`, `string_number_map`, with `default` values applied to players that omit an attribute. A value whose kind disagrees with the declared type is rejected at `Enqueue`; undeclared attributes pass through.
- **Property-expression aggregations**: `min`, `max`, `avg`, `median`, `sum`, `count`, `stddev`, `flatten`, `set_intersection`, with per-team nesting (`avg(teams[*].players.attributes[skill])` → one result per team).
- **partyAggregation**: `min` / `max` / `avg` (and `union` / `intersection` for collection) on multi-player tickets.
- **Compound statements**: AWS string form with `and` / `or` / `not` / `xor`, e.g. `"or(and(A,B), not(C))"`.
- **Algorithm block**: `exhaustiveSearch` and `balanced` strategies, `batchingPreference` (`random` / `sorted` / `largestPopulation` / `fastestRegion`), `sortByAttributes`, `backfillPriority`, `expansionAgeSelection`.
- **Expansions**: rule values, team sizes (`teams[Red,Blue].minPlayers`), and algorithm fields loosen automatically as tickets wait.
- **Ticket status & player acceptance**: FlexMatch-compatible lifecycle (`QUEUED` → `REQUIRES_ACCEPTANCE` → `PLACING` → `COMPLETED`, plus `SEARCHING` / `CANCELLED` / `TIMED_OUT`) driven by `acceptanceRequired` / `acceptanceTimeoutSeconds` / `requestTimeoutSeconds` on the rule set. On a failed acceptance (reject or acceptance timeout) the tickets that did accept return to `SEARCHING` for re-matching while the rest are `CANCELLED`; `TIMED_OUT` is reserved for the request-level `requestTimeoutSeconds`, mirroring AWS.
- **Injectable clock**: tests advance time deterministically, no `time.Sleep`.
- **Zero external dependencies at runtime** (testify is test-only). No network or persistence.
- **Goroutine-safe**: producers may `Enqueue` / `Cancel` / `Accept` / `Reject` while another goroutine drives `Tick`.

> Backfill of in-progress matches is intentionally out of scope.

## Installation

```bash
go get github.com/moepig/flexi
```

Requires Go 1.26 or later.

## Quick start

```go
package main

import (
    "fmt"
    "github.com/moepig/flexi"
)

const ruleset = `{
  "name": "skill-balance",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [{"name": "skill", "type": "number"}],
  "teams": [
    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
  ],
  "rules": [
    {
      "name": "FairSkill",
      "type": "distance",
      "measurements": ["avg(teams[red].players.attributes[skill])"],
      "referenceValue": "avg(teams[blue].players.attributes[skill])",
      "maxDistance": 10
    }
  ]
}`

func main() {
    mm, err := flexi.New([]byte(ruleset))
    if err != nil {
        panic(err)
    }

    for i, skill := range []float64{50, 52, 49, 51} {
        _ = mm.Enqueue(flexi.Ticket{
            ID: fmt.Sprintf("t%d", i),
            Players: []flexi.Player{{
                ID:         fmt.Sprintf("p%d", i),
                Attributes: flexi.Attributes{"skill": flexi.Number(skill)},
            }},
        })
    }

    matches, _ := mm.Tick()
    for _, m := range matches {
        fmt.Println("red:",  m.Teams["red"])
        fmt.Println("blue:", m.Teams["blue"])
    }
}
```

## Driving the matchmaker

`Matchmaker` does not start any goroutines or timers — the caller drives it by invoking `Tick()`. This keeps tests deterministic and lets you integrate with whatever scheduling, observability, and shutdown story you already have.

```go
ticker := time.NewTicker(time.Second)
defer ticker.Stop()

for range ticker.C {
    matches, err := mm.Tick()
    if err != nil {
        log.Printf("flexi tick: %v", err)
        continue
    }
    for _, m := range matches {
        dispatch(m) // hand off to your game server
    }
}
```

`Tick` returns every match that can be formed at this moment. Tickets consumed by a returned match are removed from the queue atomically; everything else stays queued for a future tick.

## Time and expansions

Anything time-dependent — most importantly the FlexMatch `expansions` block — reads the current time through a `Clock`. The default is the system clock; tests should pass `WithClock(NewFakeClock(...))` so they can advance time without sleeping.

```go
clock := flexi.NewFakeClock(time.Now())
mm, _ := flexi.New(rulesetJSON, flexi.WithClock(clock))

mm.Enqueue(ticket)
matches, _ := mm.Tick()           // before any expansion step

clock.Advance(60 * time.Second)
matches, _ = mm.Tick()            // expansion steps with waitTimeSeconds<=60 applied
```

## A richer rule set example

```json
{
  "name": "example",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [
    {"name": "skill", "type": "number"},
    {"name": "modes", "type": "string_list"}
  ],
  "algorithm": {
    "strategy": "balanced",
    "balancedAttribute": "skill"
  },
  "teams": [
    {"name": "team", "minPlayers": 3, "maxPlayers": 3, "quantity": 2}
  ],
  "rules": [
    {
      "name": "FairSkill",
      "type": "distance",
      "measurements": ["avg(teams[team_1].players.attributes[skill])"],
      "referenceValue": "avg(teams[team_2].players.attributes[skill])",
      "maxDistance": 10
    },
    {
      "name": "ModeOverlap",
      "type": "collection",
      "measurements": ["set_intersection(players.attributes[modes])"],
      "operation": "reference_intersection_count",
      "referenceValue": ["TDM", "CTF", "FFA"],
      "minCount": 1
    },
    {"name": "Ping", "type": "latency", "maxLatency": 150},
    {
      "name": "All",
      "type": "compound",
      "statement": "and(FairSkill, ModeOverlap, Ping)"
    }
  ],
  "expansions": [
    {
      "target": "rules[FairSkill].maxDistance",
      "steps": [
        {"waitTimeSeconds": 30, "value": 50},
        {"waitTimeSeconds": 60, "value": 200}
      ]
    }
  ]
}
```

## FlexMatch compliance notes

A few rule types have subtle AWS semantics that `flexi` follows deliberately:

- **`collection` operations**
  - `contains` — counts how many times the reference value appears across the (flattened) measurement and bounds that count with `minCount`/`maxCount` (e.g. "no more than 5 medics in a match"). With no bound it just requires the value to be present.
  - `intersection` — counts the values shared by **every** player's collection and takes **no** `referenceValue` (e.g. "all players share at least one game mode", `minCount: 1`).
  - `reference_intersection_count` — requires **each** player's collection to intersect the `referenceValue` collection within `minCount`/`maxCount`. The `referenceValue` may be a literal array or a property expression such as `set_intersection(flatten(teams[*].players.attributes[preferredOpponents]))`.
- **`batchDistance`** — a **numeric** attribute is grouped by spread (`maxDistance` / `minDistance`); a **string** attribute is grouped by value equivalency, where the distance is `(distinct values) - 1`. A string `batchDistance` with **no** `maxDistance` therefore requires every player to share the same value (the AWS "SameGameMode" form).
- **`maxDistance` / `minDistance`** accept either a JSON number (`500`) or a string-encoded number (`"500"`); the AWS docs use both forms.

## Ticket status and player acceptance

Every ticket has a FlexMatch-compatible `TicketStatus`, queryable with
`Matchmaker.Status(id)`:

```
Enqueue                        → QUEUED
Tick (acceptanceRequired=false) → PLACING                           (Match returned)
Tick (acceptanceRequired=true)  → REQUIRES_ACCEPTANCE               (Proposal created)
  all Accept, then Tick        → PLACING                           (Match returned)
  Cancel                       → CANCELLED                         (whole proposal)
  any Reject / acceptance      → rejecter/non-responder CANCELLED;
    timeout                      fully-accepted siblings SEARCHING (re-queued)
requestTimeoutSeconds elapsed  → TIMED_OUT
MarkCompleted (from PLACING)   → COMPLETED
```

`SEARCHING` marks a ticket that accepted a proposed match which then failed to
gather every required acceptance: it is returned to the queue and re-matched by
the next `Tick`. `TIMED_OUT` is reached only when a ticket exceeds the rule
set's `requestTimeoutSeconds` while searching — an **acceptance** failure
(reject or acceptance timeout) terminates the non-accepting tickets as
`CANCELLED`, matching AWS FlexMatch. `FAILED` is defined for parity with the AWS
API but is not produced by the current implementation.

Because this library operates as **FlexMatch standalone** (no game-session
placement), the terminal success status `Tick` assigns is `PLACING`.
Promote a ticket to `COMPLETED` with `MarkCompleted(id)` once your own
placement pipeline has attached connection information.

Enable the acceptance flow by setting the two standard FlexMatch fields on
the rule set:

```json
{
  ...
  "acceptanceRequired": true,
  "acceptanceTimeoutSeconds": 60
}
```

Then drive the extra state machine:

```go
matches, _ := mm.Tick()            // acceptanceRequired=true → no matches yet
for _, p := range mm.PendingAcceptances() {
    for _, id := range p.TicketIDs {
        for _, pl := range p.Teams[...] /* surface to your players */ {
            if accepted { mm.Accept(id, pl.ID) } else { mm.Reject(id, pl.ID) }
        }
    }
}
matches, _ = mm.Tick()             // fully-accepted proposals are now returned
```

When a proposed match **fails acceptance** — a player `Reject`s, or
`acceptanceTimeoutSeconds` elapses before everyone responds — flexi follows AWS
FlexMatch by splitting the proposal's tickets:

- Tickets on which **every player had already accepted** return to the queue in
  `SEARCHING` and are re-considered by the next `Tick`. `Matchmaker.StatusReason(id)`
  reports `StatusReasonAcceptanceFailed` for them, so a caller polling `Status`
  can tell a re-entering ticket apart from a freshly enqueued one (and emit the
  corresponding `MatchmakingSearching` event).
- The ticket(s) that **caused the failure** — the rejecting player's ticket, or
  any ticket whose players never responded — move to `CANCELLED`, whether the
  failure was a reject or an acceptance timeout. (FlexMatch reserves `TIMED_OUT`
  for the request-level deadline below, not for acceptance failures.) Resubmit
  with a fresh ticket ID for another attempt.

```go
mm.Accept("a", "alice")
mm.Reject("b", "bob")              // proposal fails acceptance
mm.Status("a")                      // SEARCHING  (re-queued)
mm.StatusReason("a")                // StatusReasonAcceptanceFailed, true
mm.Status("b")                      // CANCELLED  (terminal)
mm.Tick()                           // re-matches "a" against the pool
```

A user-initiated `Cancel` on any participating ticket is different: it always
dissolves the **whole** proposal, terminating every ticket as `CANCELLED`.

### Request timeout

Set `requestTimeoutSeconds` on the rule set to bound how long a ticket may stay
in matchmaking overall:

```json
{
  ...
  "requestTimeoutSeconds": 60
}
```

Any `QUEUED` or re-queued (`SEARCHING`) ticket that has been waiting longer than
this — measured from its **original** `Enqueue`, so the clock keeps running
across an acceptance-failure re-queue — moves to `TIMED_OUT` on the next `Tick`.
The deadline applies whether or not `acceptanceRequired` is set; tickets
currently held in a proposal (`REQUIRES_ACCEPTANCE`) are governed by
`acceptanceTimeoutSeconds` instead. Zero (the default) disables it.

## Rule evaluation metrics

FlexMatch's match events (`PotentialMatchCreated`, `MatchmakingTimedOut`,
`MatchmakingCancelled`) include `ruleEvaluationMetrics` — per-rule
`{ruleName, passedCount, failedCount}` tallies. flexi reproduces these:

- Every `Match` and `Proposal` carries `RuleEvaluationMetrics`, the pass/fail
  counts accumulated by the search that formed that (candidate) match. This
  maps to `PotentialMatchCreated`.
- `Matchmaker.RuleMetrics(id)` returns the **cumulative** per-rule tallies for
  a ticket across every `Tick` it took part in, retained through terminal
  states. Use it for the `MatchmakingTimedOut` / `MatchmakingCancelled` events
  of tickets that never made it into a match.

```go
matches, _ := mm.Tick()
for _, rm := range matches[0].RuleEvaluationMetrics {
    fmt.Printf("%s: passed=%d failed=%d\n", rm.RuleName, rm.PassedCount, rm.FailedCount)
}

// For a timed-out / cancelled ticket:
if metrics, ok := mm.RuleMetrics("ticket-1"); ok {
    // metrics[i].RuleName / PassedCount / FailedCount
}
```

Rule names match those declared in the rule set's `rules` block; a compound
rule is reported once (its child evaluations are not listed separately). Each
rule-set evaluation counts every rule (no short-circuit) so `failedCount` is
complete. Tickets never involved in an evaluation report no metrics (the slice
is nil / `RuleMetrics` returns `false`), keeping the addition backward
compatible.

## API at a glance

Full reference is on [pkg.go.dev](https://pkg.go.dev/github.com/moepig/flexi). The most-used surface:

| Symbol | Description |
| --- | --- |
| `flexi.New(rulesetJSON, opts...)` | Parse a rule set JSON document and return a `Matchmaker`. |
| `flexi.WithClock(c)` | Option that overrides the time source. |
| `Matchmaker.Enqueue(t)` | Add a ticket to the queue. |
| `Matchmaker.Cancel(id)` | Remove a queued ticket, or dissolve a proposal it is part of. |
| `Matchmaker.Tick()` | Expire timed-out proposals, resolve accepted ones, and form new matches. |
| `Matchmaker.Pending()` | Count of tickets currently in `QUEUED`. |
| `Matchmaker.Status(id)` | Current `TicketStatus` for a ticket. |
| `Matchmaker.StatusReason(id)` | Supplementary `StatusReason` (e.g. acceptance-failure re-queue), when one applies. |
| `Matchmaker.RuleMetrics(id)` | Cumulative per-rule pass/fail tallies for a ticket (`ruleEvaluationMetrics`). |
| `Matchmaker.PendingAcceptances()` | Snapshot of proposals in `REQUIRES_ACCEPTANCE`. |
| `Matchmaker.Accept(id, playerID)` / `Reject(id, playerID)` | Record a player's decision on a proposed match. |
| `Matchmaker.MarkCompleted(id)` | Promote a `PLACING` ticket to `COMPLETED`. |
| `flexi.Number / String / StringList / StringNumberMap` | Constructors for the four `Attribute` variants. |
| `flexi.NewFakeClock(t)` | Test clock you can `Advance` or `Set`. |

## Testing

```bash
go test ./... -race
```

Each layer (rule set parser, expression evaluator, rule evaluators, expansions, algorithm, queue) has its own unit tests. End-to-end scenarios driven through the public API live in `examples_test.go`. Tests use [stretchr/testify](https://github.com/stretchr/testify).

## License

See `LICENSE` for license terms.
