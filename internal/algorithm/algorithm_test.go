package algorithm

import (
	"fmt"
	"sort"
	"testing"

	"github.com/moepig/flexi/internal/core"
	"github.com/moepig/flexi/internal/rule"
	"github.com/moepig/flexi/internal/ruleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func num(v float64) core.Attribute { return core.Attribute{Kind: core.AttrNumber, N: v} }

func solo(id string, skill float64) core.Ticket {
	return core.Ticket{ID: id, Players: []core.Player{{
		ID: id, Attributes: core.Attributes{"skill": num(skill)},
	}}}
}

func newRS(t *testing.T, body string) *ruleset.RuleSet {
	t.Helper()
	rs, err := ruleset.Parse([]byte(body))
	require.NoError(t, err)
	return rs
}

func evals(t *testing.T, rs *ruleset.RuleSet) []rule.Evaluator {
	t.Helper()
	out := []rule.Evaluator{}
	others := map[string]rule.Evaluator{}
	for i := range rs.Rules {
		ev, err := rule.Build(&rs.Rules[i], others)
		require.NoError(t, err)
		others[rs.Rules[i].Name] = ev
		out = append(out, ev)
	}
	return out
}

// Purpose: Verify that Build assembles four solo tickets into a single two-team match.
// Method:  Supply a rule set with red/blue teams (2 players each) and four solo tickets, then call Build.
// Expect:  One Result with 2 players per team, 4 TicketIDs, and an empty remaining slice.
func TestBuild_FormsTwoTeams(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ]
	}`)
	tickets := []core.Ticket{solo("a", 10), solo("b", 11), solo("c", 12), solo("d", 13)}
	out, remaining, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.Empty(t, remaining)
	assert.Len(t, out[0].Teams["red"], 2)
	assert.Len(t, out[0].Teams["blue"], 2)
	assert.Len(t, out[0].TicketIDs, 4)
}

// Purpose: Verify that the batchDistance rule causes Build to exclude skill outliers from the match.
// Method:  Call Build with skills [10, 100, 11, 12] and maxDistance=5.
// Expect:  One match is formed and the outlier ticket "b" (skill=100) is absent from TicketIDs.
func TestBuild_RespectsBatchDistance(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "all", "minPlayers": 3, "maxPlayers": 3}],
	  "rules": [{"name": "BD", "type": "batchDistance",
	    "batchAttribute": "skill", "maxDistance": 5}]
	}`)
	tickets := []core.Ticket{solo("a", 10), solo("b", 100), solo("c", 11), solo("d", 12)}
	out, _, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	ids := append([]string(nil), out[0].TicketIDs...)
	sort.Strings(ids)
	assert.NotContains(t, ids, "b")
}

// Purpose: Verify that Build returns no match when the available tickets cannot satisfy minPlayers.
// Method:  Provide only 2 solo tickets against a team requiring minPlayers=4 and call Build.
// Expect:  Empty Result slice; remaining still contains the original 2 tickets.
func TestBuild_NoMatchUnderMin(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "all", "minPlayers": 4, "maxPlayers": 4}]
	}`)
	tickets := []core.Ticket{solo("a", 10), solo("b", 11)}
	out, remaining, _ := Build(rs, evals(t, rs), tickets)
	assert.Empty(t, out)
	assert.Len(t, remaining, 2)
}

// Purpose: Verify that a team with quantity>1 is expanded into suffixed slots ("_1", "_2", …).
// Method:  Provide a rule set with team{minPlayers:2, maxPlayers:2, quantity:2} and four solo tickets.
// Expect:  Teams["team_1"] and Teams["team_2"] each receive 2 players.
func TestBuild_QuantityExpansion(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "team", "minPlayers": 2, "maxPlayers": 2, "quantity": 2}]
	}`)
	tickets := []core.Ticket{solo("a", 1), solo("b", 2), solo("c", 3), solo("d", 4)}
	out, _, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.Len(t, out[0].Teams["team_1"], 2)
	assert.Len(t, out[0].Teams["team_2"], 2)
}

// Purpose: Verify that a team with quantity omitted defaults to a single,
// unsuffixed team (FlexMatch: quantity default is 1).
// Method:  team{minPlayers:2, maxPlayers:2} with no quantity; two solo tickets.
// Expect:  one match with a single team named "team" (no "team_1").
func TestBuild_QuantityDefaultsToOne(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "team", "minPlayers": 2, "maxPlayers": 2}]
	}`)
	tickets := []core.Ticket{solo("a", 1), solo("b", 2)}
	out, _, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.Len(t, out[0].Teams["team"], 2, "single unsuffixed team")
	_, suffixed := out[0].Teams["team_1"]
	assert.False(t, suffixed, "quantity 1 is not suffixed")
}

// Purpose: Verify that the balanced strategy distributes players so that team attribute sums are close.
// Method:  Call Build with skills [10, 100, 11, 99] using strategy=balanced / balancedAttribute=skill.
// Expect:  The difference between red and blue skill totals is within 25 (high/low pairing is applied).
func TestBuild_BalancedStrategy(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "algorithm": {"strategy": "balanced", "balancedAttribute": "skill"},
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ]
	}`)
	tickets := []core.Ticket{solo("a", 10), solo("b", 100), solo("c", 11), solo("d", 99)}
	out, _, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	red := sumSkill(out[0].Teams["red"])
	blue := sumSkill(out[0].Teams["blue"])
	assert.InDelta(t, red, blue, 25, "red=%v blue=%v", red, blue)
}

