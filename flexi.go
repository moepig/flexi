package flexi

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/moepig/flexi/internal/algorithm"
	"github.com/moepig/flexi/internal/expansion"
	"github.com/moepig/flexi/internal/queue"
	"github.com/moepig/flexi/internal/rule"
	"github.com/moepig/flexi/internal/ruleset"
)

// ErrInvalidRuleSet is returned by [New] when the rule set JSON is malformed
// or fails semantic validation. It is suitable for use with [errors.Is]:
//
//	if errors.Is(err, flexi.ErrInvalidRuleSet) { ... }
var ErrInvalidRuleSet = ruleset.ErrInvalidRuleSet

// ErrDuplicateTicket is returned by [Matchmaker.Enqueue] when a ticket with
// the same ID is already in the queue.
var ErrDuplicateTicket = queue.ErrDuplicateTicket

// ErrUnknownTicket is returned by [Matchmaker.Cancel] or [Matchmaker.Status]
// when no ticket with the given ID is currently tracked.
var ErrUnknownTicket = queue.ErrUnknownTicket

// Matchmaker forms matches from in-memory tickets according to a parsed
// FlexMatch rule set.
//
// Construct one with [New], add tickets with [Matchmaker.Enqueue], and call
// [Matchmaker.Tick] periodically to drain matches that are now satisfiable.
//
// When the rule set sets acceptanceRequired=true, Tick produces [Proposal]
// candidates instead of final [Match]es. Callers route each proposal through
// [Matchmaker.Accept] / [Matchmaker.Reject] and drive [Matchmaker.Tick]
// again; once every player on every ticket of a proposal has accepted, the
// next Tick returns the corresponding [Match].
//
// Matchmaker has no internal goroutines or timers — all work happens on the
// goroutine that calls Tick. The queue, status map, and proposals are
// protected by a mutex so producers may Enqueue/Cancel/Accept concurrently
// with a ticking loop.
type Matchmaker struct {
	rs    *ruleset.RuleSet
	q     *queue.Queue
	clock Clock

	mu        sync.Mutex
	statuses  map[string]TicketStatus
	proposals []*proposal
	// ticketToProposal indexes a ticket ID to the proposal that currently
	// holds it, so Accept/Reject/Cancel are O(1).
	ticketToProposal map[string]*proposal
}

// Option configures a [Matchmaker] at construction time. Pass any number of
// options to [New].
type Option func(*config)

type config struct {
	clock Clock
}

// WithClock injects a custom [Clock] in place of the default [SystemClock].
// Tests should pass a [FakeClock] so that expansions and ticket wait times
// can be controlled deterministically.
func WithClock(c Clock) Option {
	return func(cfg *config) { cfg.clock = c }
}

// New parses a FlexMatch rule set JSON document and returns a [Matchmaker]
// ready to accept tickets.
//
// rulesetJSON must be the same JSON body accepted by GameLift's
// CreateMatchmakingRuleSet API (the RuleSetBody parameter). Parsing or
// validation failures are reported as errors that wrap [ErrInvalidRuleSet].
//
// Options may be supplied to override defaults; see [WithClock].
func New(rulesetJSON []byte, opts ...Option) (*Matchmaker, error) {
	rs, err := ruleset.Parse(rulesetJSON)
	if err != nil {
		return nil, err
	}
	cfg := config{clock: SystemClock{}}
	for _, o := range opts {
		o(&cfg)
	}
	return &Matchmaker{
		rs:               rs,
		q:                queue.New(),
		clock:            cfg.clock,
		statuses:         make(map[string]TicketStatus),
		ticketToProposal: make(map[string]*proposal),
	}, nil
}

// Enqueue adds t to the matchmaking queue and records its status as
// [StatusQueued].
//
// The ticket's EnqueuedAt field is set from the configured [Clock]; any
// value supplied by the caller is overwritten so that wait-time calculations
// remain consistent. The ticket must have a non-empty ID and at least one
// player; otherwise an error is returned and the ticket is not enqueued.
//
// Enqueue returns [ErrDuplicateTicket] (wrapped) if a ticket with the same
// ID is already queued, in a proposal, or in a recent terminal state.
func (m *Matchmaker) Enqueue(t Ticket) error {
	if t.ID == "" {
		return errors.New("flexi: ticket id is required")
	}
	if len(t.Players) == 0 {
		return errors.New("flexi: ticket must have at least one player")
	}
	t.EnqueuedAt = m.clock.Now()

	m.mu.Lock()
	if _, known := m.statuses[t.ID]; known {
		m.mu.Unlock()
		return ErrDuplicateTicket
	}
	if err := m.q.Enqueue(t); err != nil {
		m.mu.Unlock()
		return err
	}
	m.statuses[t.ID] = StatusQueued
	m.mu.Unlock()
	return nil
}

