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
     "measurements": ["avg(teams[red].players.attributes[skill])"],
     "referenceValue": "avg(teams[blue].players.attributes[skill])",
     "maxDistance": 10}
  ],
  "acceptanceRequired": true,
  "acceptanceTimeoutSeconds": 60
}`

// Like acceptRS but also bounds the overall request lifetime at 120s via
// requestTimeoutSeconds, used to exercise the request-level TIMED_OUT path.
const acceptReqTimeoutRS = `{
  "name": "skill-balance-accept-reqtimeout",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [{"name": "skill", "type": "number"}],
  "teams": [
    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
  ],
  "rules": [
    {"name": "FairSkill", "type": "distance",
     "measurements": ["avg(teams[red].players.attributes[skill])"],
     "referenceValue": "avg(teams[blue].players.attributes[skill])",
     "maxDistance": 10}
  ],
  "acceptanceRequired": true,
  "acceptanceTimeoutSeconds": 60,
  "requestTimeoutSeconds": 120
}`

// Acceptance required with no acceptance timeout, but a short request timeout —
// used to verify the request deadline does not evict tickets held in a proposal.
const reqTimeoutNoAcceptTimeoutRS = `{
  "name": "accept-no-acctimeout-reqtimeout",
  "ruleLanguageVersion": "1.0",
  "teams": [
    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
  ],
  "acceptanceRequired": true,
  "requestTimeoutSeconds": 30
}`

// No acceptance, teams of two, with a 30s request timeout. A lone ticket can
// never match and will time out at the request deadline.
const requestTimeoutRS = `{
  "name": "req-timeout",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [{"name": "skill", "type": "number"}],
  "teams": [
    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
  ],
  "requestTimeoutSeconds": 30
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

// Purpose: Verify the default AWS-compliant Reject split — the rejecting ticket is CANCELLED while
//
//	a sibling that had fully accepted is re-queued to SEARCHING for re-matching.
//
// Method:  Create a proposal with four tickets, Accept all players on a, b, c, then Reject from d,
//
//	and check every ticket's Status, PendingAcceptances, StatusReason, and Pending count.
//
// Expect:  d → CANCELLED (terminal); a, b, c → SEARCHING with StatusReasonAcceptanceFailed and
//
//	re-queued (Pending=3); PendingAcceptances=0.
func TestAcceptance_RejectRequeuesAcceptedAndCancelsRejecter(t *testing.T) {
	mm, err := flexi.New([]byte(acceptRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)

	_, err = mm.Tick()
	require.NoError(t, err)

	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, mm.Accept(id, id))
	}
	require.NoError(t, mm.Reject("d", "d"))

	// The rejecting ticket is terminal.
	s, err := mm.Status("d")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusCancelled, s)
	_, hasReason := mm.StatusReason("d")
	assert.False(t, hasReason, "terminal ticket carries no status reason")

	// The fully-accepted siblings return to SEARCHING and are re-queued.
	for _, id := range []string{"a", "b", "c"} {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equalf(t, flexi.StatusSearching, s, "ticket %s", id)
		reason, ok := mm.StatusReason(id)
		assert.Truef(t, ok, "ticket %s should carry a status reason", id)
		assert.Equal(t, flexi.StatusReasonAcceptanceFailed, reason)
	}
	assert.Len(t, mm.PendingAcceptances(), 0)
	assert.Equal(t, 3, mm.Pending(), "accepted tickets are back in the queue")
}

