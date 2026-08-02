package flexi_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/moepig/flexi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two teams of exactly two, the smallest rule set that leaves a seat for a
// backfill to fill.
const backfillRS = `{
  "name": "backfill",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [{"name": "skill", "type": "number"}],
  "teams": [
    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
  ]
}`

// seatedPlayer is a player already in a game session, as a backfill request
// reports them.
func seatedPlayer(id, team string, skill float64) flexi.Player {
	return flexi.Player{
		ID:         id,
		Team:       team,
		Attributes: flexi.Attributes{"skill": flexi.Number(skill)},
	}
}

// newPlayer is a player asking to join, on a regular ticket.
func newPlayer(id string, skill float64) flexi.Player {
	return flexi.Player{ID: id, Attributes: flexi.Attributes{"skill": flexi.Number(skill)}}
}

// Purpose: Verify Enqueue rejects a player carrying a team assignment. AWS says
// "do not specify team if you are not using backfill", so a Team on a regular
// ticket is a caller mistake — most likely a backfill request sent to the wrong
// method — and must not be silently ignored.
// Method:  Enqueue a ticket whose player sets Team.
// Expect:  An error naming the player, and nothing queued.
func TestEnqueue_RejectsPlayerWithTeam(t *testing.T) {
	mm, err := flexi.New([]byte(backfillRS))
	require.NoError(t, err)

	err = mm.Enqueue(flexi.Ticket{ID: "t1", Players: []flexi.Player{seatedPlayer("p1", "red", 10)}})
	require.Error(t, err)
	assert.ErrorIs(t, err, flexi.ErrInvalidTicket)
	assert.Contains(t, err.Error(), "p1")
	assert.Zero(t, mm.Pending())
}

// Purpose: Verify EnqueueBackfill validates the roster's team assignments the way
// AWS requires them: every player states the team they are on, and it must name a
// team the rule set declares without ambiguity.
// Method:  Submit rosters against a rule set with "solo" (quantity 1) and "duo"
// (quantity 2), covering each resolution outcome plus an over-full team.
// Expect:  Declared and expanded names are accepted; a player with no team, an
// unknown team, a base name that expanded into several teams, and a team
// given more players than its maxPlayers are all rejected.
func TestEnqueueBackfill_ValidatesTeamAssignments(t *testing.T) {
	mm, err := flexi.New([]byte(`{
	  "name": "backfill-teams",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name": "skill", "type": "number"}],
	  "teams": [
	    {"name": "solo", "minPlayers": 1, "maxPlayers": 2},
	    {"name": "duo",  "minPlayers": 1, "maxPlayers": 2, "quantity": 2}
	  ]
	}`))
	require.NoError(t, err)

	cases := []struct {
		name    string
		players []flexi.Player
		wantErr string
	}{
		{"declared team", []flexi.Player{seatedPlayer("p1", "solo", 10)}, ""},
		{"expanded team", []flexi.Player{seatedPlayer("p1", "duo_2", 10)}, ""},
		{"no team", []flexi.Player{newPlayer("p1", 10)}, "team is required"},
		{"unknown team", []flexi.Player{seatedPlayer("p1", "green", 10)}, "unknown team"},
		{"ambiguous team", []flexi.Player{seatedPlayer("p1", "duo", 10)}, "quantity > 1"},
		{"team over maxPlayers", []flexi.Player{
			seatedPlayer("p1", "solo", 10), seatedPlayer("p2", "solo", 11), seatedPlayer("p3", "solo", 12),
		}, "maxPlayers"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := mm.EnqueueBackfill(flexi.Ticket{ID: fmt.Sprintf("bf%d", i), Players: c.players})
			if c.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, flexi.ErrInvalidTicket)
			assert.Contains(t, err.Error(), c.wantErr)
		})
	}
}

