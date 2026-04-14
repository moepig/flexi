package flexi

import (
	"errors"
	"fmt"

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

// ErrUnknownTicket is returned by [Matchmaker.Cancel] when no ticket with
// the given ID is in the queue.
var ErrUnknownTicket = queue.ErrUnknownTicket

// Matchmaker forms matches from in-memory tickets according to a parsed
// FlexMatch rule set.
//
// Construct one with [New], add tickets with [Matchmaker.Enqueue], and call
// [Matchmaker.Tick] periodically to drain matches that are now satisfiable.
//
// Matchmaker has no internal goroutines or timers — all work happens on the
// goroutine that calls Tick. The queue and tick path are protected by a
// mutex so producers may Enqueue/Cancel concurrently with a ticking loop.
type Matchmaker struct {
	rs    *ruleset.RuleSet
	q     *queue.Queue
	clock Clock
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
	return &Matchmaker{rs: rs, q: queue.New(), clock: cfg.clock}, nil
}

// Enqueue adds t to the matchmaking queue.
//
// The ticket's EnqueuedAt field is set from the configured [Clock]; any
// value supplied by the caller is overwritten so that wait-time calculations
// remain consistent. The ticket must have a non-empty ID and at least one
// player; otherwise an error is returned and the ticket is not enqueued.
//
// Enqueue returns [ErrDuplicateTicket] (wrapped) if a ticket with the same
// ID is already queued.
func (m *Matchmaker) Enqueue(t Ticket) error {
	if t.ID == "" {
		return errors.New("flexi: ticket id is required")
	}
	if len(t.Players) == 0 {
		return errors.New("flexi: ticket must have at least one player")
	}
	t.EnqueuedAt = m.clock.Now()
	return m.q.Enqueue(t)
}

// Cancel removes the queued ticket with the given ID. It returns
// [ErrUnknownTicket] if no such ticket is currently queued.
//
// Cancelling a ticket that has already been consumed by a match has no
// effect on the match itself; the returned error simply reflects that the
// ticket is no longer queued.
func (m *Matchmaker) Cancel(ticketID string) error { return m.q.Cancel(ticketID) }

// Pending returns the number of tickets currently waiting in the queue.
func (m *Matchmaker) Pending() int { return m.q.Len() }

// Tick attempts to form as many matches as it can from the current queue and
// returns them. Tickets consumed by a returned match are removed from the
// queue atomically with that match's formation; tickets that did not match
// remain queued for a future Tick.
//
// Before evaluating rules, Tick computes how long the oldest queued ticket
// has been waiting (according to the configured [Clock]) and applies any
// FlexMatch expansion steps whose waitTimeSeconds threshold has been
// reached. The original rule set is not mutated; expansions produce a
// per-tick working copy.
//
// Tick returns nil, nil when the queue is empty or when no matches can be
// formed at the current time. A non-nil error indicates a configuration
// problem (for example, an expansion targets a field that no longer exists)
// rather than a "no match" outcome.
func (m *Matchmaker) Tick() ([]Match, error) {
	tickets := m.q.Snapshot()
	if len(tickets) == 0 {
		return nil, nil
	}

	now := m.clock.Now()
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
		return nil, nil
	}

	matches := make([]Match, 0, len(results))
	for _, r := range results {
		matches = append(matches, Match{Teams: r.Teams, TicketIDs: r.TicketIDs})
		m.q.Remove(r.TicketIDs)
	}
	return matches, nil
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
