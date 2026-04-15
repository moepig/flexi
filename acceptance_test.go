package flexi_test

import (
	"errors"
	"testing"
	"time"

	"github.com/moepig/flexi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rule set equivalent to skillRS but requiring acceptance, with a 60s timeout.
const acceptRS = `{
  "name": "skill-balance-accept",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [{"name": "skill", "type": "number"}],
  "teams": [
    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
  ],
  "rules": [
    {"name": "FairSkill", "type": "distance",
     "measurements": ["avg(teams[red].players.skill)"],
     "referenceValue": "avg(teams[blue].players.skill)",
     "maxDistance": 10}
  ],
  "acceptanceRequired": true,
  "acceptanceTimeoutSeconds": 60
}`

func enqueueQuartet(t *testing.T, mm *flexi.Matchmaker) {
	t.Helper()
	for _, tk := range []flexi.Ticket{solo("a", 50), solo("b", 52), solo("c", 49), solo("d", 51)} {
		require.NoError(t, mm.Enqueue(tk))
	}
}

// --- Status tests -----------------------------------------------------------

// Purpose: Verify that a ticket is observable as StatusQueued immediately after Enqueue.
// Method:  Enqueue one ticket into an empty Matchmaker, then call Status right away.
// Expect:  Status returns StatusQueued with no error.
func TestStatus_QueuedAfterEnqueue(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 50)))

	s, err := mm.Status("a")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusQueued, s)
}

// Purpose: Verify that querying Status for an unknown ticket ID returns an error.
// Method:  Call Status("missing") on a Matchmaker with no enqueued tickets.
// Expect:  ErrUnknownTicket is returned.
func TestStatus_UnknownTicket(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	_, err = mm.Status("missing")
	assert.ErrorIs(t, err, flexi.ErrUnknownTicket)
}

// Purpose: Verify that a cancelled ticket retains StatusCancelled after removal from the queue.
// Method:  Enqueue one ticket, Cancel it, then query Status.
// Expect:  StatusCancelled is returned (the status remains queryable even after queue removal).
func TestStatus_CancelledAfterCancel(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 50)))
	require.NoError(t, mm.Cancel("a"))

	s, err := mm.Status("a")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusCancelled, s)
}

// Purpose: Verify that tickets transition to StatusPlacing after a match forms without acceptance.
// Method:  Enqueue four tickets into skillRS, call Tick to form a match, then check every ticket's Status.
// Expect:  All four tickets are StatusPlacing (the FlexMatch STANDALONE terminal state).
func TestStatus_PlacingAfterMatchWithoutAcceptance(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)

	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)

	for _, id := range []string{"a", "b", "c", "d"} {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equalf(t, flexi.StatusPlacing, s, "ticket %s", id)
	}
}

// Purpose: Verify that MarkCompleted promotes a PLACING ticket to COMPLETED.
// Method:  Form a match (tickets become PLACING), then call MarkCompleted on one ticket.
// Expect:  Status transitions to StatusCompleted.
func TestMarkCompleted_PromotesPlacingToCompleted(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)
	_, err = mm.Tick()
	require.NoError(t, err)

	require.NoError(t, mm.MarkCompleted("a"))
	s, err := mm.Status("a")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusCompleted, s)
}

// Purpose: Verify that MarkCompleted rejects promotion from any state other than PLACING.
// Method:  Call MarkCompleted on a ticket that is still QUEUED (immediately after Enqueue).
// Expect:  ErrTicketNotPlacing is returned.
func TestMarkCompleted_RejectsNonPlacing(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 50)))

	err = mm.MarkCompleted("a")
	assert.ErrorIs(t, err, flexi.ErrTicketNotPlacing)
}

// --- Acceptance happy path --------------------------------------------------

// Purpose: Verify the full acceptance happy path — all players accept and the next Tick returns a Match.
// Method:  Enqueue four tickets → Tick (creates Proposal) → all Accept → Tick again.
//
//	Check Status and PendingAcceptances at each stage.
//
// Expect:  First Tick: 0 matches, all tickets REQUIRES_ACCEPTANCE, Pending=0.
//
//	Second Tick: 1 match, all tickets StatusPlacing, PendingAcceptances empty.
func TestAcceptance_AllAcceptYieldsMatchOnNextTick(t *testing.T) {
	clock := flexi.NewFakeClock(time.Unix(1_700_000_000, 0))
	mm, err := flexi.New([]byte(acceptRS), flexi.WithClock(clock))
	require.NoError(t, err)
	enqueueQuartet(t, mm)

	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Len(t, matches, 0, "no Match yet while acceptance pending")

	props := mm.PendingAcceptances()
	require.Len(t, props, 1)
	assert.ElementsMatch(t, []string{"a", "b", "c", "d"}, props[0].TicketIDs)
	assert.Equal(t, 0, mm.Pending(), "queued count excludes REQUIRES_ACCEPTANCE")

	for _, id := range props[0].TicketIDs {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equal(t, flexi.StatusRequiresAcceptance, s)
	}

	for _, id := range props[0].TicketIDs {
		require.NoError(t, mm.Accept(id, id))
	}

	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.ElementsMatch(t, []string{"a", "b", "c", "d"}, matches[0].TicketIDs)

	for _, id := range matches[0].TicketIDs {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equal(t, flexi.StatusPlacing, s)
	}
	assert.Len(t, mm.PendingAcceptances(), 0)
}

