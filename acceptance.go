package flexi

import (
	"errors"
	"sort"
	"time"

	"github.com/moepig/flexi/internal/core"
)

// ErrUnknownProposal is returned by Accept / Reject when the ticket is not
// part of any active proposal (either it was never proposed, or the proposal
// has already been resolved).
var ErrUnknownProposal = errors.New("flexi: ticket is not in a pending proposal")

// ErrUnknownPlayer is returned by Accept / Reject when the player id is not
// part of the referenced ticket.
var ErrUnknownPlayer = errors.New("flexi: player is not part of ticket")

// ErrTicketNotPlacing is returned by MarkCompleted when the ticket is not in
// StatusPlacing.
var ErrTicketNotPlacing = errors.New("flexi: ticket is not in PLACING")

// Proposal describes a candidate match awaiting player acceptance. It is
// returned by [Matchmaker.PendingAcceptances] so callers can surface the
// pending decision to their players.
//
// Teams mirrors [Match.Teams] for the candidate. TicketIDs lists every
// ticket participating in the proposal. CreatedAt is the clock time at
// which the proposal was formed; together with the rule set's
// acceptanceTimeoutSeconds it determines when the proposal times out.
type Proposal struct {
	Teams     map[string][]core.Player
	TicketIDs []string
	CreatedAt time.Time
}

// playerAcceptance is the per-player decision within a proposal.
type playerAcceptance int

const (
	acceptPending playerAcceptance = iota
	acceptYes
	acceptNo
)

// proposal is the internal mutable version of [Proposal]. It also tracks the
// tickets (so they can be restored if the proposal survives Cancel on a
// non-member ticket) and the per-player decisions.
type proposal struct {
	teams     map[string][]core.Player
	tickets   []core.Ticket
	ticketIDs []string
	createdAt time.Time
	// decisions[ticketID][playerID] is each player's current decision.
	decisions map[string]map[string]playerAcceptance
}

func newProposal(res matchResult, tickets []core.Ticket, now time.Time) *proposal {
	byID := make(map[string]core.Ticket, len(tickets))
	for _, t := range tickets {
		byID[t.ID] = t
	}
	picked := make([]core.Ticket, 0, len(res.TicketIDs))
	decisions := make(map[string]map[string]playerAcceptance, len(res.TicketIDs))
	for _, id := range res.TicketIDs {
		t := byID[id]
		picked = append(picked, t)
		m := make(map[string]playerAcceptance, len(t.Players))
		for _, p := range t.Players {
			m[p.ID] = acceptPending
		}
		decisions[id] = m
	}
	ids := append([]string(nil), res.TicketIDs...)
	sort.Strings(ids)
	return &proposal{
		teams:     res.Teams,
		tickets:   picked,
		ticketIDs: ids,
		createdAt: now,
		decisions: decisions,
	}
}

// fullyAccepted reports whether every player on every ticket has accepted.
func (p *proposal) fullyAccepted() bool {
	for _, players := range p.decisions {
		for _, d := range players {
			if d != acceptYes {
				return false
			}
		}
	}
	return true
}

// exportTickets returns a copy of the proposal's TicketIDs slice for callers.
func (p *proposal) export() Proposal {
	teams := make(map[string][]core.Player, len(p.teams))
	for k, v := range p.teams {
		cp := make([]core.Player, len(v))
		copy(cp, v)
		teams[k] = cp
	}
	return Proposal{
		Teams:     teams,
		TicketIDs: append([]string(nil), p.ticketIDs...),
		CreatedAt: p.createdAt,
	}
}

// matchResult is the subset of algorithm.Result that acceptance.go needs; it
// is introduced to avoid the acceptance code importing the algorithm package.
type matchResult struct {
	Teams     map[string][]core.Player
	TicketIDs []string
}