// Cancel removes the ticket with the given ID from the matchmaker and marks
// it [StatusCancelled]. It returns [ErrUnknownTicket] if no such ticket is
// currently tracked.
//
// If the ticket is part of an active proposal, the entire proposal is torn
// down: every sibling ticket is also marked [StatusCancelled], matching
// FlexMatch's behaviour when any member of a proposed match drops out.
//
// Cancelling a ticket that has already been consumed by a match (and is now
// [StatusPlacing] or [StatusCompleted]) is rejected with [ErrUnknownTicket].
func (m *Matchmaker) Cancel(ticketID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	status, ok := m.statuses[ticketID]
	if !ok {
		return ErrUnknownTicket
	}
	switch status {
	case StatusQueued:
		if err := m.q.Cancel(ticketID); err != nil {
			return err
		}
		m.statuses[ticketID] = StatusCancelled
		return nil
	case StatusRequiresAcceptance:
		p := m.ticketToProposal[ticketID]
		m.dissolveProposal(p, StatusCancelled)
		return nil
	default:
		return ErrUnknownTicket
	}
}

// Pending returns the number of tickets currently in [StatusQueued].
// Tickets held in a proposal (StatusRequiresAcceptance) are not counted.
func (m *Matchmaker) Pending() int { return m.q.Len() }

// Status returns the current [TicketStatus] for ticketID. If no ticket with
// that id is currently tracked by the matchmaker, [ErrUnknownTicket] is
// returned.
//
// Note that the matchmaker retains status for tickets past queue removal
// only while they are in a terminal state reachable from this matchmaker
// (CANCELLED, TIMED_OUT, PLACING, COMPLETED). Eviction of terminal state is
// not automatic; call sites should not rely on long-term retention.
func (m *Matchmaker) Status(ticketID string) (TicketStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.statuses[ticketID]
	if !ok {
		return "", ErrUnknownTicket
	}
	return s, nil
}

// PendingAcceptances returns a snapshot of every proposal currently awaiting
// acceptance. The slice is newly allocated and safe for the caller to mutate.
func (m *Matchmaker) PendingAcceptances() []Proposal {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Proposal, 0, len(m.proposals))
	for _, p := range m.proposals {
		out = append(out, p.export())
	}
	return out
}

// Accept records that playerID, who must be part of the ticket ticketID,
// has accepted the proposed match. Once every player on every ticket in
// the proposal has accepted, the proposal is resolved on the next [Tick]:
// the tickets move to [StatusPlacing] and the corresponding [Match] is
// returned.
//
// Returns [ErrUnknownTicket] if the ticket is not tracked, [ErrUnknownProposal]
// if the ticket is not currently in a pending proposal, or [ErrUnknownPlayer]
// if playerID is not a member of the ticket.
func (m *Matchmaker) Accept(ticketID, playerID string) error {
	return m.record(ticketID, playerID, acceptYes)
}

// Reject records a player's rejection of a proposed match. A single
// rejection dissolves the entire proposal: every ticket in it moves to
// [StatusCancelled], matching the FlexMatch behaviour documented for the
// CANCELLED status.
//
// Errors match [Matchmaker.Accept].
func (m *Matchmaker) Reject(ticketID, playerID string) error {
	return m.record(ticketID, playerID, acceptNo)
}

func (m *Matchmaker) record(ticketID, playerID string, d playerAcceptance) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, known := m.statuses[ticketID]; !known {
		return ErrUnknownTicket
	}
	p, ok := m.ticketToProposal[ticketID]
	if !ok {
		return ErrUnknownProposal
	}
	players, ok := p.decisions[ticketID]
	if !ok {
		return ErrUnknownProposal
	}
	if _, ok := players[playerID]; !ok {
		return ErrUnknownPlayer
	}
	players[playerID] = d
	if d == acceptNo {
		m.dissolveProposal(p, StatusCancelled)
	}
	return nil
}

// MarkCompleted transitions ticketID from [StatusPlacing] to
// [StatusCompleted]. This is the equivalent of a standalone-mode caller
// reporting that they have finished placing the match into a game session
// and connection details are now available on their side.
//
// Returns [ErrUnknownTicket] if the ticket is not tracked or
// [ErrTicketNotPlacing] if it is in any other status.
func (m *Matchmaker) MarkCompleted(ticketID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.statuses[ticketID]
	if !ok {
		return ErrUnknownTicket
	}
	if s != StatusPlacing {
		return ErrTicketNotPlacing
	}
	m.statuses[ticketID] = StatusCompleted
	return nil
}

