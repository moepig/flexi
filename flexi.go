package flexi

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/moepig/flexi/internal/algorithm"
	"github.com/moepig/flexi/internal/core"
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
// If a proposed match fails acceptance — a player rejects, or the
// acceptanceTimeoutSeconds window elapses — flexi mirrors AWS FlexMatch:
// tickets on which every player had accepted return to the queue in
// [StatusSearching] (carrying [StatusReasonAcceptanceFailed]) for the next Tick
// to re-match, while the ticket(s) that caused the failure (a player who
// rejected or never responded) move to [StatusCancelled].
//
// Independently, a ticket that stays in matchmaking longer than the rule set's
// requestTimeoutSeconds fails with [StatusTimedOut]. This request-level
// deadline applies to queued and re-queued tickets regardless of
// acceptanceRequired.
//
// Matchmaker has no internal goroutines or timers — all work happens on the
// goroutine that calls Tick. The queue, status map, and proposals are
// protected by a mutex so producers may Enqueue/Cancel/Accept concurrently
// with a ticking loop.
type Matchmaker struct {
	rs    *ruleset.RuleSet
	q     *queue.Queue
	clock Clock
	// defaults holds parsed playerAttributes defaults, applied to players that
	// omit a declared attribute when they are enqueued.
	defaults map[string]core.Attribute
	// attrKinds maps each declared playerAttribute name to its expected kind, so
	// Enqueue can reject tickets whose values disagree with the rule set's
	// declared types. Attributes not declared in the rule set are not checked.
	attrKinds map[string]core.AttributeKind

	mu        sync.Mutex
	statuses  map[string]TicketStatus
	proposals []*proposal
	// ticketToProposal indexes a ticket ID to the proposal that currently
	// holds it, so Accept/Reject/Cancel are O(1).
	ticketToProposal map[string]*proposal
	// ruleMetrics holds the cumulative per-ticket rule-evaluation metrics
	// accumulated across every Tick a ticket participated in. Like statuses it
	// is retained through terminal states so timed-out / cancelled tickets can
	// still be queried.
	ruleMetrics map[string][]core.RuleMetric
	// statusReasons holds the supplementary StatusReason for tickets that
	// currently carry one (only acceptance-failure re-queues today). Entries
	// are only meaningful while the ticket is in StatusSearching; stale entries
	// are ignored by StatusReason.
	statusReasons map[string]StatusReason
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
	defaults, err := parseDefaults(rs)
	if err != nil {
		return nil, err
	}
	return &Matchmaker{
		rs:               rs,
		q:                queue.New(),
		clock:            cfg.clock,
		defaults:         defaults,
		attrKinds:        buildAttrKinds(rs),
		statuses:         make(map[string]TicketStatus),
		ticketToProposal: make(map[string]*proposal),
		ruleMetrics:      make(map[string][]core.RuleMetric),
		statusReasons:    make(map[string]StatusReason),
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
	if err := m.checkAttributeTypes(t); err != nil {
		return err
	}
	t = m.applyDefaults(t)
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

// buildAttrKinds maps each declared playerAttribute name to the AttributeKind
// implied by its declared type. Unknown type strings are skipped; the rule set
// has already been validated to use only the four supported types.
func buildAttrKinds(rs *ruleset.RuleSet) map[string]core.AttributeKind {
	out := make(map[string]core.AttributeKind, len(rs.PlayerAttributes))
	for _, pa := range rs.PlayerAttributes {
		switch pa.Type {
		case "string":
			out[pa.Name] = core.AttrString
		case "number":
			out[pa.Name] = core.AttrNumber
		case "string_list":
			out[pa.Name] = core.AttrStringList
		case "string_number_map":
			out[pa.Name] = core.AttrStringNumberMap
		}
	}
	return out
}

// checkAttributeTypes rejects a ticket if any player supplies a value for a
// declared attribute whose kind disagrees with the rule set's declared type.
// Attributes not declared in the rule set are passed through unchecked, mirroring
// FlexMatch, which carries undeclared player data without using it in matching.
func (m *Matchmaker) checkAttributeTypes(t Ticket) error {
	if len(m.attrKinds) == 0 {
		return nil
	}
	for _, p := range t.Players {
		for name, a := range p.Attributes {
			want, declared := m.attrKinds[name]
			if !declared {
				continue
			}
			if a.Kind != want {
				return fmt.Errorf("flexi: player %q attribute %q has kind %v, but the rule set declares %v",
					p.ID, name, a.Kind, want)
			}
		}
	}
	return nil
}

// parseDefaults reads the rule set's playerAttributes defaults into Attribute
// values keyed by attribute name. Attributes without a default are skipped.
func parseDefaults(rs *ruleset.RuleSet) (map[string]core.Attribute, error) {
	out := make(map[string]core.Attribute)
	for _, pa := range rs.PlayerAttributes {
		if len(pa.Default) == 0 {
			continue
		}
		var a core.Attribute
		switch pa.Type {
		case "string":
			a.Kind = core.AttrString
			if err := json.Unmarshal(pa.Default, &a.S); err != nil {
				return nil, fmt.Errorf("flexi: playerAttribute %q default: %w", pa.Name, err)
			}
		case "number":
			a.Kind = core.AttrNumber
			if err := json.Unmarshal(pa.Default, &a.N); err != nil {
				return nil, fmt.Errorf("flexi: playerAttribute %q default: %w", pa.Name, err)
			}
		case "string_list":
			a.Kind = core.AttrStringList
			if err := json.Unmarshal(pa.Default, &a.SL); err != nil {
				return nil, fmt.Errorf("flexi: playerAttribute %q default: %w", pa.Name, err)
			}
		case "string_number_map":
			a.Kind = core.AttrStringNumberMap
			if err := json.Unmarshal(pa.Default, &a.SDM); err != nil {
				return nil, fmt.Errorf("flexi: playerAttribute %q default: %w", pa.Name, err)
			}
		default:
			continue
		}
		out[pa.Name] = a
	}
	return out, nil
}

// applyDefaults returns a copy of t in which any player missing a declared
// attribute that has a default value has that default filled in.
func (m *Matchmaker) applyDefaults(t Ticket) Ticket {
	if len(m.defaults) == 0 {
		return t
	}
	players := make([]core.Player, len(t.Players))
	for i, p := range t.Players {
		players[i] = p
		var attrs core.Attributes
		for name, def := range m.defaults {
			if _, ok := p.Attributes[name]; ok {
				continue
			}
			if attrs == nil {
				attrs = make(core.Attributes, len(p.Attributes)+1)
				for k, v := range p.Attributes {
					attrs[k] = v
				}
			}
			attrs[name] = def
		}
		if attrs != nil {
			players[i].Attributes = attrs
		}
	}
	t.Players = players
	return t
}

// Cancel removes the ticket with the given ID from the matchmaker and marks
// it [StatusCancelled]. It returns [ErrUnknownTicket] if no such ticket is
// currently tracked. A ticket that is queued ([StatusQueued]) or has been
// re-queued after a failed acceptance ([StatusSearching]) is removed directly.
//
// If the ticket is part of an active proposal, the entire proposal is torn
// down: every sibling ticket is also marked [StatusCancelled], matching
// FlexMatch's behaviour when any member of a proposed match drops out. Unlike
// a reject or acceptance timeout, a user-initiated cancel never re-queues a
// sibling, even one that had already accepted.
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
	case StatusQueued, StatusSearching:
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

// StatusReason returns the supplementary [StatusReason] for ticketID together
// with true when one applies, and "", false otherwise.
//
// A reason is currently set only while a ticket is in [StatusSearching] after a
// proposed match it had accepted failed to gather every required acceptance; in
// that case the reason is [StatusReasonAcceptanceFailed]. This lets a caller
// polling [Matchmaker.Status] distinguish a ticket re-entering matchmaking after
// a failed acceptance (FlexMatch's MatchmakingSearching with a status reason)
// from one that was freshly enqueued. The reason clears as soon as the ticket
// leaves StatusSearching (for example when the next Tick re-matches it).
func (m *Matchmaker) StatusReason(ticketID string) (StatusReason, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.statuses[ticketID] != StatusSearching {
		return "", false
	}
	r, ok := m.statusReasons[ticketID]
	return r, ok
}

// RuleMetrics returns the cumulative rule-evaluation metrics accumulated for
// ticketID across every Tick in which it participated in match formation,
// together with true. If the ticket has never been involved in an evaluation
// (for example it was cancelled while still only queued, or no such ticket is
// tracked), it returns nil and false.
//
// The metrics support FlexMatch's MatchmakingTimedOut and MatchmakingCancelled
// parity: like [Status], they are retained through terminal states (TIMED_OUT,
// CANCELLED, PLACING, COMPLETED) and are not evicted automatically. Each entry
// is named after a top-level rule in the rule set; the returned slice is a copy
// the caller may mutate freely.
func (m *Matchmaker) RuleMetrics(ticketID string) ([]RuleMetric, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mtr, ok := m.ruleMetrics[ticketID]
	if !ok {
		return nil, false
	}
	return append([]core.RuleMetric(nil), mtr...), true
}

// mergeMetrics adds src's per-rule counts into dst by rule name, preserving
// dst's existing order and appending names not yet present. dst may be nil.
func mergeMetrics(dst, src []core.RuleMetric) []core.RuleMetric {
	if len(src) == 0 {
		return dst
	}
	idx := make(map[string]int, len(dst))
	for i, mtr := range dst {
		idx[mtr.RuleName] = i
	}
	for _, s := range src {
		if i, ok := idx[s.RuleName]; ok {
			dst[i].PassedCount += s.PassedCount
			dst[i].FailedCount += s.FailedCount
			continue
		}
		idx[s.RuleName] = len(dst)
		dst = append(dst, s)
	}
	return dst
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

// Reject records a player's rejection of a proposed match, ending the
// proposal. The proposal's tickets are then split following AWS FlexMatch: any
// ticket on which every player had already accepted is returned to the queue in
// [StatusSearching] (carrying [StatusReasonAcceptanceFailed]) so the next
// [Matchmaker.Tick] can re-match it, while every other ticket — including the
// one carrying the rejecting player and any whose players never responded —
// moves to [StatusCancelled]. An acceptance timeout (see [Matchmaker.Tick])
// splits the proposal the same way.
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
		m.failProposal(p)
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
//  1. Expires any proposals whose acceptanceTimeoutSeconds has elapsed: their
//     fully-accepted tickets return to the queue in [StatusSearching] and the
//     rest move to [StatusCancelled].
//  2. Resolves any proposals that have been fully accepted, moving tickets
//     to [StatusPlacing] and returning the corresponding [Match] values.
//  3. Fails any queued or re-queued ticket that has been in matchmaking longer
//     than requestTimeoutSeconds, moving it to [StatusTimedOut].
//  4. Applies FlexMatch expansions based on the oldest queued ticket's
//     wait time.
//  5. Runs the matching algorithm over the remaining queued tickets.
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
	m.expireRequests(now)

	tickets := m.q.Snapshot()
	if len(tickets) == 0 {
		if len(matches) == 0 {
			return nil, nil
		}
		return matches, nil
	}

	// Expansion wait time is measured against either the oldest or the newest
	// queued ticket, per algorithm.expansionAgeSelection. AWS defaults to
	// "newest" (the clock restarts whenever a newer ticket joins, so expansions
	// trigger more slowly); "oldest" triggers more quickly.
	oldest := tickets[0].EnqueuedAt
	newest := tickets[0].EnqueuedAt
	for _, t := range tickets[1:] {
		if t.EnqueuedAt.Before(oldest) {
			oldest = t.EnqueuedAt
		}
		if t.EnqueuedAt.After(newest) {
			newest = t.EnqueuedAt
		}
	}
	ref := newest
	if m.rs.Algorithm.ExpansionAgeSelection == "oldest" {
		ref = oldest
	}
	elapsed := now.Sub(ref)

	rs, err := expansion.Apply(m.rs, elapsed)
	if err != nil {
		return nil, fmt.Errorf("flexi: apply expansions: %w", err)
	}

	evals, err := buildEvaluators(rs)
	if err != nil {
		return nil, err
	}

	results, _, tickMetrics := algorithm.Build(rs, evals, tickets)
	// Accumulate this tick's metrics onto every participating ticket before
	// branching, so tickets that go on to time out or be cancelled keep them.
	for id, mtr := range tickMetrics {
		m.ruleMetrics[id] = mergeMetrics(m.ruleMetrics[id], mtr)
	}
	if len(results) == 0 {
		if len(matches) == 0 {
			return nil, nil
		}
		return matches, nil
	}

	if m.rs.AcceptanceRequired {
		for _, r := range results {
			p := newProposal(matchResult{
				Teams:                 r.Teams,
				TicketIDs:             r.TicketIDs,
				RuleEvaluationMetrics: r.RuleEvaluationMetrics,
			}, tickets, now)
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
		matches = append(matches, Match{
			Teams:                 r.Teams,
			TicketIDs:             r.TicketIDs,
			RuleEvaluationMetrics: r.RuleEvaluationMetrics,
		})
		for _, id := range r.TicketIDs {
			m.statuses[id] = StatusPlacing
		}
		m.q.Remove(r.TicketIDs)
	}
	return matches, nil
}

// expireProposals discards proposals whose acceptance window has elapsed,
// splitting their tickets the same way a reject does (fully-accepted tickets
// return to the queue in StatusSearching; the rest move to StatusCancelled),
// and removes those proposals. Callers must hold m.mu.
func (m *Matchmaker) expireProposals(now time.Time) {
	if m.rs.AcceptanceTimeoutSeconds <= 0 || len(m.proposals) == 0 {
		return
	}
	deadline := time.Duration(m.rs.AcceptanceTimeoutSeconds) * time.Second
	kept := m.proposals[:0]
	for _, p := range m.proposals {
		if now.Sub(p.createdAt) >= deadline {
			m.failProposalTickets(p)
			continue
		}
		kept = append(kept, p)
	}
	m.proposals = kept
}

// expireRequests fails any queued or re-queued (SEARCHING) ticket that has been
// in matchmaking longer than the rule set's requestTimeoutSeconds, moving it to
// StatusTimedOut and removing it from the queue. The wait is measured from the
// ticket's original EnqueuedAt, so a ticket re-queued after a failed acceptance
// still counts the time it spent before the proposal. Tickets currently held in
// a proposal (REQUIRES_ACCEPTANCE) are governed by acceptanceTimeoutSeconds, not
// this deadline. Callers must hold m.mu.
func (m *Matchmaker) expireRequests(now time.Time) {
	if m.rs.RequestTimeoutSeconds <= 0 {
		return
	}
	deadline := time.Duration(m.rs.RequestTimeoutSeconds) * time.Second
	var expired []string
	for _, t := range m.q.Snapshot() {
		if now.Sub(t.EnqueuedAt) >= deadline {
			expired = append(expired, t.ID)
		}
	}
	if len(expired) == 0 {
		return
	}
	m.q.Remove(expired)
	for _, id := range expired {
		m.statuses[id] = StatusTimedOut
		delete(m.statusReasons, id)
	}
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
			Teams:                 p.teams,
			TicketIDs:             append([]string(nil), p.ticketIDs...),
			RuleEvaluationMetrics: append([]core.RuleMetric(nil), p.ruleEvaluationMetrics...),
		})
		for _, id := range p.ticketIDs {
			m.statuses[id] = StatusPlacing
			delete(m.ticketToProposal, id)
		}
	}
	m.proposals = kept
	return out
}

