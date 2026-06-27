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
     "measurements": ["avg(teams[red].players.attributes[skill])"],
     "referenceValue": "avg(teams[blue].players.attributes[skill])",
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

func soloAttr(id string, attrs flexi.Attributes) flexi.Ticket {
	return flexi.Ticket{ID: id, Players: []flexi.Player{{ID: id, Attributes: attrs}}}
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

// Purpose: Verify that an expansion step's relaxed maxDistance is applied after the wait time elapses.
// Method:  Fix the clock, enqueue two tickets whose skill gap exceeds the initial limit, Tick (no match),
//
//	then advance the FakeClock by 31 seconds and Tick again.
//
// Expect:  First Tick: 0 matches, Pending=2. After 31 seconds: 1 match, Pending=0.
func TestEndToEnd_NoMatchUntilExpansion(t *testing.T) {
	body := `{
	  "name": "expand",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [
	    {"name": "Tight", "type": "batchDistance",
	     "batchAttribute": "skill", "maxDistance": 5}
	  ],
	  "expansions": [
	    {"target": "rules[Tight].maxDistance",
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
	  "ruleLanguageVersion": "1.0",
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
	  "ruleLanguageVersion": "1.0",
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
	  "ruleLanguageVersion": "1.0",
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

// Purpose: Verify that multiple party tickets in one match each stay intact on a
// single team (no party is split across teams).
// Method:  Two two-player parties into two teams of exactly 2; Tick once.
// Expect:  One match where each team holds exactly one whole party.
func TestEndToEnd_MultiplePartiesNotSplit(t *testing.T) {
	body := `{
	  "name": "parties",
	  "ruleLanguageVersion": "1.0",
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	duo1 := flexi.Ticket{ID: "duo1", Players: []flexi.Player{{ID: "p1"}, {ID: "p2"}}}
	duo2 := flexi.Ticket{ID: "duo2", Players: []flexi.Player{{ID: "p3"}, {ID: "p4"}}}
	require.NoError(t, mm.Enqueue(duo1))
	require.NoError(t, mm.Enqueue(duo2))

	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)

	duo1Set := map[string]bool{"p1": true, "p2": true}
	for _, ps := range matches[0].Teams {
		require.Len(t, ps, 2, "each team holds exactly one duo")
		inDuo1 := 0
		for _, p := range ps {
			if duo1Set[p.ID] {
				inDuo1++
			}
		}
		assert.True(t, inDuo1 == 0 || inDuo1 == 2, "a duo was split across teams: %v", ps)
	}
}

// Purpose: Verify that a playerAttributes default is applied to players that omit
// the attribute, so rules referencing it still evaluate.
// Method:  skill defaults to 50; a batchDistance(maxDistance=5) over four tickets
//
//	where two omit skill entirely (defaulted to 50).
//
// Expect:  A match forms because every player's effective skill is within 5.
func TestEndToEnd_PlayerAttributeDefault(t *testing.T) {
	body := `{
	  "name": "defaults",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name": "skill", "type": "number", "default": 50}],
	  "teams": [{"name": "all", "minPlayers": 4, "maxPlayers": 4}],
	  "rules": [{"name": "Tight", "type": "batchDistance",
	    "batchAttribute": "skill", "maxDistance": 5}]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)

	noAttr := func(id string) flexi.Ticket {
		return flexi.Ticket{ID: id, Players: []flexi.Player{{ID: id}}}
	}
	require.NoError(t, mm.Enqueue(solo("a", 48)))
	require.NoError(t, mm.Enqueue(solo("b", 52)))
	require.NoError(t, mm.Enqueue(noAttr("c"))) // skill defaults to 50
	require.NoError(t, mm.Enqueue(noAttr("d"))) // skill defaults to 50

	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Len(t, matches[0].Teams["all"], 4)
}

// Purpose: Verify expansionAgeSelection="oldest" measures expansion wait time
// from the oldest queued ticket rather than the newest.
// Method:  Enqueue ticket "a", advance 31s, enqueue "b" (skill gap > initial
//
//	limit), then Tick. The expansion step is 30s.
//
// Expect:  With "oldest", a's 31s wait triggers the expansion and a match forms
//
//	on the first Tick after b joins.
func TestEndToEnd_ExpansionAgeSelectionOldest(t *testing.T) {
	body := `{
	  "name": "expand-oldest",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "algorithm": {"expansionAgeSelection": "oldest"},
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "Tight", "type": "batchDistance",
	    "batchAttribute": "skill", "maxDistance": 5}],
	  "expansions": [{"target": "rules[Tight].maxDistance",
	    "steps": [{"waitTimeSeconds": 30, "value": 100}]}]
	}`
	clock := flexi.NewFakeClock(time.Unix(1_700_000_000, 0))
	mm, err := flexi.New([]byte(body), flexi.WithClock(clock))
	require.NoError(t, err)

	require.NoError(t, mm.Enqueue(solo("a", 10)))
	clock.Advance(31 * time.Second)
	require.NoError(t, mm.Enqueue(solo("b", 80)))

	// Oldest ticket "a" has waited 31s (> 30s step), so the expansion applies
	// even though "b" just joined.
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
}

// Purpose: Verify expansionAgeSelection="newest" (the default) measures expansion
// wait time from the most recently added ticket, so a fresh arrival restarts the
// expansion clock.
// Method:  Enqueue "a", advance 31s, enqueue "b" (skill gap > initial limit). The
//
//	expansion step is 30s. Tick once, then advance 31s and Tick again.
//
// Expect:  First Tick forms no match (newest ticket "b" has waited 0s); after a
//
//	further 31s the expansion applies and a match forms.
func TestEndToEnd_ExpansionAgeSelectionNewest(t *testing.T) {
	body := `{
	  "name": "expand-newest",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "algorithm": {"expansionAgeSelection": "newest"},
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "Tight", "type": "batchDistance",
	    "batchAttribute": "skill", "maxDistance": 5}],
	  "expansions": [{"target": "rules[Tight].maxDistance",
	    "steps": [{"waitTimeSeconds": 30, "value": 100}]}]
	}`
	clock := flexi.NewFakeClock(time.Unix(1_700_000_000, 0))
	mm, err := flexi.New([]byte(body), flexi.WithClock(clock))
	require.NoError(t, err)

	require.NoError(t, mm.Enqueue(solo("a", 10)))
	clock.Advance(31 * time.Second)
	require.NoError(t, mm.Enqueue(solo("b", 80)))

	// Newest ticket "b" has waited 0s, so the expansion has NOT triggered yet.
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches)

	// After another 31s the newest ticket has aged past the 30s step.
	clock.Advance(31 * time.Second)
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
}

// Purpose: Verify an absoluteSort rule influences which tickets are matched through
// the full Enqueue→Tick flow (not just orderBatch in isolation).
// Method:  One team of exactly 2; four solo tickets a(50,anchor),b(90),c(10),d(30);
//
//	absoluteSort ascending by skill. After the anchor, the lowest-skill ticket
//	is ordered next and joins the anchor's match.
//
// Expect:  The first match contains "a" and "c" (not "b").
func TestEndToEnd_AbsoluteSortInTick(t *testing.T) {
	body := `{
	  "name": "sort-tick",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "S", "type": "absoluteSort",
	    "sortDirection": "ascending", "sortAttribute": "skill"}]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	for _, tk := range []flexi.Ticket{solo("a", 50), solo("b", 90), solo("c", 10), solo("d", 30)} {
		require.NoError(t, mm.Enqueue(tk))
	}
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.NotEmpty(t, matches)
	assert.Equal(t, []string{"a", "c"}, matches[0].TicketIDs,
		"absoluteSort places the lowest-skill ticket with the anchor")
}

