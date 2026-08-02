// Package flexi implements an in-memory matchmaking engine compatible with
// Amazon GameLift FlexMatch rule sets.
//
// The engine accepts the AWS FlexMatch rule set JSON document — the same
// payload passed to CreateMatchmakingRuleSet's RuleSetBody parameter — and
// evaluates matchmaking tickets against it, producing matches whose teams
// satisfy every configured rule.
//
// # Scope
//
// flexi targets FlexMatch's "standalone" use case: pure rule evaluation with
// no GameLift hosting integration, no networking, and no persistence. The
// ticket queue is held in memory only.
//
// The rule set must declare ruleLanguageVersion "1.0" (the only version AWS
// FlexMatch supports); a missing or different value is rejected.
//
// Supported rule set features:
//
//   - Player attribute types: string, number, string_list, string_number_map,
//     with default values applied to players that omit an attribute. A value
//     whose kind disagrees with the declared type is rejected at [Matchmaker.Enqueue];
//     attributes not declared in the rule set are carried through unchecked.
//   - Property expressions in the AWS dialect, e.g.
//     teams[red].players.attributes[skill]; aggregations min, max, avg, median,
//     sum, count, stddev, flatten, set_intersection, with per-team nesting for
//     multi-team scopes (teams[a,b], teams[*]).
//   - Algorithm strategies: exhaustiveSearch, balanced (with balancedAttribute).
//   - Algorithm batchingPreference (random, sorted, largestPopulation,
//     fastestRegion), sortByAttributes, backfillPriority, expansionAgeSelection.
//   - Teams with minPlayers, maxPlayers, and quantity (multi-instance teams).
//   - All eight FlexMatch rule kinds: comparison, distance, absoluteSort,
//     distanceSort, batchDistance, collection, latency, compound (with a
//     statement string using and/or/not/xor).
//   - partyAggregation (min/max/avg, or union/intersection for collection) for
//     multi-player tickets.
//
// A few rule types follow the AWS semantics precisely enough to be worth
// spelling out:
//
//   - collection: "contains" counts how many times the reference value occurs in
//     the measurement (bounded by minCount/maxCount); "intersection" counts the
//     values shared by every player's collection and takes no referenceValue;
//     "reference_intersection_count" requires each player's collection to
//     intersect the reference value within minCount/maxCount.
//   - batchDistance: a numeric attribute is grouped by spread (maxDistance); a
//     string attribute is grouped by value equivalency, and with no maxDistance
//     it requires every player to share one value.
//   - maxDistance / minDistance accept either a JSON number or a string-encoded
//     number (e.g. "500"), matching the inconsistent AWS documentation.
//   - Time-driven expansions that loosen rule values, team sizes, or algorithm
//     fields once a ticket has been waiting long enough.
//   - Rule evaluation metrics (FlexMatch's ruleEvaluationMetrics): per-rule
//     pass/fail tallies on each [Match] and [Proposal], plus cumulative
//     per-ticket totals via [Matchmaker.RuleMetrics].
//
// # Backfill
//
// [Matchmaker.EnqueueBackfill] fills the empty seats of a match already in
// progress, the standalone-mode counterpart of GameLift's StartMatchBackfill.
// The request carries everyone currently in the game session, each tagged with
// the team they are on; those players are seated first and matchmaking fills
// what is left, so the rules are evaluated over the existing roster and the
// candidates together and the match keeps satisfying the rule set it was formed
// under. The resulting [Match] describes every seat in the session.
//
// A backfill request is an ordinary ticket in every other respect: it is
// tracked through [Matchmaker.Status], counted by [Matchmaker.Pending], subject
// to requestTimeoutSeconds, withdrawn with [Matchmaker.Cancel], and — when the
// rule set sets acceptanceRequired — proposed for acceptance by the players
// already in the session as well as the ones joining, which a game server
// accepts on their behalf. algorithm.backfillPriority decides when matchmaking
// reaches for such a request: "high" before starting a new match, "low" only
// once no new match can be formed, and the default treats it as any other
// ticket. As in FlexMatch the property applies to the exhaustiveSearch strategy
// only; the balanced strategy carries it but matches as though it were unset.
//
// Two constraints follow AWS: at most one backfill request takes part in any
// match, and a match only forms if at least one new ticket joins. Automatic
// backfill (BackfillMode "AUTOMATIC") is out of scope, as AWS itself does not
// offer it in standalone mode, and flexi knows nothing about game sessions
// beyond the optional identifier used to supersede a session's outstanding
// request.
//
// # Quick start
//
//	mm, err := flexi.New(rulesetJSON)
//	if err != nil { ... }
//
//	mm.Enqueue(flexi.Ticket{
//	    ID: "ticket-1",
//	    Players: []flexi.Player{{
//	        ID: "alice",
//	        Attributes: flexi.Attributes{"skill": flexi.Number(1500)},
//	        Latencies:  map[string]int{"us-east-1": 35},
//	    }},
//	})
//
//	matches, err := mm.Tick()
//	for _, m := range matches {
//	    // m.Teams maps team name -> assigned players
//	    // m.TicketIDs lists tickets consumed by the match
//	}
//
// # Driving the matchmaker
//
// Matchmaker has no internal goroutines or timers. Callers drive it by
// invoking [Matchmaker.Tick], which returns every match that can be formed
// against the current queue. This keeps tests deterministic and lets callers
// integrate with whatever scheduling, observability, or shutdown story they
// already have. A typical production loop looks like:
//
//	t := time.NewTicker(time.Second)
//	for range t.C {
//	    matches, err := mm.Tick()
//	    // dispatch matches, log err
//	}
//
// # Errors and retained state
//
// Every failure is classifiable with errors.Is. A ticket rejected for its own
// contents — no ID, no players, an attribute whose kind disagrees with the rule
// set, a team assignment that is missing, unknown, or ambiguous, a team or
// roster over its limit — wraps [ErrInvalidTicket], so a caller fronting an API
// answers all of them as one class of client mistake. A ticket rejected for the
// state of the matchmaker instead reports [ErrDuplicateTicket] or
// [ErrBackfillInProgress] and does not wrap it.
//
// A ticket that leaves the queue keeps its status, its cumulative rule metrics,
// and any claim it holds on a game session's backfill slot, so a caller can
// still read the outcome; nothing releases that state on its own.
// [Matchmaker.Evict] discards it for a ticket that has reached a terminal
// state, which bounds a long-running process's memory by the live queue rather
// than by every ticket it has ever seen.
//
// # Time and expansions
//
// Anything that depends on elapsed time (most importantly the FlexMatch
// "expansions" block) reads the current time through a [Clock]. The default
// is [SystemClock]; tests should pass [WithClock] with a [FakeClock] so they
// can advance time deterministically without sleeping.
//
// # Concurrency
//
// All Matchmaker methods are safe for concurrent use. The queue is protected
// by an internal mutex, so producers may Enqueue/Cancel from any goroutine
// while another goroutine drives Tick. Tick itself is intended to be called
// from a single goroutine — concurrent ticks are safe but compete for the
// same ticket pool.
package flexi