func ids(tickets []core.Ticket) []string {
	out := make([]string, len(tickets))
	for i, t := range tickets {
		out[i] = t.ID
	}
	return out
}

func playerIDs(players []core.Player) []string {
	out := make([]string, len(players))
	for i, p := range players {
		out[i] = p.ID
	}
	return out
}

// Purpose: Verify an absoluteSort rule orders tickets[1:] by attribute while
// keeping the oldest ticket as the anchor.
// Method:  Anchor "a" plus three tickets with descending skill; sort ascending.
// Expect:  Anchor stays first; the rest follow in ascending skill order.
func TestOrderBatch_AbsoluteSort(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}],
	  "rules": [{"name": "S", "type": "absoluteSort",
	    "sortDirection": "ascending", "sortAttribute": "skill"}]
	}`)
	tickets := []core.Ticket{solo("a", 50), solo("b", 90), solo("c", 10), solo("d", 30)}
	out := orderBatch(rs, tickets)
	assert.Equal(t, []string{"a", "c", "d", "b"}, ids(out))
}

// Purpose: Verify a distanceSort rule orders tickets[1:] by absolute distance
// from the anchor's attribute value.
// Method:  Anchor "a" skill=50; others at 40/90/55; sort ascending by distance.
// Expect:  Closest-to-50 first: 55(d=5), 40(d=10), 90(d=40).
func TestOrderBatch_DistanceSort(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}],
	  "rules": [{"name": "S", "type": "distanceSort",
	    "sortDirection": "ascending", "sortAttribute": "skill"}]
	}`)
	tickets := []core.Ticket{solo("a", 50), solo("b", 40), solo("c", 90), solo("d", 55)}
	out := orderBatch(rs, tickets)
	assert.Equal(t, []string{"a", "d", "b", "c"}, ids(out))
}

// Purpose: Verify a distanceSort rule honours sortDirection "descending": the
// non-anchor tickets are ordered by decreasing distance from the anchor.
// Method:  Anchor "a" skill=50; others at 40/90/55; sort descending by distance.
// Expect:  Farthest-from-50 first: 90(d=40), 40(d=10), 55(d=5).
func TestOrderBatch_DistanceSort_Descending(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}],
	  "rules": [{"name": "S", "type": "distanceSort",
	    "sortDirection": "descending", "sortAttribute": "skill"}]
	}`)
	tickets := []core.Ticket{solo("a", 50), solo("b", 40), solo("c", 90), solo("d", 55)}
	out := orderBatch(rs, tickets)
	assert.Equal(t, []string{"a", "c", "b", "d"}, ids(out))
}

// Purpose: Pin down the distinction between absoluteSort and distanceSort: an
// absoluteSort orders the non-anchor tickets purely by attribute value, so the
// result is independent of the anchor's own value; a distanceSort orders by
// distance from the anchor, so it does depend on the anchor's value.
// Method:  Two batches identical except for the anchor "a"'s skill (0 vs 100).
// Expect:  absoluteSort yields the same order for both anchors; distanceSort does
//
//	not.
func TestOrderBatch_AbsoluteSortIndependentOfAnchor(t *testing.T) {
	abs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}],
	  "rules": [{"name": "S", "type": "absoluteSort",
	    "sortDirection": "ascending", "sortAttribute": "skill"}]
	}`)
	lowAnchor := []core.Ticket{solo("a", 0), solo("b", 90), solo("c", 10), solo("d", 30)}
	highAnchor := []core.Ticket{solo("a", 100), solo("b", 90), solo("c", 10), solo("d", 30)}
	// Non-anchor tickets sort ascending by value regardless of the anchor value.
	assert.Equal(t, []string{"a", "c", "d", "b"}, ids(orderBatch(abs, lowAnchor)))
	assert.Equal(t, []string{"a", "c", "d", "b"}, ids(orderBatch(abs, highAnchor)))

	dist := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}],
	  "rules": [{"name": "S", "type": "distanceSort",
	    "sortDirection": "ascending", "sortAttribute": "skill"}]
	}`)
	// anchor 0:   |b-a|=90, |c-a|=10, |d-a|=30 → c,d,b
	assert.Equal(t, []string{"a", "c", "d", "b"}, ids(orderBatch(dist, lowAnchor)))
	// anchor 100: |b-a|=10, |c-a|=90, |d-a|=70 → b,d,c
	assert.Equal(t, []string{"a", "b", "d", "c"}, ids(orderBatch(dist, highAnchor)))
}

// Purpose: Verify batchingPreference "sorted" pre-sorts the whole pool by
// sortByAttributes (ascending), including the first ticket.
// Method:  Four tickets with unsorted skills; strategy exhaustiveSearch.
// Expect:  Tickets ordered by ascending skill.
func TestOrderBatch_SortByAttributes(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "algorithm": {"strategy": "exhaustiveSearch", "batchingPreference": "sorted",
	    "sortByAttributes": ["skill"]},
	  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}]
	}`)
	tickets := []core.Ticket{solo("a", 50), solo("b", 90), solo("c", 10), solo("d", 30)}
	out := orderBatch(rs, tickets)
	assert.Equal(t, []string{"c", "d", "a", "b"}, ids(out))
}

