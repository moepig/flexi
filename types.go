package flexi

import "github.com/moepig/flexi/internal/core"

// Player is a single participant in a [Ticket]. ID identifies the player
// within a match (it is echoed back in [Match].Teams). Attributes carry the
// values referenced by rule expressions; their keys must match names declared
// in the rule set's playerAttributes block. Latencies maps an AWS region
// name to the player's measured latency in milliseconds; it is consulted by
// latency rules.
//
// Players are passed by value and may be safely re-used across tickets.
type Player = core.Player

// Ticket is a matchmaking request submitted to the engine. A ticket may
// contain a single player (a "solo" request) or several players who must be
// matched together as a party.
//
// ID must be unique among queued tickets. EnqueuedAt is filled in by
// [Matchmaker.Enqueue] from the configured [Clock]; any value set by the
// caller is overwritten.
type Ticket = core.Ticket

// Match is a successful pairing of tickets into a complete game.
//
// Teams maps the team name (as it appeared in the rule set) to the players
// assigned to that team. When a rule set declares quantity > 1 for a team,
// the resulting names are suffixed with "_1", "_2", and so on. TicketIDs
// lists every ticket consumed to form the match, sorted lexicographically
// for stable test output.
type Match = core.Match
