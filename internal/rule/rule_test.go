package rule

import (
	"encoding/json"
	"testing"

	"github.com/moepig/flexi/internal/core"
	"github.com/moepig/flexi/internal/ruleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func num(v float64) core.Attribute { return core.Attribute{Kind: core.AttrNumber, N: v} }
func sl(v ...string) core.Attribute {
	return core.Attribute{Kind: core.AttrStringList, SL: append([]string(nil), v...)}
}

func numPlayer(id string, skill float64) core.Player {
	return core.Player{ID: id, Attributes: core.Attributes{"skill": num(skill)}}
}

func numCand(skills ...float64) *Candidate {
	pl := make([]core.Player, len(skills))
	for i, s := range skills {
		pl[i] = numPlayer("p", s)
	}
	return &Candidate{Players: pl, Teams: map[string][]core.Player{"red": pl}}
}

func ptrF(v float64) *float64 { return &v }
func ptrI(v int) *int         { return &v }

// Purpose: Verify that the comparison rule (avg(players.attributes[skill]) <= 50) correctly passes and rejects candidates.
// Method:  Evaluate the same evaluator against a player set with avg=30 and another with avg=75.
// Expect:  avg=30 → true; avg=75 → false.
func TestComparison(t *testing.T) {
	r := &ruleset.Rule{
		Name: "x", Type: ruleset.RuleComparison,
		Measurements:   []string{"avg(players.attributes[skill])"},
		ReferenceValue: json.RawMessage(`50`),
		Operation:      "<=",
	}
	ev, err := Build(r, nil)
	require.NoError(t, err)
	ok, err := ev.Evaluate(numCand(10, 30, 50))
	require.NoError(t, err)
	assert.True(t, ok)
	ok, _ = ev.Evaluate(numCand(70, 80))
	assert.False(t, ok)
}

// Purpose: Verify that the distance rule compares two teams' average skill against maxDistance.
// Method:  Evaluate the same evaluator with a close-skill pair (avg diff ≈5) and a wide-skill pair (avg diff ≈70).
// Expect:  Close pair → true; wide pair → false.
func TestDistance(t *testing.T) {
	r := &ruleset.Rule{
		Name: "x", Type: ruleset.RuleDistance,
		Measurements:   []string{"avg(teams[red].players.attributes[skill])"},
		ReferenceValue: json.RawMessage(`"avg(teams[blue].players.attributes[skill])"`),
		MaxDistance:    ptrF(10),
	}
	ev, err := Build(r, nil)
	require.NoError(t, err)
	red := []core.Player{numPlayer("r1", 10), numPlayer("r2", 20)}
	blue := []core.Player{numPlayer("b1", 12), numPlayer("b2", 18)}
	c := &Candidate{Players: append(red, blue...), Teams: map[string][]core.Player{"red": red, "blue": blue}}
	ok, err := ev.Evaluate(c)
	require.NoError(t, err)
	assert.True(t, ok)

	bad := []core.Player{numPlayer("b1", 80), numPlayer("b2", 90)}
	c2 := &Candidate{Players: append(red, bad...), Teams: map[string][]core.Player{"red": red, "blue": bad}}
	ok, _ = ev.Evaluate(c2)
	assert.False(t, ok)
}

// Purpose: Verify that the batchDistance rule limits the max skill spread within a candidate to maxDistance.
// Method:  Evaluate against a spread-15 set (10,20,25) and an over-limit set (10,30,50).
// Expect:  Spread-15 → true; over-limit → false.
func TestBatchDistance(t *testing.T) {
	r := &ruleset.Rule{
		Name: "x", Type: ruleset.RuleBatchDistance,
		BatchAttribute: "skill", MaxDistance: ptrF(15),
	}
	ev, err := Build(r, nil)
	require.NoError(t, err)
	ok, _ := ev.Evaluate(numCand(10, 20, 25))
	assert.True(t, ok)
	ok, _ = ev.Evaluate(numCand(10, 30, 50))
	assert.False(t, ok)
}

// Purpose: Verify that batchDistance without maxDistance always passes (string batching mode).
// Method:  Evaluate with no distance constraint.
// Expect:  true regardless of values.
func TestBatchDistance_NoConstraint(t *testing.T) {
	r := &ruleset.Rule{
		Name: "x", Type: ruleset.RuleBatchDistance,
		BatchAttribute: "mode",
	}
	ev, err := Build(r, nil)
	require.NoError(t, err)
	ok, _ := ev.Evaluate(numCand(10, 20, 25))
	assert.True(t, ok)
}

// Purpose: Verify partyAggregation=avg collapses a multi-player party to its average before comparing.
// Method:  Two parties: [10,20] (avg=15) and [30,40] (avg=35); spread=20; maxDistance=25.
// Expect:  true (20 <= 25). Without aggregation, spread would be 30 (40-10).
func TestBatchDistance_PartyAggregation(t *testing.T) {
	r := &ruleset.Rule{
		Name: "x", Type: ruleset.RuleBatchDistance,
		BatchAttribute: "skill", MaxDistance: ptrF(25),
		PartyAggregation: "avg",
	}
	ev, err := Build(r, nil)
	require.NoError(t, err)

	party1 := []core.Player{numPlayer("a", 10), numPlayer("b", 20)}
	party2 := []core.Player{numPlayer("c", 30), numPlayer("d", 40)}
	all := append(party1, party2...)
	c := &Candidate{
		Players: all,
		Teams:   map[string][]core.Player{"red": all},
		Parties: [][]core.Player{party1, party2},
	}
	ok, err := ev.Evaluate(c)
	require.NoError(t, err)
	assert.True(t, ok, "avg spread 20 should be within maxDistance 25")

	// With max aggregation per party: [10,20]→20, [30,40]→40, spread=20 → exceeds maxDistance=15
	r2 := &ruleset.Rule{
		Name: "y", Type: ruleset.RuleBatchDistance,
		BatchAttribute: "skill", MaxDistance: ptrF(15),
		PartyAggregation: "max",
	}
	ev2, err := Build(r2, nil)
	require.NoError(t, err)
	ok, _ = ev2.Evaluate(c)
	assert.False(t, ok, "max spread 20 should exceed maxDistance 15")
}

// Purpose: Verify that the collection/contains operation detects a target value in flatten(players.attributes[modes]).
// Method:  Evaluate against one player with modes=["TDM","CTF"] using reference value "TDM".
// Expect:  true is returned.
func TestCollection_Contains(t *testing.T) {
	r := &ruleset.Rule{
		Name: "x", Type: ruleset.RuleCollection,
		Measurements:   []string{"flatten(players.attributes[modes])"},
		Operation:      "contains",
		ReferenceValue: json.RawMessage(`"TDM"`),
	}
	ev, err := Build(r, nil)
	require.NoError(t, err)
	pl := []core.Player{
		{Attributes: core.Attributes{"modes": sl("TDM", "CTF")}},
	}
	ok, _ := ev.Evaluate(&Candidate{Players: pl})
	assert.True(t, ok)
}

// Purpose: Verify that reference_intersection_count evaluates the overlap between set_intersection and a reference set against minCount.
// Method:  Two players share "TDM"; reference=["TDM","CTF"], minCount=1. Evaluate the rule.
// Expect:  Intersection count 1 >= minCount, so true is returned.
func TestCollection_RefIntersection(t *testing.T) {
	r := &ruleset.Rule{
		Name: "x", Type: ruleset.RuleCollection,
		Measurements:   []string{"set_intersection(players.attributes[modes])"},
		Operation:      "reference_intersection_count",
		ReferenceValue: json.RawMessage(`["TDM","CTF"]`),
		MinCount:       ptrI(1),
	}
	ev, err := Build(r, nil)
	require.NoError(t, err)
	pl := []core.Player{
		{Attributes: core.Attributes{"modes": sl("TDM", "FFA")}},
		{Attributes: core.Attributes{"modes": sl("TDM", "CTF")}},
	}
	ok, err := ev.Evaluate(&Candidate{Players: pl})
	require.NoError(t, err)
	assert.True(t, ok)
}

// Purpose: Verify that the latency rule passes when at least one region satisfies the threshold for all players.
// Method:  Evaluate with a candidate where us-east-1 is within limit for both players, then one where it is not.
// Expect:  Shared valid region → true; no valid shared region → false.
func TestLatency(t *testing.T) {
	r := &ruleset.Rule{Name: "x", Type: ruleset.RuleLatency, MaxLatency: ptrI(100)}
	ev, err := Build(r, nil)
	require.NoError(t, err)
	pl := []core.Player{
		{Latencies: map[string]int{"us-east-1": 50, "us-west-2": 200}},
		{Latencies: map[string]int{"us-east-1": 80, "us-west-2": 30}},
	}
	ok, _ := ev.Evaluate(&Candidate{Players: pl})
	assert.True(t, ok, "us-east-1 satisfies both")
	bad := []core.Player{
		{Latencies: map[string]int{"us-east-1": 50}},
		{Latencies: map[string]int{"us-east-1": 200}},
	}
	ok, _ = ev.Evaluate(&Candidate{Players: bad})
	assert.False(t, ok)
}

// Purpose: Verify that a compound(and) rule requires all child rules to pass.
// Method:  Build an AND of comparison(avg<=50) and batchDistance(maxDist=20); evaluate against a passing set and a failing set.
// Expect:  Both children satisfied → true; one child fails → false.
func TestCompound(t *testing.T) {
	r1 := &ruleset.Rule{Name: "a", Type: ruleset.RuleComparison,
		Measurements: []string{"avg(players.attributes[skill])"}, ReferenceValue: json.RawMessage(`50`), Operation: "<="}
	r2 := &ruleset.Rule{Name: "b", Type: ruleset.RuleComparison,
		Measurements: []string{"max(players.attributes[skill])"}, ReferenceValue: json.RawMessage(`80`), Operation: "<="}
	rc := &ruleset.Rule{Name: "c", Type: ruleset.RuleCompound,
		Statement: "and(a, b)"}

	others := map[string]Evaluator{}
	for _, r := range []*ruleset.Rule{r1, r2} {
		ev, err := Build(r, others)
		require.NoError(t, err)
		others[r.Name] = ev
	}
	cev, err := Build(rc, others)
	require.NoError(t, err)

	ok, _ := cev.Evaluate(numCand(10, 20, 30))
	assert.True(t, ok)
	ok, _ = cev.Evaluate(numCand(10, 50, 90))
	assert.False(t, ok)
}

// Purpose: Verify xor passes only when exactly one child rule is satisfied.
// Method:  xor(a, b) where a=avg<=50 and b=max>=80; evaluate three player sets.
// Expect:  exactly-one-true → true; both-true and both-false → false.
func TestCompound_Xor(t *testing.T) {
	a := &ruleset.Rule{Name: "a", Type: ruleset.RuleComparison,
		Measurements: []string{"avg(players.attributes[skill])"}, ReferenceValue: json.RawMessage(`50`), Operation: "<="}
	b := &ruleset.Rule{Name: "b", Type: ruleset.RuleComparison,
		Measurements: []string{"max(players.attributes[skill])"}, ReferenceValue: json.RawMessage(`80`), Operation: ">="}
	rc := &ruleset.Rule{Name: "c", Type: ruleset.RuleCompound, Statement: "xor(a, b)"}

	others := map[string]Evaluator{}
	for _, r := range []*ruleset.Rule{a, b} {
		ev, err := Build(r, others)
		require.NoError(t, err)
		others[r.Name] = ev
	}
	cev, err := Build(rc, others)
	require.NoError(t, err)

	// avg=20<=50 true, max=30>=80 false -> exactly one -> true
	ok, _ := cev.Evaluate(numCand(10, 20, 30))
	assert.True(t, ok)
	// avg=90<=50 false, max=90>=80 true -> exactly one -> true
	ok, _ = cev.Evaluate(numCand(90, 90))
	assert.True(t, ok)
	// avg=40<=50 true, max=90>=80 true -> both -> false
	ok, _ = cev.Evaluate(numCand(10, 70, 90, 10))
	assert.False(t, ok)
}

// Purpose: Verify the latency distanceReference/maxDistance constraint.
// Method:  maxLatency=200, distanceReference=min, maxDistance=30. Two players in
//
//	us-east-1 with latencies 50 and 70 (spread 20) then 50 and 90 (40).
//
// Expect:  spread within 30 → true; spread beyond 30 → false.
func TestLatency_DistanceReference(t *testing.T) {
	r := &ruleset.Rule{Name: "x", Type: ruleset.RuleLatency,
		MaxLatency: ptrI(200), DistanceReference: "min", MaxDistance: ptrF(30)}
	ev, err := Build(r, nil)
	require.NoError(t, err)

	good := []core.Player{
		{Latencies: map[string]int{"us-east-1": 50}},
		{Latencies: map[string]int{"us-east-1": 70}},
	}
	ok, err := ev.Evaluate(&Candidate{Players: good})
	require.NoError(t, err)
	assert.True(t, ok)

	bad := []core.Player{
		{Latencies: map[string]int{"us-east-1": 50}},
		{Latencies: map[string]int{"us-east-1": 90}},
	}
	ok, _ = ev.Evaluate(&Candidate{Players: bad})
	assert.False(t, ok)
}

// Purpose: Verify comparison partyAggregation collapses each party before comparing.
// Method:  comparison max(players.attributes[skill]) <= 30 with partyAggregation=avg.
//
//	One party [20,40] (avg=30) and one solo [25]; aggregated max is 30.
//
// Expect:  true with avg aggregation (party becomes 30); false without (raw 40 > 30).
func TestComparison_PartyAggregation(t *testing.T) {
	mk := func(agg string) Evaluator {
		r := &ruleset.Rule{Name: "x", Type: ruleset.RuleComparison,
			Measurements:   []string{"max(players.attributes[skill])"},
			ReferenceValue: json.RawMessage(`30`), Operation: "<=", PartyAggregation: agg}
		ev, err := Build(r, nil)
		require.NoError(t, err)
		return ev
	}
	party := []core.Player{numPlayer("a", 20), numPlayer("b", 40)}
	solo := []core.Player{numPlayer("c", 25)}
	all := append(append([]core.Player{}, party...), solo...)
	c := &Candidate{
		Players:     all,
		Teams:       map[string][]core.Player{"red": all},
		TeamOrder:   []string{"red"},
		TeamParties: map[string][][]core.Player{"red": {party, solo}},
	}
	ok, err := mk("avg").Evaluate(c)
	require.NoError(t, err)
	assert.True(t, ok, "avg-aggregated party max is 30")

	ok, _ = mk("").Evaluate(c)
	assert.False(t, ok, "without aggregation raw max is 40")
}

// Purpose: Verify collection partyAggregation=intersection narrows a party to its
// shared modes before the collection operation.
// Method:  contains "CTF" over a party whose members are [TDM,CTF] and [TDM]; with
//
//	intersection the party's modes become [TDM] only.
//
// Expect:  union (default) → contains CTF true; intersection → false.
func TestCollection_PartyIntersection(t *testing.T) {
	mk := func(agg string) Evaluator {
		r := &ruleset.Rule{Name: "x", Type: ruleset.RuleCollection,
			Measurements: []string{"flatten(players.attributes[modes])"},
			Operation:    "contains", ReferenceValue: json.RawMessage(`"CTF"`),
			PartyAggregation: agg}
		ev, err := Build(r, nil)
		require.NoError(t, err)
		return ev
	}
	party := []core.Player{
		{ID: "a", Attributes: core.Attributes{"modes": sl("TDM", "CTF")}},
		{ID: "b", Attributes: core.Attributes{"modes": sl("TDM")}},
	}
	c := &Candidate{
		Players:     party,
		Teams:       map[string][]core.Player{"red": party},
		TeamOrder:   []string{"red"},
		TeamParties: map[string][][]core.Player{"red": {party}},
	}
	ok, err := mk("union").Evaluate(c)
	require.NoError(t, err)
	assert.True(t, ok, "union keeps CTF")

	ok, _ = mk("intersection").Evaluate(c)
	assert.False(t, ok, "intersection drops CTF (only TDM shared)")
}
