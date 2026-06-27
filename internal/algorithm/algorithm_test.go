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

// Purpose: Document that backfillPriority is validated but does not alter match
// formation in flexi (backfill ticket handling is out of scope).
// Method:  Form a match with the default priority and with backfillPriority="high".
// Expect:  Both produce a match over the same tickets.
func TestBuild_BackfillPriorityIsNoop(t *testing.T) {
	tickets := []core.Ticket{solo("a", 1), solo("b", 2)}
	rsDefault := newRS(t, `{"name":"x","ruleLanguageVersion":"1.0","teams":[{"name":"all","minPlayers":2,"maxPlayers":2}]}`)
	rsHigh := newRS(t, `{"name":"x","ruleLanguageVersion":"1.0","algorithm":{"backfillPriority":"high"},
	  "teams":[{"name":"all","minPlayers":2,"maxPlayers":2}]}`)
	outD, _, _ := Build(rsDefault, evals(t, rsDefault), tickets)
	outH, _, _ := Build(rsHigh, evals(t, rsHigh), tickets)
	require.Len(t, outD, 1)
	require.Len(t, outH, 1)
	assert.ElementsMatch(t, outD[0].TicketIDs, outH[0].TicketIDs)
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