// Purpose: Verify that a single Reject dissolves the entire proposal, moving all tickets to CANCELLED (per FlexMatch spec).
// Method:  Create a proposal with four tickets, Reject from one player, then check every ticket's Status.
//
//	Also confirm that a subsequent Tick does not re-match the cancelled tickets.
//
// Expect:  All four tickets become StatusCancelled, PendingAcceptances=0, next Tick returns 0 matches and Pending=0.
func TestAcceptance_RejectDissolvesProposalToCancelled(t *testing.T) {
	mm, err := flexi.New([]byte(acceptRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)

	_, err = mm.Tick()
	require.NoError(t, err)

	require.NoError(t, mm.Reject("a", "a"))

	for _, id := range []string{"a", "b", "c", "d"} {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equalf(t, flexi.StatusCancelled, s, "ticket %s", id)
	}
	assert.Len(t, mm.PendingAcceptances(), 0)

	// Subsequent Tick must not re-match cancelled tickets (they're gone).
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Len(t, matches, 0)
	assert.Equal(t, 0, mm.Pending())
}

// Purpose: Verify that a proposal is discarded as TIMED_OUT after acceptanceTimeoutSeconds elapses.
// Method:  Advance FakeClock past the deadline (61s) after only some players have accepted, then Tick.
// Expect:  All four tickets become StatusTimedOut, no Match is returned, PendingAcceptances=0.
func TestAcceptance_TimeoutMovesTicketsToTimedOut(t *testing.T) {
	clock := flexi.NewFakeClock(time.Unix(1_700_000_000, 0))
	mm, err := flexi.New([]byte(acceptRS), flexi.WithClock(clock))
	require.NoError(t, err)
	enqueueQuartet(t, mm)

	_, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, mm.PendingAcceptances(), 1)

	// Some players accept, but not all; then deadline passes.
	require.NoError(t, mm.Accept("a", "a"))
	require.NoError(t, mm.Accept("b", "b"))
	clock.Advance(61 * time.Second)

	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Len(t, matches, 0)

	for _, id := range []string{"a", "b", "c", "d"} {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equalf(t, flexi.StatusTimedOut, s, "ticket %s", id)
	}
	assert.Len(t, mm.PendingAcceptances(), 0)
}