// Purpose: Verify EnqueueBackfill enforces the 199-player ceiling AWS documents
// for StartMatchBackfill's Players parameter.
// Method:  Submit a 200-player roster against a rule set whose team is large
// enough to hold it, then a 199-player one.
// Expect:  The 200-player roster is rejected; the 199-player roster is accepted.
func TestEnqueueBackfill_RejectsRosterAboveAWSLimit(t *testing.T) {
	mm, err := flexi.New([]byte(`{
	  "name": "backfill-large",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 250}]
	}`))
	require.NoError(t, err)

	roster := func(n int) []flexi.Player {
		out := make([]flexi.Player, 0, n)
		for i := range n {
			out = append(out, flexi.Player{ID: fmt.Sprintf("p%03d", i), Team: "all"})
		}
		return out
	}
	err = mm.EnqueueBackfill(flexi.Ticket{ID: "too-big", Players: roster(200)})
	require.Error(t, err)
	assert.ErrorIs(t, err, flexi.ErrInvalidTicket)
	assert.Contains(t, err.Error(), "199")

	assert.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "at-limit", Players: roster(199)}))
}

// Purpose: Verify a later backfill request for a game session supersedes the one
// still waiting for it, mirroring FlexMatch's rule that a game session has only
// one backfill request at a time and a new one automatically replaces it.
// Method:  Enqueue two backfill requests carrying the same GameSessionID.
// Expect:  The first is CANCELLED and gone from the queue; the second is QUEUED.
func TestEnqueueBackfill_SupersedesWaitingRequestForSameSession(t *testing.T) {
	mm, err := flexi.New([]byte(backfillRS))
	require.NoError(t, err)

	roster := []flexi.Player{
		seatedPlayer("r1", "red", 10), seatedPlayer("r2", "red", 11), seatedPlayer("b1", "blue", 12),
	}
	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf1", GameSessionID: "gs-1", Players: roster}))
	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf2", GameSessionID: "gs-1", Players: roster}))

	first, err := mm.Status("bf1")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusCancelled, first)
	second, err := mm.Status("bf2")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusQueued, second)
	assert.Equal(t, 1, mm.Pending())
}

// Purpose: Verify a backfill request is not superseded once it has produced a
// match the caller is acting on. Replacing it would tear a proposal out from
// under players who are being asked to accept it.
// Method:  Enqueue a backfill request for a session together with a ticket that
// completes the match, Tick to raise the proposal, then enqueue a second
// request for the same session.
// Expect:  ErrBackfillInProgress; the proposal's ticket stays REQUIRES_ACCEPTANCE.
func TestEnqueueBackfill_RefusesWhileSessionRequestIsMatched(t *testing.T) {
	mm, err := flexi.New([]byte(`{
	  "name": "backfill-accept",
	  "ruleLanguageVersion": "1.0",
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ],
	  "acceptanceRequired": true,
	  "acceptanceTimeoutSeconds": 60
	}`))
	require.NoError(t, err)

	roster := []flexi.Player{
		seatedPlayer("r1", "red", 10), seatedPlayer("r2", "red", 11), seatedPlayer("b1", "blue", 12),
	}
	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf1", GameSessionID: "gs-1", Players: roster}))
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "t1", Players: []flexi.Player{newPlayer("n1", 13)}}))
	_, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, mm.PendingAcceptances(), 1)

	err = mm.EnqueueBackfill(flexi.Ticket{ID: "bf2", GameSessionID: "gs-1", Players: roster})
	assert.True(t, errors.Is(err, flexi.ErrBackfillInProgress), "err: %v", err)
	assert.NotErrorIs(t, err, flexi.ErrInvalidTicket, "the request is well formed; the session is busy")
	status, err := mm.Status("bf1")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusRequiresAcceptance, status)
}

// Purpose: Verify leaving GameSessionID empty opts out of the one-request-per-
// session bookkeeping, so a caller that tracks sessions itself can hold several
// backfill requests at once.
// Method:  Enqueue two backfill requests with no GameSessionID.
// Expect:  Both stay QUEUED.
func TestEnqueueBackfill_WithoutGameSessionIDKeepsBothRequests(t *testing.T) {
	mm, err := flexi.New([]byte(backfillRS))
	require.NoError(t, err)

	roster := []flexi.Player{seatedPlayer("r1", "red", 10)}
	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf1", Players: roster}))
	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf2", Players: roster}))
	assert.Equal(t, 2, mm.Pending())
}