// Purpose: Verify that when a rule set defines multiple compound rules, ALL of
// them must hold for a match to form (FlexMatch: "all compound rules must be
// true to form a match").
// Method:  children A (avg<=50) and B (max<=80); compounds Any=or(A,B) and
//
//	Both=and(A,B). Enqueue a pair where Any holds but Both fails; then a pair
//	that satisfies both.
//
// Expect:  the first pair forms no match; once a satisfiable pair is available a
//
//	match forms.
func TestEndToEnd_MultipleCompoundRulesAllMustHold(t *testing.T) {
	body := `{
	  "name": "multi-compound",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [
	    {"name":"A","type":"comparison","measurements":["avg(players.attributes[skill])"],"referenceValue":50,"operation":"<="},
	    {"name":"B","type":"comparison","measurements":["max(players.attributes[skill])"],"referenceValue":80,"operation":"<="},
	    {"name":"Any","type":"compound","statement":"or(A,B)"},
	    {"name":"Both","type":"compound","statement":"and(A,B)"}
	  ]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)

	// avg(10,90)=50 → A true; max=90 → B false. Any holds but Both fails.
	require.NoError(t, mm.Enqueue(solo("a", 10)))
	require.NoError(t, mm.Enqueue(solo("b", 90)))
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "Both=and(A,B) fails, so no match forms even though Any holds")

	// avg(10,30)=20 → A true; max=30 → B true: a satisfiable pair now exists.
	require.NoError(t, mm.Enqueue(solo("c", 10)))
	require.NoError(t, mm.Enqueue(solo("d", 30)))
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "a pair satisfying both compound rules forms a match")
}

// The tests below mirror, at the public Enqueue→Tick level, the FlexMatch
// spec-compliance fixes made on this branch. Each previously had only
// internal package-level coverage; these confirm the corrected behaviour is
// observable end-to-end and changes real match outcomes.

// Purpose: Verify a party (multi-player ticket) collapses to its AVERAGE before a
// distance rule is evaluated when partyAggregation is omitted (FlexMatch default
// "avg"), rather than each member being checked individually.
// Method:  one team of exactly 2 filled by a single party {p1:10, p2:30}; a
//
//	distance rule with referenceValue 20, maxDistance 5 and no partyAggregation.
//
// Expect:  a match forms — the party averages to 20 (diff 0). Had members been
//
//	evaluated individually, the skill-10 player (diff 10 > 5) would block it.
func TestEndToEnd_PartyAggregationDefaultsToAvg(t *testing.T) {
	body := `{
	  "name": "party-avg",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "Near", "type": "distance",
	    "measurements": ["players.attributes[skill]"],
	    "referenceValue": 20, "maxDistance": 5}]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	party := flexi.Ticket{ID: "duo", Players: []flexi.Player{
		{ID: "p1", Attributes: flexi.Attributes{"skill": flexi.Number(10)}},
		{ID: "p2", Attributes: flexi.Attributes{"skill": flexi.Number(30)}},
	}}
	require.NoError(t, mm.Enqueue(party))
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "party averages to 20, matching referenceValue 20")
}