// dissolveProposal removes p and transitions every one of its tickets to
// status. Used by Cancel, where a user-initiated cancel terminates the whole
// proposed match regardless of per-player acceptance. Callers must hold m.mu.
func (m *Matchmaker) dissolveProposal(p *proposal, status TicketStatus) {
	m.markProposalTickets(p, status)
	m.removeProposal(p)
}

// failProposal handles a proposal that failed acceptance — a reject or an
// acceptance timeout — splitting its tickets via failProposalTickets and then
// removing the proposal. Callers must hold m.mu.
func (m *Matchmaker) failProposal(p *proposal) {
	m.failProposalTickets(p)
	m.removeProposal(p)
}

// failProposalTickets disposes of an acceptance-failed proposal's tickets but,
// unlike failProposal, leaves the m.proposals slice untouched so callers that
// rebuild it in a loop (expireProposals) can manage removal themselves.
//
// Following AWS FlexMatch, every ticket on which all players had accepted is
// returned to the queue in StatusSearching with StatusReasonAcceptanceFailed,
// and the remaining tickets — those holding a player who rejected or never
// responded to the proposed match — move to StatusCancelled. (FlexMatch reserves
// TIMED_OUT for the request-level RequestTimeoutSeconds, handled separately in
// expireRequests, not for acceptance failures.) Callers must hold m.mu.
func (m *Matchmaker) failProposalTickets(p *proposal) {
	byID := make(map[string]core.Ticket, len(p.tickets))
	for _, t := range p.tickets {
		byID[t.ID] = t
	}
	for _, id := range p.ticketIDs {
		delete(m.ticketToProposal, id)
		if p.ticketAccepted(id) {
			m.statuses[id] = StatusSearching
			m.statusReasons[id] = StatusReasonAcceptanceFailed
			// Return the ticket to the queue (preserving its original
			// EnqueuedAt so wait-time expansions keep accruing) for the next
			// Tick to re-match. The ticket was removed from the queue when the
			// proposal formed, so a duplicate here is not expected.
			_ = m.q.Enqueue(byID[id])
			continue
		}
		m.statuses[id] = StatusCancelled
		delete(m.statusReasons, id)
	}
}