// Purpose: Verify Evict releases a spent backfill request's hold on its game
// session without disturbing the request that currently holds the session, so
// the one-request-per-session bookkeeping stays bounded and correct as a caller
// sweeps terminal tickets.
// Method:  Supersede a request for a session, evict the superseded one, then
// enqueue a third request for the same session.
// Expect:  The evicted request is unknown; the third still supersedes the second,
// which moves to CANCELLED.
func TestEvict_ReleasesSupersededBackfillWithoutFreeingTheSession(t *testing.T) {
	mm, err := flexi.New([]byte(backfillRS))
	require.NoError(t, err)

	roster := []flexi.Player{seatedPlayer("r1", "red", 10)}
	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf1", GameSessionID: "gs-1", Players: roster}))
	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf2", GameSessionID: "gs-1", Players: roster}))

	require.NoError(t, mm.Evict("bf1"))
	_, err = mm.Status("bf1")
	assert.ErrorIs(t, err, flexi.ErrUnknownTicket)

	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf3", GameSessionID: "gs-1", Players: roster}))
	status, err := mm.Status("bf2")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusCancelled, status)
	assert.Equal(t, 1, mm.Pending())
}

// Purpose: Verify the end-to-end backfill flow in the simplest configuration:
// a request naming the players already in a session is matched with a waiting
// ticket, and the resulting match describes the whole session.
// Method:  Enqueue a backfill request holding three of four seats plus a solo
// ticket, then Tick.
// Expect:  One match listing both tickets, whose teams hold the existing players
// alongside the new one; both tickets move to PLACING.
func TestTick_BackfillFillsTheOpenSeat(t *testing.T) {
	mm, err := flexi.New([]byte(backfillRS))
	require.NoError(t, err)

	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf", Players: []flexi.Player{
		seatedPlayer("r1", "red", 10), seatedPlayer("r2", "red", 11), seatedPlayer("b1", "blue", 12),
	}}))
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "t1", Players: []flexi.Player{newPlayer("n1", 13)}}))

	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, []string{"bf", "t1"}, matches[0].TicketIDs)
	assert.Equal(t, []string{"r1", "r2"}, playerIDs(matches[0].Teams["red"]))
	assert.Equal(t, []string{"b1", "n1"}, playerIDs(matches[0].Teams["blue"]))

	for _, id := range []string{"bf", "t1"} {
		status, err := mm.Status(id)
		require.NoError(t, err)
		assert.Equal(t, flexi.StatusPlacing, status, "ticket %s", id)
	}
}

// Purpose: Verify a backfill request never forms a match by itself. The players
// it carries are already playing, so a "match" that admits nobody new has
// achieved nothing and the request must keep waiting for a candidate.
// Method:  Enqueue a backfill request whose roster already satisfies every team's
// minPlayers, with no other ticket queued, and Tick.
// Expect:  No match; the request stays QUEUED.
func TestTick_BackfillAloneFormsNoMatch(t *testing.T) {
	mm, err := flexi.New([]byte(`{
	  "name": "backfill-open",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 4}]
	}`))
	require.NoError(t, err)

	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf", Players: []flexi.Player{
		seatedPlayer("p1", "all", 10), seatedPlayer("p2", "all", 11),
	}}))

	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches)
	status, err := mm.Status("bf")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusQueued, status)
}

// Purpose: Verify a backfill request takes part in the acceptance flow like any
// other ticket, with no special-casing: AWS documents backfill as managing player
// acceptance too, and a game server accepting on behalf of the players already in
// the session reproduces that arrangement.
// Method:  Raise a proposal from a backfill request plus a joining ticket, then
// accept for every player on both sides and Tick.
// Expect:  The proposal lists both tickets and every seated player; once all have
// accepted the match is returned.
func TestTick_BackfillRequiresAcceptanceFromSeatedPlayers(t *testing.T) {
	mm, err := flexi.New([]byte(`{
	  "name": "backfill-accept",
	  "ruleLanguageVersion": "1.0",
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ],
	  "acceptanceRequired": true,
	  "acceptanceTimeoutSeconds": 60
	}`))
	require.NoError(t, err)

	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf", Players: []flexi.Player{
		seatedPlayer("r1", "red", 10), seatedPlayer("r2", "red", 11), seatedPlayer("b1", "blue", 12),
	}}))
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "t1", Players: []flexi.Player{newPlayer("n1", 13)}}))

	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "acceptance is still outstanding")
	pending := mm.PendingAcceptances()
	require.Len(t, pending, 1)
	assert.Equal(t, []string{"bf", "t1"}, pending[0].TicketIDs)

	// The seated players have no client to prompt; the game server accepts for
	// them, exactly as it would against AWS.
	for _, id := range []string{"r1", "r2", "b1"} {
		require.NoError(t, mm.Accept("bf", id))
	}
	require.NoError(t, mm.Accept("t1", "n1"))

	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, []string{"bf", "t1"}, matches[0].TicketIDs)
}

