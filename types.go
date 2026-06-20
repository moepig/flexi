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

// RuleMetric reports how many times a rule passed or failed while
// matchmaking processed a set of tickets. It corresponds to one entry of the
// ruleEvaluationMetrics array in FlexMatch's PotentialMatchCreated,
// MatchmakingTimedOut, and MatchmakingCancelled events. RuleName matches a
// rule name declared in the rule set. It is exposed on [Match] and [Proposal]
// for a formed/candidate match, and via [Matchmaker.RuleMetrics] for the
// cumulative per-ticket totals.
type RuleMetric = core.RuleMetric

// TicketStatus is the FlexMatch-compatible ticket lifecycle state.
//
// Values mirror the MatchmakingTicket.Status values in the AWS GameLift API.
// Because this library runs FlexMatch in standalone mode (no game session
// placement), the terminal success status is [StatusPlacing]; callers that
// have attached connection information to the ticket may promote it to
// [StatusCompleted] via [Matchmaker.MarkCompleted].
//
// Transitions:
//   - Enqueue                         → StatusQueued
//   - Tick forms a candidate (acceptanceRequired=true)
//                                     → StatusRequiresAcceptance
//   - All players Accept              → StatusPlacing (Match returned)
//   - acceptanceRequired=false + Tick → StatusPlacing (Match returned)
//   - MarkCompleted                   → StatusCompleted
//   - Cancel on queued / searching    → StatusCancelled
//   - Reject: rejecting / non-accepting ticket → StatusCancelled;
//     fully-accepted siblings → StatusSearching (re-queued)
//   - Acceptance timeout: non-accepting ticket → StatusTimedOut;
//     fully-accepted siblings → StatusSearching (re-queued)
//
// StatusSearching is assigned to a ticket that is returned to the queue after a
// proposed match it accepted failed to gather every required acceptance (see
// [Matchmaker.StatusReason]); such a ticket is re-considered by the next
// [Matchmaker.Tick]. StatusFailed is defined for parity with FlexMatch but is
// not produced by the current implementation.
type TicketStatus = core.TicketStatus

const (
	StatusQueued             = core.StatusQueued
	StatusSearching          = core.StatusSearching
	StatusRequiresAcceptance = core.StatusRequiresAcceptance
	StatusPlacing            = core.StatusPlacing
	StatusCompleted          = core.StatusCompleted
	StatusFailed             = core.StatusFailed
	StatusCancelled          = core.StatusCancelled
	StatusTimedOut           = core.StatusTimedOut
)

// StatusReason gives supplementary context for a ticket's status, mirroring
// MatchmakingTicket.StatusReason in the GameLift API. It is reported by
// [Matchmaker.StatusReason].
type StatusReason = core.StatusReason

const (
	// StatusReasonAcceptanceFailed is set on a ticket that has returned to
	// [StatusSearching] because a proposed match it had accepted failed to
	// gather every required acceptance (a sibling player rejected or timed
	// out). It corresponds to FlexMatch's MatchmakingSearching event being
	// re-emitted with a status reason after a proposed match fails.
	StatusReasonAcceptanceFailed = core.StatusReasonAcceptanceFailed
)