// markProposalTickets sets the terminal status for every ticket in p and
// clears its ticketToProposal entries. Callers must hold m.mu.
func (m *Matchmaker) markProposalTickets(p *proposal, status TicketStatus) {
	for _, id := range p.ticketIDs {
		m.statuses[id] = status
		delete(m.ticketToProposal, id)
	}
}

// removeProposal drops p from the pending proposals slice. Callers must hold
// m.mu.
func (m *Matchmaker) removeProposal(p *proposal) {
	kept := m.proposals[:0]
	for _, q := range m.proposals {
		if q != p {
			kept = append(kept, q)
		}
	}
	m.proposals = kept
}

// buildEvaluators constructs evaluators in two passes so that compound rules
// can resolve references to siblings that appear later in the rule list.
//
// A rule that is referenced by a compound rule's statement is NOT returned as a
// top-level evaluator: AWS FlexMatch evaluates such a rule only as part of the
// compound that references it, never standalone. Were it enforced on its own,
// a statement like or(A, B) would collapse to "A and B" (both would have to
// pass independently), defeating the compound's logic. The referenced rule is
// still built and kept in the lookup map so the compound can evaluate it.
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
	referenced := compoundReferencedRules(rs)
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
		if _, ref := referenced[rs.Rules[i].Name]; ref {
			continue
		}
		out = append(out, others[rs.Rules[i].Name])
	}
	return out, nil
}

// compoundReferencedRules returns the set of rule names that appear inside any
// compound rule's statement. Statements have already been validated by
// ruleset.Parse, so a parse failure here is treated as "no references".
func compoundReferencedRules(rs *ruleset.RuleSet) map[string]struct{} {
	referenced := make(map[string]struct{})
	for i := range rs.Rules {
		if rs.Rules[i].Type != ruleset.RuleCompound {
			continue
		}
		node, err := ruleset.ParseCompound(rs.Rules[i].Statement)
		if err != nil {
			continue
		}
		for _, name := range node.RuleNames() {
			referenced[name] = struct{}{}
		}
	}
	return referenced
}
