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
- `Build(rs, set, tickets)` returns matches, leftover tickets, per-ticket metrics, and an evaluation error. Results and metrics are discarded on error.
- `CheckBackfillRoster(rs, players) error` — validates a backfill ticket's
  team assignments against `rs` on behalf of the public package, so a
  roster that could never be seated is rejected at enqueue time.
- `MergeMetrics(dst, src)` — accumulates rule-evaluation tallies by rule
  name, shared with the public package.

## Algorithm

For a single match (`formOne`):

1. Expand `rs.Teams` into concrete `teamSlot` instances. A team with
   `quantity: N` becomes `N` slots named `<base>_1` … `<base>_N`.
2. If the search was given a backfill ticket, seat its players on the slots
   their `Player.Team` names (`seedBackfill`), each as a one-player party.
   Only regular tickets go on to the placement loop.
3. If `algorithm.strategy == "balanced"`, pre-sort the ticket list by the
   `balancedAttribute` descending; this gives the greedy "place into the
   team with the lowest current attribute sum" loop a much better split.
   Otherwise, order the batch (`orderBatch`, see `sort.go`): apply
   `batchingPreference: "sorted"` with `sortByAttributes`, then any
   `absoluteSort` / `distanceSort` rules (keeping the oldest ticket as the
   anchor the match is built around).
4. For each ticket, compute a team ordering (`teamOrder`) and try to
   place the whole party on the first slot where:
   - capacity is not exceeded (`canAdd`), and
   - ready rules pass against the candidate so far. Count rules wait for the referenced teams' minimum sizes, and eligible collection lower bounds may be deferred.
   If no slot works, the ticket is left in the queue.
5. If the seed ticket cannot be placed at all, abandon this attempt.
6. After processing every ticket (or once every slot is full), require
   that every slot has at least `minPlayers`, that a backfill search
   admitted at least one regular ticket, and that all rules still pass;
   otherwise abandon.
7. Emit a `Result` and let `Build` try again with the remaining tickets.

`formNext` wraps `formOne` to apply `algorithm.backfillPriority`
(`backfillAttempts`), deciding which of the pool's backfill tickets the
search offers as a seed and in what order, and merging the metrics of every
attempt it makes.

## Design notes

- The search is greedy on purpose. A full backtracking search would be
  combinatorial in the ticket count; this implementation is fast and
  produces the same result on most realistic inputs.
- Ready rules are checked at every placement step and all rules are checked on the complete candidate. A deferred collection lower bound does not count as a pass or failure in placement metrics. Evaluation errors stop the search and discard its results.
- Per-ticket metrics are derived from cumulative search totals when each ticket is consumed or the search ends. This preserves attribution without updating every waiting ticket after every match.
- `sharedRegion` picks the region every player has a latency entry for,
  but the latency rule itself is responsible for verifying the threshold;
  the region in `Result` is informational.
- A seeded backfill roster is never evaluated on its own, only as part of
  the final check over the combined roster. The match those players came
  from is a given, and it may have been formed under expansion-loosened
  values the rule set no longer offers; re-judging it in isolation would
  reject rosters that FlexMatch keeps.
- Add new placement strategies (e.g. richer `batchingPreference` handling)
  by extending `teamOrder` rather than the main loop.