func sumSkill(ps []core.Player) float64 {
	var s float64
	for _, p := range ps {
		s += p.Attributes["skill"].N
	}
	return s
}

func soloMap(id string, m map[string]float64) core.Ticket {
	return core.Ticket{ID: id, Players: []core.Player{{
		ID: id, Attributes: core.Attributes{"ping": {Kind: core.AttrStringNumberMap, SDM: m}},
	}}}
}

func party(id string, skills ...float64) core.Ticket {
	ps := make([]core.Player, len(skills))
	for i, s := range skills {
		ps[i] = core.Player{ID: id, Attributes: core.Attributes{"skill": num(s)}}
	}
	return core.Ticket{ID: id, Players: ps}
}

// Purpose: Verify an absoluteSort rule honours sortDirection "descending".
// Method:  Anchor "a" plus three tickets; sort descending by skill.
// Expect:  Anchor stays first; the rest follow in descending skill order.
func TestOrderBatch_AbsoluteSort_Descending(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}],
	  "rules": [{"name": "S", "type": "absoluteSort",
	    "sortDirection": "descending", "sortAttribute": "skill"}]
	}`)
	tickets := []core.Ticket{solo("a", 50), solo("b", 90), solo("c", 10), solo("d", 30)}
	out := orderBatch(rs, tickets)
	assert.Equal(t, []string{"a", "b", "d", "c"}, ids(out))
}

// Purpose: Verify absoluteSort reduces a string_number_map attribute via mapKey
// (minValue vs maxValue) before sorting.
// Method:  Tickets with ping maps; sort ascending by mapKey minValue, then maxValue.
// Expect:  minValue orders by each ticket's lowest ping; maxValue by its highest.
func TestOrderBatch_AbsoluteSort_MapKey(t *testing.T) {
	mk := func(mapKey string) []string {
		rs := newRS(t, `{
		  "name": "x",
		  "ruleLanguageVersion": "1.0",
		  "playerAttributes": [{"name":"ping","type":"string_number_map"}],
		  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}],
		  "rules": [{"name": "S", "type": "absoluteSort",
		    "sortDirection": "ascending", "sortAttribute": "ping", "mapKey": "`+mapKey+`"}]
		}`)
		tickets := []core.Ticket{
			soloMap("a", map[string]float64{"x": 0}),
			soloMap("b", map[string]float64{"x": 90}),
			soloMap("c", map[string]float64{"x": 10, "y": 50}),
			soloMap("d", map[string]float64{"x": 30}),
		}
		return ids(orderBatch(rs, tickets))
	}
	// minValue: c→10, d→30, b→90
	assert.Equal(t, []string{"a", "c", "d", "b"}, mk("minValue"))
	// maxValue: d→30, c→50, b→90
	assert.Equal(t, []string{"a", "d", "c", "b"}, mk("maxValue"))
}

// Purpose: Verify absoluteSort reduces a multi-player party to a scalar via
// partyAggregation before sorting.
// Method:  Anchor "a", a party [10,90], and solo "s"=40; sort ascending by skill.
// Expect:  avg → party scores 50 (after s); min → party scores 10 (before s).
func TestOrderBatch_AbsoluteSort_PartyAggregation(t *testing.T) {
	mk := func(agg string) []string {
		rs := newRS(t, `{
		  "name": "x",
		  "ruleLanguageVersion": "1.0",
		  "playerAttributes": [{"name":"skill","type":"number"}],
		  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}],
		  "rules": [{"name": "S", "type": "absoluteSort",
		    "sortDirection": "ascending", "sortAttribute": "skill", "partyAggregation": "`+agg+`"}]
		}`)
		tickets := []core.Ticket{solo("a", 0), party("p", 10, 90), solo("s", 40)}
		return ids(orderBatch(rs, tickets))
	}
	assert.Equal(t, []string{"a", "s", "p"}, mk("avg")) // party avg 50 > 40
	assert.Equal(t, []string{"a", "p", "s"}, mk("min")) // party min 10 < 40
}

// Purpose: Verify distanceSort reduces a string_number_map attribute via mapKey
// before computing each ticket's distance from the anchor.
// Method:  Anchor "a" ping reduces to 50; sort ascending by distance, mapKey
//
//	minValue then maxValue.
//
// Expect:  ordering by |reduced - 50| under each mapKey.
func TestOrderBatch_DistanceSort_MapKey(t *testing.T) {
	mk := func(mapKey string) []string {
		rs := newRS(t, `{
		  "name": "x",
		  "ruleLanguageVersion": "1.0",
		  "playerAttributes": [{"name":"ping","type":"string_number_map"}],
		  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}],
		  "rules": [{"name": "S", "type": "distanceSort",
		    "sortDirection": "ascending", "sortAttribute": "ping", "mapKey": "`+mapKey+`"}]
		}`)
		tickets := []core.Ticket{
			soloMap("a", map[string]float64{"x": 50}),
			soloMap("b", map[string]float64{"x": 90}),
			soloMap("c", map[string]float64{"x": 10, "y": 80}),
			soloMap("d", map[string]float64{"x": 55}),
		}
		return ids(orderBatch(rs, tickets))
	}
	// minValue: a→50. dist b|90-50|=40, c|10-50|=40, d|55-50|=5 → d, then b,c (stable)
	assert.Equal(t, []string{"a", "d", "b", "c"}, mk("minValue"))
	// maxValue: a→50. dist b|90-50|=40, c|80-50|=30, d|55-50|=5 → d, c, b
	assert.Equal(t, []string{"a", "d", "c", "b"}, mk("maxValue"))
}

// Purpose: Verify distanceSort reduces a multi-player party to a scalar via
// partyAggregation before computing its distance from the anchor.
// Method:  Anchor "a"=0, a party [10,90], and solo "s"=40; sort ascending by
//
//	distance from the anchor.
//
// Expect:  avg → party distance 50 (after s=40); min → party distance 10 (before s).
func TestOrderBatch_DistanceSort_PartyAggregation(t *testing.T) {
	mk := func(agg string) []string {
		rs := newRS(t, `{
		  "name": "x",
		  "ruleLanguageVersion": "1.0",
		  "playerAttributes": [{"name":"skill","type":"number"}],
		  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}],
		  "rules": [{"name": "S", "type": "distanceSort",
		    "sortDirection": "ascending", "sortAttribute": "skill", "partyAggregation": "`+agg+`"}]
		}`)
		tickets := []core.Ticket{solo("a", 0), party("p", 10, 90), solo("s", 40)}
		return ids(orderBatch(rs, tickets))
	}
	assert.Equal(t, []string{"a", "s", "p"}, mk("avg")) // party avg 50, dist 50 > 40
	assert.Equal(t, []string{"a", "p", "s"}, mk("min")) // party min 10, dist 10 < 40
}

// Purpose: Verify batchingPreference "sorted" applies sortByAttributes in priority
// order, using a later attribute only to break ties.
// Method:  Sort by ["tier","skill"]; tickets share tiers but differ in skill.
// Expect:  Grouped by tier ascending, then skill ascending within a tier.
func TestOrderBatch_SortByAttributes_Tiebreak(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"tier","type":"string"},{"name":"skill","type":"number"}],
	  "algorithm": {"strategy": "exhaustiveSearch", "batchingPreference": "sorted",
	    "sortByAttributes": ["tier", "skill"]},
	  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}]
	}`)
	mk := func(id, tier string, skill float64) core.Ticket {
		return core.Ticket{ID: id, Players: []core.Player{{ID: id, Attributes: core.Attributes{
			"tier": {Kind: core.AttrString, S: tier}, "skill": num(skill)}}}}
	}
	tickets := []core.Ticket{
		mk("a", "gold", 90), mk("b", "bronze", 50), mk("c", "gold", 10), mk("d", "bronze", 20),
	}
	out := orderBatch(rs, tickets)
	// bronze before gold; within each, ascending skill.
	assert.Equal(t, []string{"d", "b", "c", "a"}, ids(out))
}