// Purpose: Verify the acceptance-failure split treats a backfill request like any
// other fully-accepted ticket: when the joining player rejects, the request is
// returned to matchmaking rather than cancelled, so the session can be offered a
// different candidate.
// Method:  Raise the same proposal, accept for the seated players, then have the
// joining player reject.
// Expect:  The backfill request returns to SEARCHING with ACCEPTANCE_FAILED and
// is queued again; the rejecting ticket is CANCELLED.
func TestTick_BackfillReturnsToSearchingWhenJoinerRejects(t *testing.T) {
	mm, err := flexi.New([]byte(`{
	  "name": "backfill-accept",
	  "ruleLanguageVersion": "1.0",
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ],
	  "acceptanceRequired": true,
	  "acceptanceTimeoutSeconds": 60
	}`))
	require.NoError(t, err)

	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf", Players: []flexi.Player{
		seatedPlayer("r1", "red", 10), seatedPlayer("r2", "red", 11), seatedPlayer("b1", "blue", 12),
	}}))
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "t1", Players: []flexi.Player{newPlayer("n1", 13)}}))
	_, err = mm.Tick()
	require.NoError(t, err)

	for _, id := range []string{"r1", "r2", "b1"} {
		require.NoError(t, mm.Accept("bf", id))
	}
	require.NoError(t, mm.Reject("t1", "n1"))

	status, err := mm.Status("bf")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusSearching, status)
	reason, ok := mm.StatusReason("bf")
	assert.True(t, ok)
	assert.Equal(t, flexi.StatusReasonAcceptanceFailed, reason)
	assert.Equal(t, 1, mm.Pending(), "the backfill request is queued again")

	rejected, err := mm.Status("t1")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusCancelled, rejected)
}

// Purpose: Verify requestTimeoutSeconds governs a backfill request as it does any
// other, so a session that never finds a player stops waiting. AWS leaves it to
// the caller to submit a fresh request afterwards.
// Method:  Enqueue a backfill request with nobody to match it, advance past the
// rule set's 30s request timeout, and Tick.
// Expect:  The request is TIMED_OUT and out of the queue.
func TestTick_BackfillTimesOutLikeAnyRequest(t *testing.T) {
	clock := flexi.NewFakeClock(time.Unix(0, 0))
	mm, err := flexi.New([]byte(`{
	  "name": "backfill-timeout",
	  "ruleLanguageVersion": "1.0",
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ],
	  "requestTimeoutSeconds": 30
	}`), flexi.WithClock(clock))
	require.NoError(t, err)

	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf", Players: []flexi.Player{
		seatedPlayer("r1", "red", 10), seatedPlayer("r2", "red", 11), seatedPlayer("b1", "blue", 12),
	}}))
	clock.Advance(30 * time.Second)

	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches)
	status, err := mm.Status("bf")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusTimedOut, status)
	assert.Zero(t, mm.Pending())
}