// Purpose: Verify a re-queued ticket is genuinely re-matched by a later Tick once new partners arrive.
// Method:  Form a proposal over a,b,c,d; accept all of a,b,c; reject d so a,b,c re-queue to SEARCHING;
//
//	enqueue a fresh ticket e; Tick to form a new proposal; accept all four; Tick to a Match.
//
// Expect:  The second Tick re-proposes using the re-queued tickets, and full acceptance yields a Match.
func TestAcceptance_RequeuedTicketsRematchOnLaterTick(t *testing.T) {
	mm, err := flexi.New([]byte(acceptRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)
	_, err = mm.Tick()
	require.NoError(t, err)

	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, mm.Accept(id, id))
	}
	require.NoError(t, mm.Reject("d", "d"))

	// A fresh partner arrives to replace the cancelled d.
	require.NoError(t, mm.Enqueue(solo("e", 50)))

	_, err = mm.Tick()
	require.NoError(t, err)
	props := mm.PendingAcceptances()
	require.Len(t, props, 1, "re-queued tickets form a new proposal")
	assert.ElementsMatch(t, []string{"a", "b", "c", "e"}, props[0].TicketIDs)

	// The re-queued tickets must have shed their acceptance-failure reason now
	// that they are back in a proposal.
	for _, id := range props[0].TicketIDs {
		_, ok := mm.StatusReason(id)
		assert.Falsef(t, ok, "ticket %s should no longer carry a status reason", id)
		require.NoError(t, mm.Accept(id, id))
	}

	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.ElementsMatch(t, []string{"a", "b", "c", "e"}, matches[0].TicketIDs)
}

// Purpose: Verify the AWS-compliant acceptance-timeout split — accepted tickets re-queue, non-responders are CANCELLED.
// Method:  Accept all players on a, b (not c, d), advance FakeClock past the 61s deadline, then Tick.
// Expect:  a, b → SEARCHING (re-queued, StatusReasonAcceptanceFailed); c, d → CANCELLED; no Match; Pending=2.
//
//	FlexMatch terminates non-accepting tickets as CANCELLED (not TIMED_OUT) on an acceptance failure.
func TestAcceptance_TimeoutRequeuesAcceptedCancelsNonResponders(t *testing.T) {
	clock := flexi.NewFakeClock(time.Unix(1_700_000_000, 0))
	mm, err := flexi.New([]byte(acceptRS), flexi.WithClock(clock))
	require.NoError(t, err)
	enqueueQuartet(t, mm)

	_, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, mm.PendingAcceptances(), 1)

	// a and b accept fully; c and d never respond; then the deadline passes.
	require.NoError(t, mm.Accept("a", "a"))
	require.NoError(t, mm.Accept("b", "b"))
	clock.Advance(61 * time.Second)

	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Len(t, matches, 0)

	for _, id := range []string{"a", "b"} {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equalf(t, flexi.StatusSearching, s, "ticket %s", id)
		reason, ok := mm.StatusReason(id)
		assert.Truef(t, ok, "ticket %s should carry a status reason", id)
		assert.Equal(t, flexi.StatusReasonAcceptanceFailed, reason)
	}
	for _, id := range []string{"c", "d"} {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equalf(t, flexi.StatusCancelled, s, "ticket %s", id)
	}
	assert.Len(t, mm.PendingAcceptances(), 0)
	assert.Equal(t, 2, mm.Pending(), "accepted tickets are back in the queue")
}

// Purpose: Verify that a fully unanswered proposal cancels every ticket (none re-queued, none timed out).
// Method:  Let the 61s acceptance deadline pass with no acceptances at all, then Tick.
// Expect:  All four tickets become StatusCancelled, no Match, PendingAcceptances=0, Pending=0.
func TestAcceptance_TimeoutWithNoAcceptancesCancelsAll(t *testing.T) {
	clock := flexi.NewFakeClock(time.Unix(1_700_000_000, 0))
	mm, err := flexi.New([]byte(acceptRS), flexi.WithClock(clock))
	require.NoError(t, err)
	enqueueQuartet(t, mm)

	_, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, mm.PendingAcceptances(), 1)

	clock.Advance(61 * time.Second)

	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Len(t, matches, 0)

	for _, id := range []string{"a", "b", "c", "d"} {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equalf(t, flexi.StatusCancelled, s, "ticket %s", id)
	}
	assert.Len(t, mm.PendingAcceptances(), 0)
	assert.Equal(t, 0, mm.Pending())
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