// Purpose: Verify non-sorting batchingPreferences keep the incoming queue order
// (flexi treats "random"/"largestPopulation"/"fastestRegion" as deterministic).
// Method:  exhaustiveSearch + "random" with no sort rules; check order is preserved.
// Expect:  orderBatch returns the tickets in their original order.
func TestOrderBatch_RandomKeepsQueueOrder(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "algorithm": {"strategy": "exhaustiveSearch", "batchingPreference": "random"},
	  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}]
	}`)
	tickets := []core.Ticket{solo("a", 50), solo("b", 90), solo("c", 10), solo("d", 30)}
	out := orderBatch(rs, tickets)
	assert.Equal(t, []string{"a", "b", "c", "d"}, ids(out))
}

// seated returns a player already sitting on team in an in-progress match, as a
// backfill ticket reports them.
func seated(id, team string, skill float64) core.Player {
	return core.Player{ID: id, Team: team, Attributes: core.Attributes{"skill": num(skill)}}
}

// backfill returns a backfill ticket carrying the given in-progress roster.
func backfill(id string, players ...core.Player) core.Ticket {
	return core.Ticket{ID: id, Backfill: true, Players: players}
}

// Purpose: Verify a backfill ticket's roster is seated on the teams it already
// occupies and new tickets fill only the seats that remain.
// Method:  Two teams of 2 with a backfill roster of red+red+blue, plus two solo
// tickets — one more than the single free seat.
// Expect:  One match holding the whole roster, blue completed by the older solo
// ticket, and the other solo ticket left in the queue.
func TestBuild_BackfillSeatsRosterAndFillsRemainingSeats(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ]
	}`)
	tickets := []core.Ticket{
		backfill("bf", seated("r1", "red", 10), seated("r2", "red", 11), seated("b1", "blue", 12)),
		solo("a", 13),
		solo("b", 14),
	}
	out, remaining, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.Equal(t, []string{"a", "bf"}, out[0].TicketIDs)
	assert.Equal(t, []string{"r1", "r2"}, playerIDs(out[0].Teams["red"]))
	assert.Equal(t, []string{"b1", "a"}, playerIDs(out[0].Teams["blue"]))
	require.Len(t, remaining, 1)
	assert.Equal(t, "b", remaining[0].ID)
}