// Tick drives the matchmaker forward by one step.
//
// In order, Tick:
//  1. Expires any proposals whose acceptanceTimeoutSeconds has elapsed,
//     moving their tickets to [StatusTimedOut].
//  2. Resolves any proposals that have been fully accepted, moving tickets
//     to [StatusPlacing] and returning the corresponding [Match] values.
//  3. Applies FlexMatch expansions based on the oldest queued ticket's
//     wait time.
//  4. Runs the matching algorithm over the remaining queued tickets.
//     When acceptanceRequired=false, each result becomes a [Match] returned
//     in this tick; when true, each result is held as a new proposal and
//     its tickets move to [StatusRequiresAcceptance].
//
// Tick returns nil, nil when nothing resolves in this step. A non-nil error
// indicates a configuration problem (for example, an expansion targeting a
// field that no longer exists).
func (m *Matchmaker) Tick() ([]Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock.Now()
	m.expireProposals(now)
	matches := m.resolveAcceptedProposals()

	tickets := m.q.Snapshot()
	if len(tickets) == 0 {
		if len(matches) == 0 {
			return nil, nil
		}
		return matches, nil
	}

	oldest := tickets[0].EnqueuedAt
	for _, t := range tickets[1:] {
		if t.EnqueuedAt.Before(oldest) {
			oldest = t.EnqueuedAt
		}
	}
	elapsed := now.Sub(oldest)

	rs, err := expansion.Apply(m.rs, elapsed)
	if err != nil {
		return nil, fmt.Errorf("flexi: apply expansions: %w", err)
	}

	evals, err := buildEvaluators(rs)
	if err != nil {
		return nil, err
	}

	results, _ := algorithm.Build(rs, evals, tickets)
	if len(results) == 0 {
		if len(matches) == 0 {
			return nil, nil
		}
		return matches, nil
	}

	if m.rs.AcceptanceRequired {
		for _, r := range results {
			p := newProposal(matchResult{Teams: r.Teams, TicketIDs: r.TicketIDs}, tickets, now)
			m.proposals = append(m.proposals, p)
			for _, id := range r.TicketIDs {
				m.statuses[id] = StatusRequiresAcceptance
				m.ticketToProposal[id] = p
			}
			m.q.Remove(r.TicketIDs)
		}
		if len(matches) == 0 {
			return nil, nil
		}
		return matches, nil
	}

	for _, r := range results {
		matches = append(matches, Match{Teams: r.Teams, TicketIDs: r.TicketIDs})
		for _, id := range r.TicketIDs {
			m.statuses[id] = StatusPlacing
		}
		m.q.Remove(r.TicketIDs)
	}
	return matches, nil
}

// expireProposals moves timed-out proposals' tickets to StatusTimedOut and
// removes those proposals. Callers must hold m.mu.
func (m *Matchmaker) expireProposals(now time.Time) {
	if m.rs.AcceptanceTimeoutSeconds <= 0 || len(m.proposals) == 0 {
		return
	}
	deadline := time.Duration(m.rs.AcceptanceTimeoutSeconds) * time.Second
	kept := m.proposals[:0]
	for _, p := range m.proposals {
		if now.Sub(p.createdAt) >= deadline {
			m.markProposalTickets(p, StatusTimedOut)
			continue
		}
		kept = append(kept, p)
	}
	m.proposals = kept
}

// resolveAcceptedProposals promotes fully-accepted proposals to matches.
// Callers must hold m.mu.
func (m *Matchmaker) resolveAcceptedProposals() []Match {
	if len(m.proposals) == 0 {
		return nil
	}
	kept := m.proposals[:0]
	var out []Match
	for _, p := range m.proposals {
		if !p.fullyAccepted() {
			kept = append(kept, p)
			continue
		}
		out = append(out, Match{
			Teams:     p.teams,
			TicketIDs: append([]string(nil), p.ticketIDs...),
		})
		for _, id := range p.ticketIDs {
			m.statuses[id] = StatusPlacing
			delete(m.ticketToProposal, id)
		}
	}
	m.proposals = kept
	return out
}

// dissolveProposal removes p and transitions each of its tickets to status.
// Used by Cancel (StatusCancelled) and Reject (StatusCancelled).
// Callers must hold m.mu.
func (m *Matchmaker) dissolveProposal(p *proposal, status TicketStatus) {
	m.markProposalTickets(p, status)
	kept := m.proposals[:0]
	for _, q := range m.proposals {
		if q != p {
			kept = append(kept, q)
		}
	}
	m.proposals = kept
}

// markProposalTickets sets the terminal status for every ticket in p and
// clears its ticketToProposal entries. Callers must hold m.mu.
func (m *Matchmaker) markProposalTickets(p *proposal, status TicketStatus) {
	for _, id := range p.ticketIDs {
		m.statuses[id] = status
		delete(m.ticketToProposal, id)
	}
}

// buildEvaluators constructs evaluators in two passes so that compound rules
// can resolve references to siblings that appear later in the rule list.
func buildEvaluators(rs *ruleset.RuleSet) ([]rule.Evaluator, error) {
	others := make(map[string]rule.Evaluator, len(rs.Rules))
	for i := range rs.Rules {
		if rs.Rules[i].Type == ruleset.RuleCompound {
			continue
		}
		ev, err := rule.Build(&rs.Rules[i], others)
		if err != nil {
			return nil, fmt.Errorf("flexi: build rule %q: %w", rs.Rules[i].Name, err)
		}
		others[rs.Rules[i].Name] = ev
	}
	for i := range rs.Rules {
		if rs.Rules[i].Type != ruleset.RuleCompound {
			continue
		}
		ev, err := rule.Build(&rs.Rules[i], others)
		if err != nil {
			return nil, fmt.Errorf("flexi: build compound %q: %w", rs.Rules[i].Name, err)
		}
		others[rs.Rules[i].Name] = ev
	}
	out := make([]rule.Evaluator, 0, len(rs.Rules))
	for i := range rs.Rules {
		out = append(out, others[rs.Rules[i].Name])
	}
	return out, nil
}
