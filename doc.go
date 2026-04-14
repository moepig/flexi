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
// Supported rule set features:
//
//   - Player attribute types: string, number, string_list, string_number_map.
//   - Algorithm strategies: exhaustiveSearch, balanced (with balancedAttribute).
//   - Algorithm batching preferences: largestPopulation, fastestRegion, balanced
//     (parsed; influences team-fill ordering for balanced).
//   - Teams with minPlayers, maxPlayers, and quantity (multi-instance teams).
//   - All seven FlexMatch rule kinds: comparison, distance, absoluteSort,
//     batchDistance, collection, latency, compound.
//   - Time-driven expansions that loosen rule values once a ticket has been
//     waiting long enough.
//
// Backfill of in-progress matches is intentionally out of scope.
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