// Purpose: Verify that a partial Accept does not resolve the proposal, and full acceptance does.
// Method:  Two players Accept → Tick (no match) → remaining two Accept → Tick.
// Expect:  First Tick returns 0 matches; second Tick returns 1 match.
func TestAcceptance_PartialAcceptThenMoreAccepts(t *testing.T) {
	mm, err := flexi.New([]byte(acceptRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)

	_, err = mm.Tick()
	require.NoError(t, err)

	require.NoError(t, mm.Accept("a", "a"))
	require.NoError(t, mm.Accept("b", "b"))

	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Len(t, matches, 0, "not all accepted yet")

	require.NoError(t, mm.Accept("c", "c"))
	require.NoError(t, mm.Accept("d", "d"))

	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
}

// Purpose: Verify that a double Accept is idempotent, and that any operation on a dissolved proposal returns ErrUnknownProposal.
// Method:  Accept the same player twice (should be fine), then Reject from another ticket to dissolve the proposal,
//
//	then attempt Accept on a remaining ticket.
//
// Expect:  Second Accept succeeds without error; Accept after dissolution returns ErrUnknownProposal.
func TestAcceptance_DoubleAcceptIdempotentDoubleRejectRejects(t *testing.T) {
	mm, err := flexi.New([]byte(acceptRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)
	_, err = mm.Tick()
	require.NoError(t, err)

	require.NoError(t, mm.Accept("a", "a"))
	require.NoError(t, mm.Accept("a", "a"))

	// Reject from another ticket dissolves the proposal; further decisions
	// against those tickets must now return ErrUnknownProposal.
	require.NoError(t, mm.Reject("b", "b"))
	err = mm.Accept("c", "c")
	assert.ErrorIs(t, err, flexi.ErrUnknownProposal)
}

// Purpose: Verify the error types returned for invalid ticket ID and invalid player ID in Accept.
// Method:  After creating a proposal, call Accept with an unknown ticket ID and with an unknown player ID.
// Expect:  Unknown ticket → ErrUnknownTicket; unknown player → ErrUnknownPlayer.
func TestAcceptance_UnknownTicketAndPlayer(t *testing.T) {
	mm, err := flexi.New([]byte(acceptRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)
	_, err = mm.Tick()
	require.NoError(t, err)

	assert.ErrorIs(t, mm.Accept("nope", "nope"), flexi.ErrUnknownTicket)
	assert.ErrorIs(t, mm.Accept("a", "not-a-player"), flexi.ErrUnknownPlayer)
}

// Purpose: Verify that Accept on a QUEUED ticket (not yet in any proposal) returns ErrUnknownProposal.
// Method:  Enqueue a single ticket without calling Tick, then immediately call Accept on it.
// Expect:  ErrUnknownProposal is returned (the call is not silently swallowed).
func TestAcceptance_AcceptOnUnrelatedQueuedTicketFails(t *testing.T) {
	mm, err := flexi.New([]byte(acceptRS))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("lonely", 50)))

	err = mm.Accept("lonely", "lonely")
	assert.ErrorIs(t, err, flexi.ErrUnknownProposal)
}

// Purpose: Verify that cancelling one ticket in a proposal dissolves the entire proposal.
// Method:  Create a proposal with four tickets, Cancel one, then check every ticket's Status and PendingAcceptances.
// Expect:  All four tickets become StatusCancelled; PendingAcceptances is empty.
func TestAcceptance_CancelDuringProposalDissolvesIt(t *testing.T) {
	mm, err := flexi.New([]byte(acceptRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)
	_, err = mm.Tick()
	require.NoError(t, err)

	require.NoError(t, mm.Cancel("a"))

	for _, id := range []string{"a", "b", "c", "d"} {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equalf(t, flexi.StatusCancelled, s, "ticket %s", id)
	}
	assert.Len(t, mm.PendingAcceptances(), 0)
}

// --- Parser / config surface ------------------------------------------------

// Purpose: Verify that omitting acceptanceRequired (defaults to false) produces the same immediate-match behaviour as before.
// Method:  Enqueue four tickets into skillRS (no acceptanceRequired) and call Tick once.
// Expect:  One Match is returned immediately and PendingAcceptances remains empty.
func TestAcceptance_Disabled_BehavesLikePlainMatch(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Len(t, mm.PendingAcceptances(), 0)
}

// Purpose: Verify that a negative acceptanceTimeoutSeconds value is rejected at parse time.
// Method:  Pass a rule set JSON with acceptanceTimeoutSeconds=-1 to flexi.New.
// Expect:  An error wrapping ErrInvalidRuleSet is returned.
func TestAcceptance_NegativeTimeoutRejected(t *testing.T) {
	body := `{
      "name": "bad",
      "teams": [{"name": "t", "minPlayers": 1, "maxPlayers": 1}],
      "acceptanceRequired": true,
      "acceptanceTimeoutSeconds": -1
    }`
	_, err := flexi.New([]byte(body))
	assert.Truef(t, errors.Is(err, flexi.ErrInvalidRuleSet), "got %v", err)
}

// Purpose: Verify Enqueue input validation: empty ID, zero players, and duplicate ID.
// Method:  Attempt to enqueue an empty Ticket, a Ticket with no Players, and a duplicate ID in sequence.
// Expect:  Errors containing "ticket id is required" / "at least one player" / ErrDuplicateTicket respectively.
func TestEnqueue_ValidationErrors(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)

	assert.ErrorContains(t, mm.Enqueue(flexi.Ticket{}), "ticket id is required")
	assert.ErrorContains(t, mm.Enqueue(flexi.Ticket{ID: "x"}), "at least one player")

	require.NoError(t, mm.Enqueue(solo("a", 50)))
	assert.ErrorIs(t, mm.Enqueue(solo("a", 50)), flexi.ErrDuplicateTicket)
}

// Purpose: Verify that re-using a cancelled ticket's ID is rejected (matching FlexMatch's "resubmit with a new ID" rule).
// Method:  Enqueue ID "a", Cancel it, then Enqueue ID "a" again.
// Expect:  ErrDuplicateTicket is returned on the second Enqueue.
func TestEnqueue_RejectsIDReusedByTerminalStatus(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 50)))
	require.NoError(t, mm.Cancel("a"))

	assert.ErrorIs(t, mm.Enqueue(solo("a", 50)), flexi.ErrDuplicateTicket)
}