// Purpose: Verify that a ticket re-queued to SEARCHING after a failed acceptance can still be Cancelled.
// Method:  Reject a proposal so accepted siblings re-queue, then Cancel one re-queued ticket.
// Expect:  Cancel succeeds, the ticket becomes StatusCancelled, and it leaves the queue (Pending drops).
func TestCancel_OnRequeuedSearchingTicket(t *testing.T) {
	mm, err := flexi.New([]byte(acceptRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)
	_, err = mm.Tick()
	require.NoError(t, err)

	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, mm.Accept(id, id))
	}
	require.NoError(t, mm.Reject("d", "d"))
	require.Equal(t, 3, mm.Pending())

	require.NoError(t, mm.Cancel("a"))
	s, err := mm.Status("a")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusCancelled, s)
	assert.Equal(t, 2, mm.Pending(), "cancelled ticket removed from the queue")
}

// Purpose: Verify per-player aggregation — a party ticket where one member does not accept is CANCELLED, not re-queued.
// Method:  Propose duo(p1,p2)+c+d; accept c, d fully and p1 of the duo, then Reject p2.
// Expect:  duo → CANCELLED (p1's acceptance does not save it); c, d → SEARCHING (re-queued); Pending=2.
func TestAcceptance_PartyTicketPartialAcceptanceCancelled(t *testing.T) {
	mm, err := flexi.New([]byte(reqTimeoutNoAcceptTimeoutRS))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "duo", Players: []flexi.Player{{ID: "p1"}, {ID: "p2"}}}))
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "c", Players: []flexi.Player{{ID: "c"}}}))
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "d", Players: []flexi.Player{{ID: "d"}}}))

	_, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, mm.PendingAcceptances(), 1)

	require.NoError(t, mm.Accept("c", "c"))
	require.NoError(t, mm.Accept("d", "d"))
	require.NoError(t, mm.Accept("duo", "p1"))
	require.NoError(t, mm.Reject("duo", "p2"))

	s, err := mm.Status("duo")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusCancelled, s, "duo not fully accepted (p2 rejected) → cancelled")

	for _, id := range []string{"c", "d"} {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equalf(t, flexi.StatusSearching, s, "ticket %s", id)
	}
	assert.Equal(t, 2, mm.Pending())
}

// Purpose: Verify that a Reject cancels not only the rejecter but also siblings that never responded.
// Method:  Propose a,b,c,d; accept only a; Reject b; leave c and d with no response.
// Expect:  a → SEARCHING (re-queued); b, c, d → CANCELLED (rejected or failed to respond); Pending=1.
func TestAcceptance_RejectCancelsPendingSiblings(t *testing.T) {
	mm, err := flexi.New([]byte(acceptRS))
	require.NoError(t, err)
	enqueueQuartet(t, mm)
	_, err = mm.Tick()
	require.NoError(t, err)

	require.NoError(t, mm.Accept("a", "a"))
	require.NoError(t, mm.Reject("b", "b"))

	s, err := mm.Status("a")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusSearching, s, "fully-accepted ticket re-queued")

	for _, id := range []string{"b", "c", "d"} {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equalf(t, flexi.StatusCancelled, s, "ticket %s (rejected or unresponsive)", id)
	}
	assert.Equal(t, 1, mm.Pending())
}

// --- Request timeout (requestTimeoutSeconds) --------------------------------

// Purpose: Verify a ticket that cannot match times out at requestTimeoutSeconds with StatusTimedOut.
// Method:  Enqueue a lone ticket (teams need 4 players), Tick, advance past 30s, Tick again.
// Expect:  First Tick leaves it QUEUED; after the deadline it becomes StatusTimedOut and leaves the queue.
func TestRequestTimeout_UnmatchedTicketTimesOut(t *testing.T) {
	clock := flexi.NewFakeClock(time.Unix(1_700_000_000, 0))
	mm, err := flexi.New([]byte(requestTimeoutRS), flexi.WithClock(clock))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 50)))

	_, err = mm.Tick()
	require.NoError(t, err)
	s, err := mm.Status("a")
	require.NoError(t, err)
	require.Equal(t, flexi.StatusQueued, s)

	clock.Advance(31 * time.Second)
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Len(t, matches, 0)

	s, err = mm.Status("a")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusTimedOut, s)
	assert.Equal(t, 0, mm.Pending())
}