// Purpose: Verify rules are evaluated over the combined roster — the players
// already in the match plus the candidates joining them — which is the point of
// sending the current players along with a backfill request.
// Method:  batchDistance(skill, maxDistance 5) over a roster of skills 10 and 11,
// offering a distant solo ticket (100) and a close one (12).
// Expect:  The close ticket joins; the distant one is rejected and stays queued.
func TestBuild_BackfillEvaluatesRulesOverCombinedRoster(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "all", "minPlayers": 3, "maxPlayers": 3}],
	  "rules": [{"name": "BD", "type": "batchDistance",
	    "batchAttribute": "skill", "maxDistance": 5}]
	}`)
	tickets := []core.Ticket{
		backfill("bf", seated("p1", "all", 10), seated("p2", "all", 11)),
		solo("far", 100),
		solo("near", 12),
	}
	out, remaining, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.Equal(t, []string{"bf", "near"}, out[0].TicketIDs)
	require.Len(t, remaining, 1)
	assert.Equal(t, "far", remaining[0].ID)
}

// Purpose: Verify a backfill roster that no longer satisfies the rule set on its
// own admits nobody. The seated players are never judged in isolation — the match
// they came from may have been formed under expanded values — but the final
// evaluation still covers them, so no new player can be added to a match the
// current rules reject.
// Method:  batchDistance(skill, maxDistance 5) over a roster whose own skills are
// 10 and 100, plus a solo ticket that would fit either of them.
// Expect:  No match; every ticket stays queued.
func TestBuild_BackfillRosterViolatingRulesAdmitsNobody(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "all", "minPlayers": 3, "maxPlayers": 3}],
	  "rules": [{"name": "BD", "type": "batchDistance",
	    "batchAttribute": "skill", "maxDistance": 5}]
	}`)
	tickets := []core.Ticket{
		backfill("bf", seated("p1", "all", 10), seated("p2", "all", 100)),
		solo("a", 11),
	}
	out, remaining, _ := Build(rs, evals(t, rs), tickets)
	assert.Empty(t, out)
	assert.Len(t, remaining, 2)
}

// Purpose: Verify a backfill ticket never forms a match on its own: a backfill
// that admits no new player has achieved nothing and must keep waiting.
// Method:  A single-team rule set whose minPlayers is already met by the roster,
// with no other ticket queued.
// Expect:  No match; the backfill ticket stays queued.
func TestBuild_BackfillAloneIsNotAMatch(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 4}]
	}`)
	tickets := []core.Ticket{backfill("bf", seated("p1", "all", 10), seated("p2", "all", 11))}
	out, remaining, _ := Build(rs, evals(t, rs), tickets)
	assert.Empty(t, out)
	assert.Len(t, remaining, 1)
}

// Purpose: Verify two backfill tickets are never matched together, mirroring
// FlexMatch's limit of one backfill ticket per match.
// Method:  A team of exactly 4 offered two backfill rosters (2 and 1 players) and
// one solo ticket — a combination that fills the team exactly, but only
// if both rosters are seated at once.
// Expect:  No match, even under backfillPriority "high"; all three stay queued.
func TestBuild_TwoBackfillTicketsNeverShareAMatch(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "algorithm": {"backfillPriority": "high"},
	  "teams": [{"name": "all", "minPlayers": 4, "maxPlayers": 4}]
	}`)
	tickets := []core.Ticket{
		backfill("bf1", seated("p1", "all", 10), seated("p2", "all", 11)),
		backfill("bf2", seated("p3", "all", 12)),
		solo("a", 13),
	}
	out, remaining, _ := Build(rs, evals(t, rs), tickets)
	assert.Empty(t, out)
	assert.Len(t, remaining, 3)
}