// Purpose: Verify expansions reach a backfill request as they do a regular one:
// the rules are evaluated over the existing players and the candidate together,
// so a candidate too far from the session's skill level joins only once waiting
// has loosened the rule.
// Method:  A session of two skill-10 players needs two more, but the only
// candidates are at skill 60 and the skill rule allows a distance of 10.
// Tick, advance past the expansion's 30s step, and Tick again.
// Expect:  No match at first; after the expansion widens the distance to 100 the
// match forms.
func TestTick_BackfillMatchesAfterExpansionLoosensTheRules(t *testing.T) {
	clock := flexi.NewFakeClock(time.Unix(0, 0))
	mm, err := flexi.New([]byte(`{
	  "name": "backfill-expansion",
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
	  "expansions": [
	    {"target": "rules[FairSkill].maxDistance",
	     "steps": [{"waitTimeSeconds": 30, "value": 100}]}
	  ]
	}`), flexi.WithClock(clock))
	require.NoError(t, err)

	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf", Players: []flexi.Player{
		seatedPlayer("r1", "red", 10), seatedPlayer("r2", "red", 10),
	}}))
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "t1", Players: []flexi.Player{newPlayer("n1", 60)}}))
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "t2", Players: []flexi.Player{newPlayer("n2", 60)}}))

	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "skill distance 50 exceeds the initial maxDistance of 10")

	clock.Advance(30 * time.Second)
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, []string{"bf", "t1", "t2"}, matches[0].TicketIDs)
	assert.Equal(t, []string{"n1", "n2"}, playerIDs(matches[0].Teams["blue"]))
}

// Purpose: Verify Cancel withdraws a backfill request, the standalone equivalent
// of StopMatchmaking on a backfill ticket, and that the session may then submit a
// fresh one under the same GameSessionID.
// Method:  Enqueue a backfill request for a session, cancel it, and enqueue
// another for the same session.
// Expect:  The first is CANCELLED; the replacement is accepted and QUEUED.
func TestCancel_BackfillFreesTheSession(t *testing.T) {
	mm, err := flexi.New([]byte(backfillRS))
	require.NoError(t, err)

	roster := []flexi.Player{seatedPlayer("r1", "red", 10)}
	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf1", GameSessionID: "gs-1", Players: roster}))
	require.NoError(t, mm.Cancel("bf1"))
	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf2", GameSessionID: "gs-1", Players: roster}))

	first, err := mm.Status("bf1")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusCancelled, first)
	second, err := mm.Status("bf2")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusQueued, second)
}

func playerIDs(players []flexi.Player) []string {
	out := make([]string, len(players))
	for i, p := range players {
		out[i] = p.ID
	}
	return out
}

// Purpose: Verify Enqueue produces a regular ticket regardless of the Backfill
// field, which is documented as set by EnqueueBackfill and overwritten otherwise.
// A ticket that kept Backfill would never be placed by the greedy search, so
// honouring the caller's value would silently strand it.
// Method:  Enqueue four tickets that set Backfill true but name no team, into a
// rule set needing exactly those four players, then Tick.
// Expect:  A match consuming all four.
func TestEnqueue_IgnoresCallerSuppliedBackfillFlag(t *testing.T) {
	mm, err := flexi.New([]byte(backfillRS))
	require.NoError(t, err)

	for i, id := range []string{"t1", "t2", "t3", "t4"} {
		require.NoError(t, mm.Enqueue(flexi.Ticket{
			ID: id, Backfill: true, Players: []flexi.Player{newPlayer(fmt.Sprintf("n%d", i), 10)},
		}))
	}
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, []string{"t1", "t2", "t3", "t4"}, matches[0].TicketIDs)
}