// Purpose: Verify the request deadline is measured from the original enqueue, so a re-queued ticket still times out.
// Method:  Propose a,b,c,d; accept a,b,c and Reject d (a,b,c re-queue to SEARCHING); advance past 120s; Tick.
// Expect:  a, b, c → StatusTimedOut (measured from their original enqueue), their acceptance-failure reason cleared.
func TestRequestTimeout_RequeuedTicketTimesOutFromOriginalEnqueue(t *testing.T) {
	clock := flexi.NewFakeClock(time.Unix(1_700_000_000, 0))
	mm, err := flexi.New([]byte(acceptReqTimeoutRS), flexi.WithClock(clock))
	require.NoError(t, err)
	enqueueQuartet(t, mm)

	_, err = mm.Tick()
	require.NoError(t, err)

	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, mm.Accept(id, id))
	}
	require.NoError(t, mm.Reject("d", "d"))
	for _, id := range []string{"a", "b", "c"} {
		s, err := mm.Status(id)
		require.NoError(t, err)
		require.Equalf(t, flexi.StatusSearching, s, "ticket %s re-queued", id)
	}

	clock.Advance(121 * time.Second)
	_, err = mm.Tick()
	require.NoError(t, err)

	for _, id := range []string{"a", "b", "c"} {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equalf(t, flexi.StatusTimedOut, s, "ticket %s", id)
		_, ok := mm.StatusReason(id)
		assert.Falsef(t, ok, "ticket %s should not carry a status reason once timed out", id)
	}
	assert.Equal(t, 0, mm.Pending())
}

// Purpose: Verify the request deadline does not evict tickets currently held in a proposal (REQUIRES_ACCEPTANCE).
// Method:  Form a proposal (no acceptance timeout, 30s request timeout), advance past 30s, Tick.
// Expect:  Tickets stay REQUIRES_ACCEPTANCE — acceptance windows, not the request deadline, govern proposal tickets.
func TestRequestTimeout_DoesNotAffectProposalTickets(t *testing.T) {
	clock := flexi.NewFakeClock(time.Unix(1_700_000_000, 0))
	mm, err := flexi.New([]byte(reqTimeoutNoAcceptTimeoutRS), flexi.WithClock(clock))
	require.NoError(t, err)
	for _, tk := range []flexi.Ticket{
		{ID: "a", Players: []flexi.Player{{ID: "a"}}},
		{ID: "b", Players: []flexi.Player{{ID: "b"}}},
		{ID: "c", Players: []flexi.Player{{ID: "c"}}},
		{ID: "d", Players: []flexi.Player{{ID: "d"}}},
	} {
		require.NoError(t, mm.Enqueue(tk))
	}

	_, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, mm.PendingAcceptances(), 1)

	clock.Advance(31 * time.Second)
	_, err = mm.Tick()
	require.NoError(t, err)

	for _, id := range []string{"a", "b", "c", "d"} {
		s, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equalf(t, flexi.StatusRequiresAcceptance, s, "ticket %s still awaiting acceptance", id)
	}
	require.Len(t, mm.PendingAcceptances(), 1)
}

// Purpose: Verify that a negative requestTimeoutSeconds is rejected at parse time.
// Method:  Pass a rule set JSON with requestTimeoutSeconds=-1 to flexi.New.
// Expect:  An error wrapping ErrInvalidRuleSet is returned.
func TestRequestTimeout_NegativeRejected(t *testing.T) {
	body := `{
      "name": "bad-req",
      "ruleLanguageVersion": "1.0",
      "teams": [{"name": "t", "minPlayers": 1, "maxPlayers": 1}],
      "requestTimeoutSeconds": -1
    }`
	_, err := flexi.New([]byte(body))
	assert.Truef(t, errors.Is(err, flexi.ErrInvalidRuleSet), "got %v", err)
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
      "ruleLanguageVersion": "1.0",
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
      "ruleLanguageVersion": "1.0",
      "playerAttributes": [{"name": "skill", "type": "number"}],
      "teams": [
        {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
        {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
      ],
      "rules": [
        {"name": "FairSkill", "type": "distance",
         "measurements": ["avg(teams[red].players.attributes[skill])"],
         "referenceValue": "avg(teams[blue].players.attributes[skill])",
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