// Purpose: Verify a roster is seated on the expanded slot its Team names, so a
// team declared with quantity > 1 backfills the instance the players are on.
// Method:  team{quantity: 2, 2 players each} with a roster of one player on
// "team_1", plus three solo tickets.
// Expect:  "team_1" is completed by one solo ticket and "team_2" filled by the
// other two.
func TestBuild_BackfillSeatsExpandedTeamInstance(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "team", "minPlayers": 2, "maxPlayers": 2, "quantity": 2}]
	}`)
	tickets := []core.Ticket{
		backfill("bf", seated("p1", "team_1", 10)),
		solo("a", 11), solo("b", 12), solo("c", 13),
	}
	out, _, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.Equal(t, []string{"a", "b", "bf", "c"}, out[0].TicketIDs)
	assert.Contains(t, playerIDs(out[0].Teams["team_1"]), "p1")
	assert.Len(t, out[0].Teams["team_1"], 2)
	assert.Len(t, out[0].Teams["team_2"], 2)
}

// Purpose: Verify a roster naming a team the rule set does not declare forms no
// match, rather than being seated somewhere arbitrary. The public package
// rejects such a ticket at enqueue time; this pins the algorithm's own guard.
// Method:  A roster on team "green" against a rule set declaring only "all".
// Expect:  No match.
func TestBuild_BackfillWithUnknownTeamFormsNoMatch(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}]
	}`)
	tickets := []core.Ticket{backfill("bf", seated("p1", "green", 10)), solo("a", 11)}
	out, _, _ := Build(rs, evals(t, rs), tickets)
	assert.Empty(t, out)
}

// Purpose: Verify algorithm.backfillPriority decides when a search reaches for a
// backfill ticket: "high" tries them before forming a new match, "low" only once
// no new match can be formed, and the default treats them as ordinary tickets
// that take part when they are the oldest in the pool.
// Method:  Two pools that each admit a match with or without the backfill ticket:
// one where the backfill ticket is oldest, one where two solo tickets
// precede it. Build each under all three priorities.
// Expect:  "high" always consumes the backfill ticket; "low" never does while a
// new match is available; the default consumes it only when it is oldest.
func TestBuild_BackfillPriorityOrdersTheSearch(t *testing.T) {
	rsFor := func(priority string) *ruleset.RuleSet {
		algorithm := ""
		if priority != "" {
			algorithm = `"algorithm":{"backfillPriority":"` + priority + `"},`
		}
		return newRS(t, `{"name":"x","ruleLanguageVersion":"1.0",`+algorithm+
			`"teams":[{"name":"all","minPlayers":3,"maxPlayers":3}]}`)
	}
	bf := backfill("bf", seated("p1", "all", 10))
	cases := []struct {
		name     string
		tickets  []core.Ticket
		priority string
		want     bool // is the backfill ticket consumed?
	}{
		{"oldest/high", []core.Ticket{bf, solo("a", 1), solo("b", 2), solo("c", 3)}, "high", true},
		{"oldest/normal", []core.Ticket{bf, solo("a", 1), solo("b", 2), solo("c", 3)}, "normal", true},
		{"oldest/default", []core.Ticket{bf, solo("a", 1), solo("b", 2), solo("c", 3)}, "", true},
		{"oldest/low", []core.Ticket{bf, solo("a", 1), solo("b", 2), solo("c", 3)}, "low", false},
		{"newer/high", []core.Ticket{solo("a", 1), solo("b", 2), bf, solo("c", 3)}, "high", true},
		{"newer/normal", []core.Ticket{solo("a", 1), solo("b", 2), bf, solo("c", 3)}, "normal", false},
		{"newer/default", []core.Ticket{solo("a", 1), solo("b", 2), bf, solo("c", 3)}, "", false},
		{"newer/low", []core.Ticket{solo("a", 1), solo("b", 2), bf, solo("c", 3)}, "low", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs := rsFor(c.priority)
			out, _, _ := Build(rs, evals(t, rs), c.tickets)
			require.Len(t, out, 1)
			if c.want {
				assert.Contains(t, out[0].TicketIDs, "bf")
			} else {
				assert.NotContains(t, out[0].TicketIDs, "bf")
			}
		})
	}
}

