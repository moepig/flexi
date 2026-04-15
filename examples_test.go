package flexi_test

import (
	"errors"
	"testing"
	"time"

	"github.com/moepig/flexi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const skillRS = `{
  "name": "skill-balance",
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
  ]
}`

func solo(id string, skill float64) flexi.Ticket {
	return flexi.Ticket{ID: id, Players: []flexi.Player{{
		ID: id, Attributes: flexi.Attributes{"skill": flexi.Number(skill)},
	}}}
}

func soloLat(id string, latencies map[string]int) flexi.Ticket {
	return flexi.Ticket{ID: id, Players: []flexi.Player{{ID: id, Latencies: latencies}}}
}

// Purpose: Verify end-to-end match formation with a minimal rule set (distance rule only, no acceptance).
// Method:  Enqueue four skill-balanced solo tickets and call Tick once.
// Expect:  One Match is returned with two players per team and Pending drops to 0.
func TestEndToEnd_BasicMatch(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)

	for _, tk := range []flexi.Ticket{solo("a", 50), solo("b", 52), solo("c", 49), solo("d", 51)} {
		require.NoError(t, mm.Enqueue(tk))
	}
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Len(t, matches[0].Teams["red"], 2)
	assert.Len(t, matches[0].Teams["blue"], 2)
	assert.Equal(t, 0, mm.Pending())
}

// Purpose: Verify that an expansion step's relaxed maxAttributeDistance is applied after the wait time elapses.
// Method:  Fix the clock, enqueue two tickets whose skill gap exceeds the initial limit, Tick (no match),
//
//	then advance the FakeClock by 31 seconds and Tick again.
//
// Expect:  First Tick: 0 matches, Pending=2. After 31 seconds: 1 match, Pending=0.
func TestEndToEnd_NoMatchUntilExpansion(t *testing.T) {
	body := `{
	  "name": "expand",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [
	    {"name": "Tight", "type": "batchDistance",
	     "batchAttribute": "skill", "maxAttributeDistance": 5}
	  ],
	  "expansions": [
	    {"target": "rules[Tight].maxAttributeDistance",
	     "steps": [{"waitTimeSeconds": 30, "value": 100}]}
	  ]
	}`
	clock := flexi.NewFakeClock(time.Unix(1_700_000_000, 0))
	mm, err := flexi.New([]byte(body), flexi.WithClock(clock))
	require.NoError(t, err)

	require.NoError(t, mm.Enqueue(solo("a", 10)))
	require.NoError(t, mm.Enqueue(solo("b", 80)))

	// Before expansion: no match (distance 70 > 5).
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches)
	assert.Equal(t, 2, mm.Pending())

	// Advance past the expansion step; now maxDistance becomes 100.
	clock.Advance(31 * time.Second)
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, 0, mm.Pending())
}

// Purpose: Verify that the latency rule allows a match when all players satisfy the threshold in a shared region.
// Method:  Enqueue two tickets with us-east-1 latencies of 50ms and 70ms against maxLatency=80, then Tick.
// Expect:  One Match is formed (us-east-1 satisfies both players).
func TestEndToEnd_LatencyRule(t *testing.T) {
	body := `{
	  "name": "lat",
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "P", "type": "latency", "maxLatency": 80}]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(soloLat("a", map[string]int{"us-east-1": 50, "us-west-2": 200})))
	require.NoError(t, mm.Enqueue(soloLat("b", map[string]int{"us-east-1": 70, "us-west-2": 30})))
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "us-east-1 satisfies both")
}

// Purpose: Verify that the latency rule blocks a match when no shared region satisfies the threshold for all players.
// Method:  Enqueue two tickets with us-east-1 latencies 100ms and 10ms against maxLatency=50, then Tick.
// Expect:  Zero matches (100ms exceeds the limit, so no valid shared region exists).
func TestEndToEnd_LatencyRule_NoMatch(t *testing.T) {
	body := `{
	  "name": "lat",
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "P", "type": "latency", "maxLatency": 50}]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(soloLat("a", map[string]int{"us-east-1": 100})))
	require.NoError(t, mm.Enqueue(soloLat("b", map[string]int{"us-east-1": 10})))
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches)
}

// Purpose: Verify that Cancel removes a queued ticket and returns ErrUnknownTicket for an unknown ID.
// Method:  Enqueue two tickets, Cancel "a", then attempt to Cancel the non-existent ID "nope".
// Expect:  Pending drops to 1; the second Cancel returns ErrUnknownTicket.
func TestEndToEnd_CancelTicket(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 50)))
	require.NoError(t, mm.Enqueue(solo("b", 51)))
	require.NoError(t, mm.Cancel("a"))
	assert.Equal(t, 1, mm.Pending())

	err = mm.Cancel("nope")
	assert.True(t, errors.Is(err, flexi.ErrUnknownTicket))
}

// Purpose: Verify that enqueueing the same ticket ID twice returns ErrDuplicateTicket.
// Method:  Enqueue ID "a" twice in succession.
// Expect:  The second Enqueue returns ErrDuplicateTicket.
func TestEndToEnd_DuplicateTicket(t *testing.T) {
	mm, err := flexi.New([]byte(skillRS))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 1)))
	err = mm.Enqueue(solo("a", 1))
	assert.True(t, errors.Is(err, flexi.ErrDuplicateTicket))
}

// Purpose: Verify that an invalid rule set JSON is rejected by flexi.New.
// Method:  Pass a minimal JSON that is missing the required "teams" field to flexi.New.
// Expect:  An error wrapping ErrInvalidRuleSet is returned.
func TestEndToEnd_InvalidRuleSet(t *testing.T) {
	_, err := flexi.New([]byte(`{"name":"x"}`))
	assert.True(t, errors.Is(err, flexi.ErrInvalidRuleSet))
}

// Purpose: Verify that players in a party ticket are never split across teams.
// Method:  Enqueue one two-player party ticket alongside two solo tickets; inspect the resulting Match.
// Expect:  One Match is formed, and p1/p2 land on the same team in every assignment.
func TestEndToEnd_PartyTicket(t *testing.T) {
	body := `{
	  "name": "party",
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	party := flexi.Ticket{ID: "duo", Players: []flexi.Player{
		{ID: "p1"}, {ID: "p2"},
	}}
	require.NoError(t, mm.Enqueue(party))
	require.NoError(t, mm.Enqueue(solo("c", 0)))
	require.NoError(t, mm.Enqueue(solo("d", 0)))
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	// duo should land entirely on one team
	for name, ps := range matches[0].Teams {
		ids := []string{}
		for _, p := range ps {
			ids = append(ids, p.ID)
		}
		_ = name
		bothInDuo := contains(ids, "p1") && contains(ids, "p2")
		neitherInDuo := !contains(ids, "p1") && !contains(ids, "p2")
		assert.True(t, bothInDuo || neitherInDuo, "party split across teams: %v", ids)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
