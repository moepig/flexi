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