// Purpose: Verify the comparison "compare across players" form — a comparison
// with no referenceValue compares players against each other, which FlexMatch
// restricts to the = and != operations.
// Method:  a comparison rule on players.attributes[character] with operation "!="
//
//	(every player's character must differ) and no referenceValue.
//
// Expect:  two players sharing a character form no match; once a third player with
//
//	a distinct character arrives, a valid distinct pair matches.
func TestEndToEnd_ComparisonAcrossPlayers(t *testing.T) {
	body := `{
	  "name": "distinct-chars",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"character","type":"string"}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "Unique", "type": "comparison",
	    "measurements": ["players.attributes[character]"], "operation": "!="}]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(soloAttr("a", flexi.Attributes{"character": flexi.String("mage")})))
	require.NoError(t, mm.Enqueue(soloAttr("b", flexi.Attributes{"character": flexi.String("mage")})))
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "identical characters fail !=")

	require.NoError(t, mm.Enqueue(soloAttr("c", flexi.Attributes{"character": flexi.String("rogue")})))
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "a distinct pair (mage/rogue) satisfies !=")
}

// Purpose: Verify the "compare across players" form is rejected at parse time for
// any operation other than = / != (FlexMatch restricts the reference-less form to
// equality operations).
// Method:  a comparison rule with operation "<" and no referenceValue.
// Expect:  flexi.New returns an error wrapping ErrInvalidRuleSet.
func TestEndToEnd_ComparisonAcrossPlayersRejectsOrdering(t *testing.T) {
	body := `{
	  "name": "bad-across",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "Bad", "type": "comparison",
	    "measurements": ["players.attributes[skill]"], "operation": "<"}]
	}`
	_, err := flexi.New([]byte(body))
	assert.ErrorIs(t, err, flexi.ErrInvalidRuleSet)
}

// Purpose: Verify the collection "intersection" operation counts the values shared
// by EVERY player's collection and takes no referenceValue (the FlexMatch
// "SharedMode" example), with minCount bounding that count.
// Method:  a collection rule on players.attributes[modes], operation
//
//	"intersection", minCount 1.
//
// Expect:  players with no common mode form no match; once a player sharing a mode
//
//	arrives, the pair with a non-empty intersection matches.
func TestEndToEnd_CollectionIntersection(t *testing.T) {
	body := `{
	  "name": "shared-mode",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"modes","type":"string_list"}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "SharedMode", "type": "collection",
	    "measurements": ["players.attributes[modes]"],
	    "operation": "intersection", "minCount": 1}]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(soloAttr("a", flexi.Attributes{"modes": flexi.StringList("TDM", "CTF")})))
	require.NoError(t, mm.Enqueue(soloAttr("b", flexi.Attributes{"modes": flexi.StringList("FFA")})))
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "no shared mode → empty intersection")

	require.NoError(t, mm.Enqueue(soloAttr("c", flexi.Attributes{"modes": flexi.StringList("CTF")})))
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "a/c share CTF → non-empty intersection")
}