// Purpose: Verify the balanced strategy ignores backfillPriority. FlexMatch uses
// the property only when pre-sorting an exhaustive search, so a rule set that
// declares one alongside balanced keeps it without gaining its effect.
// Method:  The pool that "high" would otherwise consume the backfill ticket from
// — two solo tickets ahead of it — under balanced with backfillPriority "high".
// Expect:  The solo tickets form a new match and the backfill ticket keeps
// waiting, as it would under the default priority.
func TestBuild_BalancedStrategyIgnoresBackfillPriority(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "algorithm": {"strategy": "balanced", "balancedAttribute": "skill", "backfillPriority": "high"},
	  "teams": [{"name": "all", "minPlayers": 3, "maxPlayers": 3}]
	}`)
	tickets := []core.Ticket{
		solo("a", 1), solo("b", 2),
		backfill("bf", seated("p1", "all", 10)),
		solo("c", 3),
	}
	out, remaining, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.NotContains(t, out[0].TicketIDs, "bf")
	require.Len(t, remaining, 1)
	assert.Equal(t, "bf", remaining[0].ID)
}

// Purpose: Verify a backfill roster is exposed to rules as one party per player.
// A FlexMatch backfill request carries per-player data only, so the parties of
// the match in progress cannot be recovered and each seated player counts alone.
// Method:  A rule counting a team's players — which partyAggregation collapses to
// a count of its parties — required to equal 3, against a roster of two
// players seated on the same team plus one solo ticket.
// Expect:  A match forms, so the three players stood as three parties; had the
// roster counted as one party the team would have held only two.
func TestBuild_BackfillRosterCountsAsSoloParties(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "all", "minPlayers": 3, "maxPlayers": 3}],
	  "rules": [{"name": "Parties", "type": "comparison",
	    "measurements": ["count(teams[all].players)"],
	    "referenceValue": 3, "operation": "=", "partyAggregation": "avg"}]
	}`)
	tickets := []core.Ticket{
		backfill("bf", seated("p1", "all", 10), seated("p2", "all", 11)),
		solo("a", 12),
	}
	out, _, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.Equal(t, []string{"a", "bf"}, out[0].TicketIDs)
}

// Purpose: Verify CheckBackfillRoster accepts the team names a caller can
// legitimately report and rejects the rest, so a bad roster is caught when the
// ticket is submitted rather than silently failing to match.
// Method:  Check rosters against a rule set declaring "solo" (quantity 1, up to 2
// players) and "duo" (quantity 2), covering each resolution outcome.
// Expect:  Declared and expanded names resolve; an ambiguous base name, an
// unknown name, a missing name, and an over-full team are rejected.
func TestCheckBackfillRoster(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [
	    {"name": "solo", "minPlayers": 1, "maxPlayers": 2},
	    {"name": "duo",  "minPlayers": 1, "maxPlayers": 2, "quantity": 2}
	  ]
	}`)
	cases := []struct {
		name    string
		players []core.Player
		wantErr string
	}{
		{"declared name", []core.Player{seated("p1", "solo", 10)}, ""},
		{"expanded name", []core.Player{seated("p1", "duo_2", 10)}, ""},
		{"team at maxPlayers", []core.Player{seated("p1", "solo", 10), seated("p2", "solo", 11)}, ""},
		{"ambiguous base name", []core.Player{seated("p1", "duo", 10)}, "quantity > 1"},
		{"unknown team", []core.Player{seated("p1", "green", 10)}, "unknown team"},
		{"missing team", []core.Player{{ID: "p1"}}, "team is required"},
		{"team over maxPlayers", []core.Player{
			seated("p1", "solo", 10), seated("p2", "solo", 11), seated("p3", "solo", 12),
		}, "maxPlayers"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckBackfillRoster(rs, c.players)
			if c.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
		})
	}
}

// Purpose: Verify the balanced strategy forms a large match (>40 players) and keeps
// per-team sums of the balanced attribute close.
// Method:  Two teams of 25; 50 solo tickets with skills 0..49; strategy=balanced.
// Expect:  One match with 25 players per team and team skill sums within a small delta.
func TestBuild_BalancedLargeMatch(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "algorithm": {"strategy": "balanced", "balancedAttribute": "skill"},
	  "teams": [
	    {"name": "red",  "minPlayers": 25, "maxPlayers": 25},
	    {"name": "blue", "minPlayers": 25, "maxPlayers": 25}
	  ]
	}`)
	tickets := make([]core.Ticket, 0, 50)
	for i := 0; i < 50; i++ {
		tickets = append(tickets, solo(fmt.Sprintf("t%02d", i), float64(i)))
	}
	out, _, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.Len(t, out[0].Teams["red"], 25)
	assert.Len(t, out[0].Teams["blue"], 25)
	red := sumSkill(out[0].Teams["red"])
	blue := sumSkill(out[0].Teams["blue"])
	assert.InDelta(t, red, blue, 25, "red=%v blue=%v", red, blue)
}

// Purpose: Verify sharedRegion picks a region every player can reach, and picks it
// deterministically when more than one region qualifies.
// Method:  Two players who both report latencies for "eu" and "us" plus one region
//          only one of them reports; call sharedRegion repeatedly.
// Expect:  Always "eu" — the lexicographically smallest fully-covering region — never
//          the partially-covering one, despite randomized map iteration order.
func TestSharedRegion_DeterministicAcrossFullyCoveringRegions(t *testing.T) {
	slots := []teamSlot{{Name: "all", Players: []core.Player{
		{ID: "p1", Latencies: map[string]int{"us": 10, "eu": 20, "ap": 30}},
		{ID: "p2", Latencies: map[string]int{"us": 40, "eu": 50}},
	}}}
	for range 50 {
		assert.Equal(t, "eu", sharedRegion(slots))
	}
}