// Purpose: Verify a backfill roster passes through the same attribute handling as
// any other ticket. The rules are evaluated over the seated players, so a missing
// declared attribute must take its default and a mistyped one must be rejected —
// otherwise the roster would be judged against different data than it carries.
// Method:  Against a rule set declaring skill with a default of 42, submit a
// roster that omits skill, and one that supplies it as a string.
// Expect:  The omitting roster matches and reports the default on the seated
// player; the mistyped roster is rejected at enqueue.
func TestEnqueueBackfill_AppliesDefaultsAndChecksAttributeTypes(t *testing.T) {
	body := []byte(`{
	  "name": "backfill-attrs",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name": "skill", "type": "number", "default": 42}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}]
	}`)
	mm, err := flexi.New(body)
	require.NoError(t, err)

	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf", Players: []flexi.Player{{ID: "seated", Team: "all"}}}))
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "t1", Players: []flexi.Player{newPlayer("n1", 10)}}))
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	seated := matches[0].Teams["all"][0]
	require.Equal(t, "seated", seated.ID)
	assert.Equal(t, flexi.Number(42), seated.Attributes["skill"], "the declared default reached the seated player")

	mm2, err := flexi.New(body)
	require.NoError(t, err)
	err = mm2.EnqueueBackfill(flexi.Ticket{ID: "bf", Players: []flexi.Player{
		{ID: "seated", Team: "all", Attributes: flexi.Attributes{"skill": flexi.String("high")}},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "skill")
}

// Purpose: Verify a backfill request stops being replaceable once its match is
// being placed, and becomes replaceable again after the caller reports the
// placement done. This is the protocol a game server follows to refill the same
// session repeatedly.
// Method:  Match a backfill request, attempt a second request for its session,
// then MarkCompleted both tickets and attempt again.
// Expect:  ErrBackfillInProgress while PLACING; accepted once COMPLETED.
func TestEnqueueBackfill_RefusesWhileSessionRequestIsPlacing(t *testing.T) {
	mm, err := flexi.New([]byte(backfillRS))
	require.NoError(t, err)

	roster := []flexi.Player{
		seatedPlayer("r1", "red", 10), seatedPlayer("r2", "red", 11), seatedPlayer("b1", "blue", 12),
	}
	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf1", GameSessionID: "gs-1", Players: roster}))
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "t1", Players: []flexi.Player{newPlayer("n1", 13)}}))
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)

	err = mm.EnqueueBackfill(flexi.Ticket{ID: "bf2", GameSessionID: "gs-1", Players: roster})
	assert.True(t, errors.Is(err, flexi.ErrBackfillInProgress), "err: %v", err)

	require.NoError(t, mm.MarkCompleted("bf1"))
	assert.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf3", GameSessionID: "gs-1", Players: roster}))
}

// Purpose: Verify a backfill request that returned to the queue after a failed
// acceptance is still replaceable. It is waiting for a match again, so the
// session is free to describe its roster afresh.
// Method:  Drive a backfill request to SEARCHING through a rejected proposal,
// then enqueue another request for the same session.
// Expect:  The searching request is CANCELLED and the new one is QUEUED.
func TestEnqueueBackfill_SupersedesSearchingRequest(t *testing.T) {
	mm, err := flexi.New([]byte(`{
	  "name": "backfill-accept",
	  "ruleLanguageVersion": "1.0",
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ],
	  "acceptanceRequired": true,
	  "acceptanceTimeoutSeconds": 60
	}`))
	require.NoError(t, err)

	roster := []flexi.Player{
		seatedPlayer("r1", "red", 10), seatedPlayer("r2", "red", 11), seatedPlayer("b1", "blue", 12),
	}
	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf1", GameSessionID: "gs-1", Players: roster}))
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "t1", Players: []flexi.Player{newPlayer("n1", 13)}}))
	_, err = mm.Tick()
	require.NoError(t, err)
	for _, id := range []string{"r1", "r2", "b1"} {
		require.NoError(t, mm.Accept("bf1", id))
	}
	require.NoError(t, mm.Reject("t1", "n1"))
	searching, err := mm.Status("bf1")
	require.NoError(t, err)
	require.Equal(t, flexi.StatusSearching, searching)

	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf2", GameSessionID: "gs-1", Players: roster}))
	first, err := mm.Status("bf1")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusCancelled, first)
	assert.Equal(t, 1, mm.Pending())
}

// Purpose: Verify the one-request-per-session rule reaches only backfill
// requests. GameSessionID has no meaning on a regular ticket, so a backfill
// request must not evict a queued player who happens to carry the same value.
// Method:  Enqueue a regular ticket with a GameSessionID, then a backfill request
// for the same session.
// Expect:  Both are QUEUED.
func TestEnqueueBackfill_DoesNotSupersedeRegularTickets(t *testing.T) {
	mm, err := flexi.New([]byte(backfillRS))
	require.NoError(t, err)

	require.NoError(t, mm.Enqueue(flexi.Ticket{
		ID: "t1", GameSessionID: "gs-1", Players: []flexi.Player{newPlayer("n1", 10)},
	}))
	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{
		ID: "bf", GameSessionID: "gs-1", Players: []flexi.Player{seatedPlayer("r1", "red", 10)},
	}))

	regular, err := mm.Status("t1")
	require.NoError(t, err)
	assert.Equal(t, flexi.StatusQueued, regular)
	assert.Equal(t, 2, mm.Pending())
}