// Purpose: Verify the collection "contains" operation counts OCCURRENCES of the
// reference value and honours maxCount (the FlexMatch "no more than N medics"
// example), rather than being a mere present/absent boolean that ignores bounds.
// Method:  a collection rule on flatten(players.attributes[character]) with
//
//	operation "contains", referenceValue "medic", maxCount 1.
//
// Expect:  a pair holding two medics forms no match; a pair with at most one medic
//
//	matches.
func TestEndToEnd_CollectionContainsCountsOccurrences(t *testing.T) {
	body := `{
	  "name": "medic-limit",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"character","type":"string_list"}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "MedicLimit", "type": "collection",
	    "measurements": ["flatten(players.attributes[character])"],
	    "operation": "contains", "referenceValue": "medic", "maxCount": 1}]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(soloAttr("a", flexi.Attributes{"character": flexi.StringList("medic")})))
	require.NoError(t, mm.Enqueue(soloAttr("b", flexi.Attributes{"character": flexi.StringList("medic")})))
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "two medics exceed maxCount 1")

	require.NoError(t, mm.Enqueue(soloAttr("c", flexi.Attributes{"character": flexi.StringList("knight")})))
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "a pair with at most one medic is within maxCount 1")
}

// Purpose: Verify a party's collections combine by "union" when partyAggregation
// is omitted (FlexMatch default), versus "intersection" when set explicitly.
// Method:  a single party {p1:[TDM,CTF], p2:[TDM]} fills a team of 2; a collection
//
//	"contains CTF" rule, run once with the default and once with intersection.
//
// Expect:  default (union → [TDM,CTF]) contains CTF and matches; intersection
//
//	(→ [TDM] only) drops CTF and forms no match.
func TestEndToEnd_CollectionPartyAggregationDefaultsToUnion(t *testing.T) {
	body := func(agg string) string {
		return `{
		  "name": "party-union",
		  "ruleLanguageVersion": "1.0",
		  "playerAttributes": [{"name":"modes","type":"string_list"}],
		  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
		  "rules": [{"name": "HasCTF", "type": "collection",
		    "measurements": ["players.attributes[modes]"],
		    "operation": "contains", "referenceValue": "CTF"` + agg + `}]
		}`
	}
	party := func() flexi.Ticket {
		return flexi.Ticket{ID: "duo", Players: []flexi.Player{
			{ID: "p1", Attributes: flexi.Attributes{"modes": flexi.StringList("TDM", "CTF")}},
			{ID: "p2", Attributes: flexi.Attributes{"modes": flexi.StringList("TDM")}},
		}}
	}

	// Default (omitted) partyAggregation unions the party's modes → keeps CTF.
	mm, err := flexi.New([]byte(body("")))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(party()))
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "default union keeps CTF")

	// Explicit intersection drops CTF (only TDM is shared) → no match.
	mm, err = flexi.New([]byte(body(`, "partyAggregation": "intersection"`)))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(party()))
	matches, err = mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "intersection drops CTF (only TDM shared)")
}

// Purpose: Verify reference_intersection_count evaluates EACH player's collection
// against the reference (which may itself be a property expression), per the
// FlexMatch "preferred opponents" example.
// Method:  measurement = each player's myCharacter; reference =
//
//	set_intersection of all players' preferredOpponents; minCount 1.
//
// Expect:  when every player's character is on the common opponents list a match
//
//	forms; when one player's character is off the list, no match forms.
func TestEndToEnd_CollectionReferenceIntersectionCount(t *testing.T) {
	body := `{
	  "name": "opponent-match",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [
	    {"name":"myCharacter","type":"string_list"},
	    {"name":"preferredOpponents","type":"string_list"}
	  ],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "OpponentMatch", "type": "collection",
	    "measurements": ["players.attributes[myCharacter]"],
	    "operation": "reference_intersection_count",
	    "referenceValue": "set_intersection(players.attributes[preferredOpponents])",
	    "minCount": 1}]
	}`
	pass := func(t *testing.T) *flexi.Matchmaker {
		mm, err := flexi.New([]byte(body))
		require.NoError(t, err)
		return mm
	}

	// common preferredOpponents = {mage, knight}; both characters are on it.
	mm := pass(t)
	require.NoError(t, mm.Enqueue(soloAttr("a", flexi.Attributes{
		"myCharacter": flexi.StringList("knight"), "preferredOpponents": flexi.StringList("mage", "knight", "rogue")})))
	require.NoError(t, mm.Enqueue(soloAttr("b", flexi.Attributes{
		"myCharacter": flexi.StringList("mage"), "preferredOpponents": flexi.StringList("mage", "knight")})))
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "every character is on the common opponents list")

	// player b picks rogue, which is not in the common opponents {mage, knight}.
	mm = pass(t)
	require.NoError(t, mm.Enqueue(soloAttr("a", flexi.Attributes{
		"myCharacter": flexi.StringList("knight"), "preferredOpponents": flexi.StringList("mage", "knight", "rogue")})))
	require.NoError(t, mm.Enqueue(soloAttr("b", flexi.Attributes{
		"myCharacter": flexi.StringList("rogue"), "preferredOpponents": flexi.StringList("mage", "knight")})))
	matches, err = mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "a character off the common opponents list fails")
}

// Purpose: Verify a string batchAttribute batches by value equivalency: with no
// maxDistance, every player must share one value (the FlexMatch "SameGameMode"
// form), instead of the rule trivially passing.
// Method:  batchDistance on a string attribute "mode" with no bounds.
// Expect:  two players on different modes form no match; two on the same mode do.
func TestEndToEnd_BatchDistanceStringSameValue(t *testing.T) {
	body := `{
	  "name": "same-mode",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"mode","type":"string"}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "SameMode", "type": "batchDistance", "batchAttribute": "mode"}]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(soloAttr("a", flexi.Attributes{"mode": flexi.String("ranked")})))
	require.NoError(t, mm.Enqueue(soloAttr("b", flexi.Attributes{"mode": flexi.String("casual")})))
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "different modes are not the same value")

	require.NoError(t, mm.Enqueue(soloAttr("c", flexi.Attributes{"mode": flexi.String("ranked")})))
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "a/c share the mode 'ranked'")
}

