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
}

type Ticket struct {
	ID         string
	Players    []Player
	EnqueuedAt time.Time
}

type Match struct {
	Teams     map[string][]Player
	TicketIDs []string
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
