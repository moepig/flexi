# internal/algorithm

Match assembly: turns a queue of tickets plus a set of rule evaluators into
zero or more concrete matches.

## Responsibility

This is the search loop. For each match attempt it expands the rule set's
teams (honouring `quantity`), walks tickets in queue order, and tries to
place each ticket on a team without violating any rule. When every team
reaches `minPlayers` and every rule still passes, the assembled grouping
is emitted as a match.

## Contents

- `Result` — one assembled match: per-team players, the consumed
  ticket IDs, and the inferred shared region.
- `Build(rs, evals, tickets) ([]Result, []core.Ticket)` — the only export.
  Calls `formOne` repeatedly, accumulating matches until no more can be
  formed. Returns the leftover tickets in queue order so the caller can
  put them back.

## Algorithm

For a single match (`formOne`):

1. Expand `rs.Teams` into concrete `teamSlot` instances. A team with
   `quantity: N` becomes `N` slots named `<base>_1` … `<base>_N`.
2. If `algorithm.strategy == "balanced"`, pre-sort the ticket list by the
   `balancedAttribute` descending; this gives the greedy "place into the
   team with the lowest current attribute sum" loop a much better split.
3. For each ticket, compute a team ordering (`teamOrder`) and try to
   place the whole party on the first slot where:
   - capacity is not exceeded (`canAdd`), and
   - all rules still pass against the candidate so far.
   If no slot works, the ticket is left in the queue.
4. If the seed ticket cannot be placed at all, abandon this attempt.
5. After processing every ticket (or once every slot is full), require
   that every slot has at least `minPlayers` and that all rules still
   pass; otherwise abandon.
6. Emit a `Result` and let `Build` try again with the remaining tickets.

## Design notes

- The search is greedy on purpose. A full backtracking search would be
  combinatorial in the ticket count; this implementation is fast and
  produces the same result on most realistic inputs.
- Rules are checked at every placement step, not just at the end.
  `internal/expr` returns `KindNone` for aggregates over empty teams and
  `internal/rule` treats that as "skip", which is what makes incremental
  evaluation safe for rules like `distance` that compare team aggregates.
- `sharedRegion` picks the region every player has a latency entry for,
  but the latency rule itself is responsible for verifying the threshold;
  the region in `Result` is informational.
- Add new placement strategies (e.g. richer `batchingPreference` handling)
  by extending `teamOrder` rather than the main loop.