// Purpose: Verify ticket IDs are unique across both entry points, so a backfill
// request cannot shadow a ticket already in matchmaking.
// Method:  Enqueue a regular ticket, then a backfill request reusing its ID, and
// the reverse.
// Expect:  Both collisions return ErrDuplicateTicket.
func TestEnqueueBackfill_RejectsDuplicateTicketID(t *testing.T) {
	mm, err := flexi.New([]byte(backfillRS))
	require.NoError(t, err)

	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "dup", Players: []flexi.Player{newPlayer("n1", 10)}}))
	err = mm.EnqueueBackfill(flexi.Ticket{ID: "dup", Players: []flexi.Player{seatedPlayer("r1", "red", 10)}})
	assert.ErrorIs(t, err, flexi.ErrDuplicateTicket)

	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf", Players: []flexi.Player{seatedPlayer("r2", "red", 10)}}))
	err = mm.Enqueue(flexi.Ticket{ID: "bf", Players: []flexi.Player{newPlayer("n2", 10)}})
	assert.ErrorIs(t, err, flexi.ErrDuplicateTicket)
}

// Purpose: Verify an expansion can retarget algorithm.backfillPriority while
// tickets wait. The property is an expansion target, and now that it steers the
// search a rule set can use it to fall back on backfilling only once forming a
// new match has taken too long.
// Method:  Queue two solo tickets — one short of a new match — plus a backfill
// request that is not the oldest ticket, so the default priority passes it over.
// Expand backfillPriority to "high" after 30s and Tick either side of that.
// Expect:  No match while the default applies; once "high" takes effect the
// backfill request is matched with the two waiting tickets.
func TestTick_ExpansionCanRetargetBackfillPriority(t *testing.T) {
	clock := flexi.NewFakeClock(time.Unix(0, 0))
	mm, err := flexi.New([]byte(`{
	  "name": "backfill-priority-expansion",
	  "ruleLanguageVersion": "1.0",
	  "algorithm": {"strategy": "exhaustiveSearch"},
	  "teams": [{"name": "all", "minPlayers": 3, "maxPlayers": 3}],
	  "expansions": [
	    {"target": "algorithm.backfillPriority",
	     "steps": [{"waitTimeSeconds": 30, "value": "high"}]}
	  ]
	}`), flexi.WithClock(clock))
	require.NoError(t, err)

	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "t1", Players: []flexi.Player{newPlayer("n1", 10)}}))
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "t2", Players: []flexi.Player{newPlayer("n2", 11)}}))
	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf", Players: []flexi.Player{seatedPlayer("p1", "all", 12)}}))

	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "the default priority passes over a backfill request that is not the oldest")

	clock.Advance(30 * time.Second)
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, []string{"bf", "t1", "t2"}, matches[0].TicketIDs)
}

// Purpose: Verify rule-evaluation metrics are accumulated for a backfill request
// like any other ticket, so a session whose request times out can be diagnosed
// from the same data as a player's.
// Method:  Let a backfill request wait through a Tick that evaluates a rule but
// forms no match, then read its metrics.
// Expect:  Metrics are reported and name the rule set's rule.
func TestRuleMetrics_TracksBackfillRequests(t *testing.T) {
	mm, err := flexi.New([]byte(`{
	  "name": "backfill-metrics",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name": "skill", "type": "number"}],
	  "teams": [{"name": "all", "minPlayers": 3, "maxPlayers": 3}],
	  "rules": [{"name": "BD", "type": "batchDistance", "batchAttribute": "skill", "maxDistance": 5}]
	}`))
	require.NoError(t, err)

	require.NoError(t, mm.EnqueueBackfill(flexi.Ticket{ID: "bf", Players: []flexi.Player{seatedPlayer("p1", "all", 10)}}))
	require.NoError(t, mm.Enqueue(flexi.Ticket{ID: "t1", Players: []flexi.Player{newPlayer("n1", 500)}}))
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Empty(t, matches)

	metrics, ok := mm.RuleMetrics("bf")
	require.True(t, ok, "a backfill request that took part in a search reports metrics")
	require.Len(t, metrics, 1)
	assert.Equal(t, "BD", metrics[0].RuleName)
	assert.Positive(t, metrics[0].FailedCount)
}