// Purpose: Verify a string-encoded maxDistance (e.g. "5") is parsed and applied
// like the numeric form, matching the verbatim FlexMatch docs where maxDistance
// is printed as a quoted string.
// Method:  batchDistance on a numeric attribute "skill" with maxDistance "5".
// Expect:  a pair within a spread of 5 matches; a wider pair does not.
func TestEndToEnd_BatchDistanceMaxDistanceStringEncoded(t *testing.T) {
	body := `{
	  "name": "string-distance",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "Tight", "type": "batchDistance",
	    "batchAttribute": "skill", "maxDistance": "5"}]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(solo("a", 10)))
	require.NoError(t, mm.Enqueue(solo("b", 80)))
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "spread 70 exceeds string-encoded maxDistance \"5\"")

	require.NoError(t, mm.Enqueue(solo("c", 13)))
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "a/c spread 3 is within string-encoded maxDistance \"5\"")
}

// Purpose: Verify ruleLanguageVersion is required and must be "1.0".
// Method:  one rule set omitting ruleLanguageVersion and one with an unsupported
//
//	version "2.0".
//
// Expect:  both are rejected with ErrInvalidRuleSet.
func TestEndToEnd_RuleLanguageVersionRequired(t *testing.T) {
	missing := `{
	  "name": "no-version",
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}]
	}`
	_, err := flexi.New([]byte(missing))
	assert.ErrorIs(t, err, flexi.ErrInvalidRuleSet, "ruleLanguageVersion is required")

	wrong := `{
	  "name": "bad-version",
	  "ruleLanguageVersion": "2.0",
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}]
	}`
	_, err = flexi.New([]byte(wrong))
	assert.ErrorIs(t, err, flexi.ErrInvalidRuleSet, "only \"1.0\" is supported")
}

// Purpose: Verify the balanced strategy's balancedAttribute must reference a
// declared playerAttribute of type "number".
// Method:  one rule set whose balancedAttribute names a string attribute, and one
//
//	whose balancedAttribute is not declared at all.
//
// Expect:  both are rejected with ErrInvalidRuleSet; a number-typed reference loads.
func TestEndToEnd_BalancedAttributeMustBeNumber(t *testing.T) {
	wrongType := `{
	  "name": "balanced-wrong-type",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"role","type":"string"}],
	  "algorithm": {"strategy": "balanced", "balancedAttribute": "role"},
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}]
	}`
	_, err := flexi.New([]byte(wrongType))
	assert.ErrorIs(t, err, flexi.ErrInvalidRuleSet, "balancedAttribute must be a number attribute")

	undeclared := `{
	  "name": "balanced-undeclared",
	  "ruleLanguageVersion": "1.0",
	  "algorithm": {"strategy": "balanced", "balancedAttribute": "skill"},
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}]
	}`
	_, err = flexi.New([]byte(undeclared))
	assert.ErrorIs(t, err, flexi.ErrInvalidRuleSet, "balancedAttribute must be declared")

	ok := `{
	  "name": "balanced-ok",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "algorithm": {"strategy": "balanced", "balancedAttribute": "skill"},
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}]
	}`
	_, err = flexi.New([]byte(ok))
	require.NoError(t, err, "a number-typed balancedAttribute is valid")
}