// Purpose: Verify sharedRegion reports no region when no single region covers every
// player.
// Method:  Two players with disjoint latency maps.
// Expect:  The empty string.
func TestSharedRegion_NoRegionCoversEveryone(t *testing.T) {
	slots := []teamSlot{{Name: "all", Players: []core.Player{
		{ID: "p1", Latencies: map[string]int{"us": 10}},
		{ID: "p2", Latencies: map[string]int{"eu": 20}},
	}}}
	assert.Empty(t, sharedRegion(slots))
}

// Purpose: Verify the one-backfill-per-match limit constrains each match, not the
// whole search: several sessions waiting for players are each filled in turn.
// Method:  Two backfill rosters and two solo tickets against a team of 2, under
// backfillPriority "high".
// Expect:  Two matches, each pairing one backfill ticket with one solo ticket.
func TestBuild_SeveralBackfillTicketsFillSeparateMatches(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "algorithm": {"backfillPriority": "high"},
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}]
	}`)
	tickets := []core.Ticket{
		backfill("bf1", seated("p1", "all", 10)),
		backfill("bf2", seated("p2", "all", 11)),
		solo("a", 12), solo("b", 13),
	}
	out, remaining, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 2)
	assert.Equal(t, []string{"a", "bf1"}, out[0].TicketIDs)
	assert.Equal(t, []string{"b", "bf2"}, out[1].TicketIDs)
	assert.Empty(t, remaining)
}

// Purpose: Verify a search that tries several backfill tickets before settling
// reports the rule evaluations of every attempt it made. The failed attempts are
// part of what the queue spent on this match, so dropping them would understate
// the failure counts a caller reads back as ruleEvaluationMetrics.
// Method:  Under backfillPriority "high", offer a backfill roster too far from
// the pool to match (skill 100 against maxDistance 5) alongside two solo tickets
// that do match, so the search fails an attempt before succeeding.
// Expect:  One match, whose metrics carry both the failures from the abandoned
// backfill attempt and the passes from the successful one.
func TestBuild_MetricsCoverEveryBackfillAttemptOfASearch(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "algorithm": {"backfillPriority": "high"},
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "BD", "type": "batchDistance",
	    "batchAttribute": "skill", "maxDistance": 5}]
	}`)
	tickets := []core.Ticket{
		backfill("bf", seated("p1", "all", 100)),
		solo("a", 10), solo("b", 11),
	}
	out, _, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.Equal(t, []string{"a", "b"}, out[0].TicketIDs)
	require.Len(t, out[0].RuleEvaluationMetrics, 1)
	m := out[0].RuleEvaluationMetrics[0]
	assert.Equal(t, 2, m.FailedCount, "both placements attempted against the distant roster failed")
	assert.Equal(t, 3, m.PassedCount, "two placements plus the final check of the match that formed")
}

// Purpose: Verify the players a backfill ticket seats count towards the region
// the match reports. The session is already running somewhere, so a region only
// the joining players can reach is not a region the match can be hosted in.
// Method:  A roster reporting latency for "us" alone, joined by a ticket
// reporting both "eu" and "us".
// Expect:  Region is "us" — had the seated player been left out, "eu" would have
// qualified too and won the lexicographic tie-break.
func TestBuild_BackfillRosterCountsTowardsSharedRegion(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{"name": "Ping", "type": "latency", "maxLatency": 100}]
	}`)
	tickets := []core.Ticket{
		backfill("bf", core.Player{ID: "p1", Team: "all", Latencies: map[string]int{"us": 10}}),
		{ID: "a", Players: []core.Player{{ID: "n1", Latencies: map[string]int{"eu": 30, "us": 20}}}},
	}
	out, _, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.Equal(t, "us", out[0].Region)
}

// Purpose: Verify a multi-player ticket joining a backfilled session is placed
// whole, against the seats the roster leaves rather than an empty team. A party
// that fits the match but not the remaining space must be passed over, not split.
// Method:  A roster holding one of red's two seats and none of blue's, offered a
// two-player party and then a solo ticket.
// Expect:  The party takes blue, which has room for it; the solo ticket completes
// red beside the seated player.
func TestBuild_BackfillAdmitsPartiesWhole(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "ruleLanguageVersion": "1.0",
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ]
	}`)
	party := core.Ticket{ID: "party", Players: []core.Player{
		{ID: "d1", Attributes: core.Attributes{"skill": num(20)}},
		{ID: "d2", Attributes: core.Attributes{"skill": num(21)}},
	}}
	tickets := []core.Ticket{
		backfill("bf", seated("r1", "red", 10)),
		party,
		solo("a", 22),
	}
	out, remaining, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.Equal(t, []string{"a", "bf", "party"}, out[0].TicketIDs)
	assert.Equal(t, []string{"d1", "d2"}, playerIDs(out[0].Teams["blue"]), "the party stayed together")
	assert.Equal(t, []string{"r1", "a"}, playerIDs(out[0].Teams["red"]))
	assert.Empty(t, remaining)
}