// Purpose: Verify that Cancel on an unknown ticket ID returns an error.
// Method:  Call Cancel("nope") on a Matchmaker with no enqueued tickets.
// Expect:  ErrUnknownTicket is returned.
func TestCancel_UnknownTicket(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	assert.ErrorIs(t, mm.Cancel("nope"), flexi.ErrUnknownTicket)
}

// Purpose: Verify that Cancel on a terminal-state ticket (PLACING) is rejected.
// Method:  Form a match so all four tickets become PLACING, then attempt to Cancel one.
// Expect:  ErrUnknownTicket is returned (Cancel only applies to QUEUED or REQUIRES_ACCEPTANCE tickets).
func TestCancel_AfterTerminalRejects(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)
	_, err = mm.Tick()
	require.NoError(t, err)

	// All four tickets are now PLACING; Cancel must refuse.
	assert.ErrorIs(t, mm.Cancel("a"), flexi.ErrUnknownTicket)
}

// Purpose: Verify that MarkCompleted on an unknown ticket ID returns an error.
// Method:  Call MarkCompleted("nope") on a Matchmaker with no enqueued tickets.
// Expect:  ErrUnknownTicket is returned.
func TestMarkCompleted_UnknownTicket(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	assert.ErrorIs(t, mm.MarkCompleted("nope"), flexi.ErrUnknownTicket)
}

// Purpose: Verify that Tick on an empty queue returns nil safely.
// Method:  Call Tick immediately on a newly created Matchmaker with no tickets.
// Expect:  No error and matches is nil.
func TestTick_EmptyQueueReturnsNil(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Nil(t, matches)
}

// Purpose: Verify that Tick returns a Match from a fully-accepted proposal even when the queue is empty.
// Method:  Create a proposal, accept all players (queue is now empty), then Tick.
// Expect:  One Match is returned.
func TestTick_AcceptedProposalReturnsEvenWithEmptyQueue(t *testing.T) {
	mm, err := flexi.New([]byte(acceptRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)
	_, err = mm.Tick()
	require.NoError(t, err)

	for _, id := range []string{"a", "b", "c", "d"} {
		require.NoError(t, mm.Accept(id, id))
	}
	assert.Equal(t, 0, mm.Pending())

	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
}

// Purpose: Verify that Tick correctly resolves an accepted proposal alongside a queued ticket that cannot yet form a match.
// Method:  Accept all four proposal tickets, enqueue a fifth ticket "e" that cannot match alone, then Tick.
// Expect:  One Match for (a,b,c,d) is returned; "e" remains queued with Pending=1.
func TestTick_AcceptedProposalAlongsideUnmatchedQueuedTicket(t *testing.T) {
	mm, err := flexi.New([]byte(acceptRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)
	_, err = mm.Tick()
	require.NoError(t, err)

	for _, id := range []string{"a", "b", "c", "d"} {
		require.NoError(t, mm.Accept(id, id))
	}
	require.NoError(t, mm.Enqueue(solo("e", 50)))

	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.ElementsMatch(t, []string{"a", "b", "c", "d"}, matches[0].TicketIDs)
	assert.Equal(t, 1, mm.Pending(), "lonely ticket still queued")
}

// Purpose: Verify that acceptanceTimeoutSeconds=0 (omitted) is interpreted as "no timeout".
// Method:  Create a proposal with a rule set that has no timeout, advance the FakeClock by 10 hours, then Tick.
// Expect:  No Match is returned and the proposal remains pending (StatusRequiresAcceptance is preserved).
func TestAcceptance_ZeroTimeoutMeansNoTimeout(t *testing.T) {
	body := `{
      "name": "no-timeout",
      "playerAttributes": [{"name": "skill", "type": "number"}],
      "teams": [
        {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
        {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
      ],
      "rules": [
        {"name": "FairSkill", "type": "distance",
         "measurements": ["avg(teams[red].players.skill)"],
         "referenceValue": "avg(teams[blue].players.skill)",
         "maxDistance": 10}
      ],
      "acceptanceRequired": true
    }`
	clock := flexi.NewFakeClock(time.Unix(1_700_000_000, 0))
	mm, err := flexi.New([]byte(body), flexi.WithClock(clock))
	require.NoError(t, err)
	enqueueQuartet(t, mm)
	_, err = mm.Tick()
	require.NoError(t, err)

	clock.Advance(10 * time.Hour)
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Len(t, matches, 0)
	require.Len(t, mm.PendingAcceptances(), 1, "still pending — zero timeout disables expiry")
}
