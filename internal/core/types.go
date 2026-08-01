// Package core holds value types shared between the public flexi package and
// the internal sub-packages, defined here to break an import cycle.
package core

import "time"

type AttributeKind int

const (
	AttrUnknown AttributeKind = iota
	AttrString
	AttrNumber
	AttrStringList
	AttrStringNumberMap
)

type Attribute struct {
	Kind AttributeKind
	S    string
	N    float64
	SL   []string
	SDM  map[string]float64
}

type Attributes map[string]Attribute

type Player struct {
	ID         string
	Attributes Attributes
	Latencies  map[string]int
	// Team is the team the player already occupies in an in-progress match.
	// It mirrors AWS's Player.Team, which is required on every player of a
	// StartMatchBackfill request and must be absent on a regular one.
	Team string
}

type Ticket struct {
	ID         string
	Players    []Player
	EnqueuedAt time.Time
	// Backfill marks a ticket that asks for new players to join an existing
	// match rather than to form a new one. It is set by the public package's
	// EnqueueBackfill; Players then carry the roster already in the match.
	Backfill bool
	// GameSessionID optionally identifies the game session a backfill ticket
	// refills, which is the key FlexMatch uses to keep at most one outstanding
	// backfill request per session.
	GameSessionID string
}

type Match struct {
	Teams                 map[string][]Player
	TicketIDs             []string
	RuleEvaluationMetrics []RuleMetric
}

// RuleMetric aggregates how often a single rule passed or failed during
// matchmaking. It mirrors one entry of FlexMatch's ruleEvaluationMetrics
// array. RuleName matches the name declared in the rule set's rules block.
type RuleMetric struct {
	RuleName    string
	PassedCount int
	FailedCount int
}

// TicketStatus mirrors the FlexMatch ticket lifecycle as documented for
// MatchmakingTicket.Status. See types.go in the public package for user-facing
// docs.
type TicketStatus string

const (
	StatusQueued             TicketStatus = "QUEUED"
	StatusSearching          TicketStatus = "SEARCHING"
	StatusRequiresAcceptance TicketStatus = "REQUIRES_ACCEPTANCE"
	StatusPlacing            TicketStatus = "PLACING"
	StatusCompleted          TicketStatus = "COMPLETED"
	StatusFailed             TicketStatus = "FAILED"
	StatusCancelled          TicketStatus = "CANCELLED"
	StatusTimedOut           TicketStatus = "TIMED_OUT"
)

// StatusReason supplies additional context for a ticket's current status,
// mirroring MatchmakingTicket.StatusReason in the GameLift API. See types.go
// in the public package for user-facing docs.
type StatusReason string

const (
	// StatusReasonAcceptanceFailed marks a ticket that has returned to
	// StatusSearching after a proposed match failed to gather the required
	// acceptances — i.e. a sibling player rejected or let the acceptance
	// window time out. It corresponds to FlexMatch re-entering an accepted
	// ticket into matchmaking after a proposed match fails.
	StatusReasonAcceptanceFailed StatusReason = "ACCEPTANCE_FAILED"
)
