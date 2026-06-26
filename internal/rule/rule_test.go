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
func str(v string) core.Attribute  { return core.Attribute{Kind: core.AttrString, S: v} }
func sl(v ...string) core.Attribute {
	return core.Attribute{Kind: core.AttrStringList, SL: append([]string(nil), v...)}
}

// strCand builds a single-team candidate whose players each carry a "character"
// string attribute.
func strCand(chars ...string) *Candidate {
	pl := make([]core.Player, len(chars))
	for i, ch := range chars {
		pl[i] = core.Player{ID: "p", Attributes: core.Attributes{"character": str(ch)}}
	}
	return &Candidate{Players: pl, Teams: map[string][]core.Player{"red": pl}, TeamOrder: []string{"red"}}
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

// Purpose: Verify batchDistance string-equivalency mode: a string batchAttribute
//          batches by value, distance = (distinct values) - 1, so maxDistance=0
//          requires every player to share one value.
// Method:  Build batchDistance(batchAttribute="character", maxDistance=0) and
//          evaluate against all-equal, two-distinct, and three-distinct sets.
// Expect:  all-equal → true; any value spread → false.
func TestBatchDistance_StringEquality(t *testing.T) {
	r := &ruleset.Rule{
		Name: "x", Type: ruleset.RuleBatchDistance,
		BatchAttribute: "character", MaxDistance: ptrF(0),
	}
	ev, err := Build(r, nil)
	require.NoError(t, err)

	ok, err := ev.Evaluate(strCand("warrior", "warrior", "warrior"))
	require.NoError(t, err)
	assert.True(t, ok, "identical strings have distance 0")

	ok, err = ev.Evaluate(strCand("warrior", "mage"))
	require.NoError(t, err)
	assert.False(t, ok, "two distinct strings have distance 1 > maxDistance 0")

	ok, err = ev.Evaluate(strCand("warrior", "mage", "archer"))
	require.NoError(t, err)
	assert.False(t, ok, "three distinct strings have distance 2 > maxDistance 0")
}

// Purpose: Verify string batchDistance honours a non-zero maxDistance, allowing
//          up to maxDistance+1 distinct values in the match.
// Method:  Build batchDistance(batchAttribute="character", maxDistance=1) and
//          evaluate two-distinct (distance 1) and three-distinct (distance 2) sets.
// Expect:  two distinct → true; three distinct → false.
func TestBatchDistance_StringMaxDistance(t *testing.T) {
	r := &ruleset.Rule{
		Name: "x", Type: ruleset.RuleBatchDistance,
		BatchAttribute: "character", MaxDistance: ptrF(1),
	}
	ev, err := Build(r, nil)
	require.NoError(t, err)

	ok, err := ev.Evaluate(strCand("warrior", "mage"))
	require.NoError(t, err)
	assert.True(t, ok, "two distinct strings (distance 1) within maxDistance 1")

	ok, err = ev.Evaluate(strCand("warrior", "mage", "archer"))
	require.NoError(t, err)
	assert.False(t, ok, "three distinct strings (distance 2) exceed maxDistance 1")
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

// Purpose: Verify comparison partyAggregation collapses each party before comparing,
// and that an omitted partyAggregation defaults to "avg" (FlexMatch spec).
// Method:  comparison max(players.attributes[skill]) <= 30 over one party [20,40]
//
//	and one solo [25]. avg collapses the party to 30; max collapses it to 40.
//
// Expect:  avg and "" (default) → true (party becomes 30); max → false (party is 40).
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
	assert.True(t, ok, "omitted partyAggregation defaults to avg → party max is 30")

	ok, _ = mk("max").Evaluate(c)
	assert.False(t, ok, "max-aggregated party is 40 > 30")
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

	// A-3: omitting partyAggregation defaults to "union".
	ok, _ = mk("").Evaluate(c)
	assert.True(t, ok, "default partyAggregation is union → keeps CTF")
}

// Purpose: Verify the comparison "compare across players" form (no referenceValue,
// only = or !=) per the FlexMatch spec.
// Method:  != requires every player's character to differ; = requires them equal.
// Expect:  != true for all-distinct, false on a duplicate; = true for all-equal.
func TestComparison_AcrossPlayers(t *testing.T) {
	mk := func(op string) Evaluator {
		r := &ruleset.Rule{Name: "diffChars", Type: ruleset.RuleComparison,
			Measurements: []string{"players.attributes[character]"}, Operation: op}
		ev, err := Build(r, nil)
		require.NoError(t, err)
		return ev
	}

	ne := mk("!=")
	ok, err := ne.Evaluate(strCand("mage", "rogue", "tank"))
	require.NoError(t, err)
	assert.True(t, ok, "all distinct characters satisfy !=")
	ok, _ = ne.Evaluate(strCand("mage", "rogue", "mage"))
	assert.False(t, ok, "duplicate character fails !=")

	eq := mk("=")
	ok, _ = eq.Evaluate(strCand("mage", "mage"))
	assert.True(t, ok, "all-equal satisfies =")
	ok, _ = eq.Evaluate(strCand("mage", "rogue"))
	assert.False(t, ok, "differing values fail =")

	// Numeric attribute across players (e.g. distinct spawn slots).
	mkNum := func(op string) Evaluator {
		r := &ruleset.Rule{Name: "slots", Type: ruleset.RuleComparison,
			Measurements: []string{"players.attributes[skill]"}, Operation: op}
		ev, err := Build(r, nil)
		require.NoError(t, err)
		return ev
	}
	ok, _ = mkNum("!=").Evaluate(numCand(10, 20, 30))
	assert.True(t, ok, "distinct numeric values satisfy !=")
	ok, _ = mkNum("!=").Evaluate(numCand(10, 20, 10))
	assert.False(t, ok, "duplicate numeric value fails !=")
	ok, _ = mkNum("=").Evaluate(numCand(30, 30))
	assert.True(t, ok, "equal numeric values satisfy =")
}

// Purpose: Verify that distance defaults partyAggregation to "avg" (FlexMatch spec).
// Method:  reference 10; maxDistance 5; one party [10,30] (avg=20, diff 10) collapsed.
// Expect:  default ("") and avg → false (diff 10 > 5); min → true (party becomes 10).
func TestDistance_PartyAggregationDefault(t *testing.T) {
	mk := func(agg string) Evaluator {
		r := &ruleset.Rule{Name: "x", Type: ruleset.RuleDistance,
			Measurements:   []string{"players.attributes[skill]"},
			ReferenceValue: json.RawMessage(`10`), MaxDistance: ptrF(5), PartyAggregation: agg}
		ev, err := Build(r, nil)
		require.NoError(t, err)
		return ev
	}
	party := []core.Player{numPlayer("a", 10), numPlayer("b", 30)}
	c := &Candidate{
		Players:     party,
		Teams:       map[string][]core.Player{"red": party},
		TeamOrder:   []string{"red"},
		TeamParties: map[string][][]core.Player{"red": {party}},
	}
	ok, err := mk("").Evaluate(c)
	require.NoError(t, err)
	assert.False(t, ok, "default avg → party 20, diff 10 > 5")

	ok, _ = mk("min").Evaluate(c)
	assert.True(t, ok, "min → party 10, diff 0 <= 5")
}

// Purpose: Verify latency aggregates a party's per-region latencies (default avg).
// Method:  maxLatency 100; one party [us-east-1: 60, 120] (avg 90) collapsed.
// Expect:  default avg → true (90 <= 100); max → false (120 > 100).
func TestLatency_PartyAggregation(t *testing.T) {
	mk := func(agg string) Evaluator {
		r := &ruleset.Rule{Name: "x", Type: ruleset.RuleLatency, MaxLatency: ptrI(100), PartyAggregation: agg}
		ev, err := Build(r, nil)
		require.NoError(t, err)
		return ev
	}
	party := []core.Player{
		{ID: "a", Latencies: map[string]int{"us-east-1": 60}},
		{ID: "b", Latencies: map[string]int{"us-east-1": 120}},
	}
	c := &Candidate{
		Players:     party,
		Teams:       map[string][]core.Player{"red": party},
		TeamOrder:   []string{"red"},
		TeamParties: map[string][][]core.Player{"red": {party}},
	}
	ok, err := mk("").Evaluate(c)
	require.NoError(t, err)
	assert.True(t, ok, "default avg → 90 <= 100")

	ok, _ = mk("max").Evaluate(c)
	assert.False(t, ok, "max → 120 > 100")
}

// Purpose: Verify the collection intersection operation, including minCount/maxCount
// bounds (A-4) and the unbounded overlap form.
// Method:  measurement set [TDM,CTF,FFA] against reference [TDM,CTF] (overlap 2).
// Expect:  unbounded → true; minCount 2 → true; minCount 3 → false; maxCount 1 → false.
func TestCollection_Intersection(t *testing.T) {
	mk := func(min, max *int) Evaluator {
		r := &ruleset.Rule{Name: "x", Type: ruleset.RuleCollection,
			Measurements:   []string{"flatten(players.attributes[modes])"},
			Operation:      "intersection",
			ReferenceValue: json.RawMessage(`["TDM","CTF"]`),
			MinCount:       min, MaxCount: max}
		ev, err := Build(r, nil)
		require.NoError(t, err)
		return ev
	}
	pl := []core.Player{{Attributes: core.Attributes{"modes": sl("TDM", "CTF", "FFA")}}}
	c := &Candidate{Players: pl}

	ok, err := mk(nil, nil).Evaluate(c)
	require.NoError(t, err)
	assert.True(t, ok, "overlap 2 > 0 satisfies unbounded intersection")

	ok, _ = mk(ptrI(2), nil).Evaluate(c)
	assert.True(t, ok, "overlap 2 >= minCount 2")

	ok, _ = mk(ptrI(3), nil).Evaluate(c)
	assert.False(t, ok, "overlap 2 < minCount 3")

	ok, _ = mk(nil, ptrI(1)).Evaluate(c)
	assert.False(t, ok, "overlap 2 > maxCount 1")
}

// Purpose: Verify the collection not_contains operation (a flexi extension; not part
// of the AWS FlexMatch operation set).
// Method:  not_contains "CTF" over a player whose modes lack / include CTF.
// Expect:  absent → true; present → false.
func TestCollection_NotContains(t *testing.T) {
	r := &ruleset.Rule{Name: "x", Type: ruleset.RuleCollection,
		Measurements:   []string{"flatten(players.attributes[modes])"},
		Operation:      "not_contains",
		ReferenceValue: json.RawMessage(`"CTF"`)}
	ev, err := Build(r, nil)
	require.NoError(t, err)

	absent := []core.Player{{Attributes: core.Attributes{"modes": sl("TDM", "FFA")}}}
	ok, err := ev.Evaluate(&Candidate{Players: absent})
	require.NoError(t, err)
	assert.True(t, ok, "CTF absent → not_contains true")

	present := []core.Player{{Attributes: core.Attributes{"modes": sl("TDM", "CTF")}}}
	ok, _ = ev.Evaluate(&Candidate{Players: present})
	assert.False(t, ok, "CTF present → not_contains false")
}

// Purpose: Verify every comparison operator against a numeric reference value.
// Method:  measurement players.attributes[skill]=50 compared to referenceValue 50.
// Expect:  =,<=,>= pass; !=,<,> fail.
func TestComparison_NumericOperators(t *testing.T) {
	cases := map[string]bool{"=": true, "!=": false, "<": false, "<=": true, ">": false, ">=": true}
	for op, want := range cases {
		t.Run(op, func(t *testing.T) {
			r := &ruleset.Rule{Name: "x", Type: ruleset.RuleComparison,
				Measurements:   []string{"players.attributes[skill]"},
				ReferenceValue: json.RawMessage(`50`), Operation: op}
			ev, err := Build(r, nil)
			require.NoError(t, err)
			ok, err := ev.Evaluate(numCand(50))
			require.NoError(t, err)
			assert.Equal(t, want, ok, "skill 50 %s 50", op)
		})
	}
}

// Purpose: Verify string comparison supports = and != against a reference string,
// and that an ordering operator on strings simply fails (no panic/error).
// Method:  character "mage" compared to "mage" / "rogue" with =, !=, and <.
// Expect:  = matches equal; != matches differing; < yields false.
func TestComparison_StringOperators(t *testing.T) {
	mk := func(ref, op string) Evaluator {
		r := &ruleset.Rule{Name: "x", Type: ruleset.RuleComparison,
			Measurements:   []string{"players.attributes[character]"},
			ReferenceValue: json.RawMessage(`"` + ref + `"`), Operation: op}
		ev, err := Build(r, nil)
		require.NoError(t, err)
		return ev
	}
	ok, err := mk("mage", "=").Evaluate(strCand("mage"))
	require.NoError(t, err)
	assert.True(t, ok)
	ok, _ = mk("rogue", "!=").Evaluate(strCand("mage"))
	assert.True(t, ok)
	ok, _ = mk("mage", "<").Evaluate(strCand("mage"))
	assert.False(t, ok, "ordering operators are unsupported on strings → false")
}

// Purpose: Verify the distance rule's minDistance lower bound and min+max band.
// Method:  reference 0, measurement = single skill value; minDistance 10, maxDistance 30.
// Expect:  skill 5 → false (too close); 20 → true; 40 → false (too far).
func TestDistance_MinDistance(t *testing.T) {
	r := &ruleset.Rule{Name: "x", Type: ruleset.RuleDistance,
		Measurements:   []string{"players.attributes[skill]"},
		ReferenceValue: json.RawMessage(`0`),
		MinDistance:    ptrF(10), MaxDistance: ptrF(30)}
	ev, err := Build(r, nil)
	require.NoError(t, err)
	ok, _ := ev.Evaluate(numCand(5))
	assert.False(t, ok, "diff 5 < minDistance 10")
	ok, _ = ev.Evaluate(numCand(20))
	assert.True(t, ok, "diff 20 within [10,30]")
	ok, _ = ev.Evaluate(numCand(40))
	assert.False(t, ok, "diff 40 > maxDistance 30")
}

// Purpose: Verify batchDistance minDistance (lower spread bound) and the single-party
// short-circuit (a batch of one party always passes).
// Method:  minDistance 10 over spreads 5 and 20; then a single-party candidate.
// Expect:  spread 5 → false; spread 20 → true; single party → true.
func TestBatchDistance_MinDistance(t *testing.T) {
	r := &ruleset.Rule{Name: "x", Type: ruleset.RuleBatchDistance,
		BatchAttribute: "skill", MinDistance: ptrF(10)}
	ev, err := Build(r, nil)
	require.NoError(t, err)

	p1 := []core.Player{numPlayer("a", 10)}
	p2 := []core.Player{numPlayer("b", 15)}
	tight := &Candidate{Players: append(append([]core.Player{}, p1...), p2...), Parties: [][]core.Player{p1, p2}}
	ok, _ := ev.Evaluate(tight)
	assert.False(t, ok, "spread 5 < minDistance 10")

	p3 := []core.Player{numPlayer("c", 30)}
	wide := &Candidate{Players: append(append([]core.Player{}, p1...), p3...), Parties: [][]core.Player{p1, p3}}
	ok, _ = ev.Evaluate(wide)
	assert.True(t, ok, "spread 20 >= minDistance 10")

	single := &Candidate{Players: p1, Parties: [][]core.Player{p1}}
	ok, _ = ev.Evaluate(single)
	assert.True(t, ok, "single party always passes")
}

// Purpose: Verify the latency rule ignores regions above maxLatency and accepts a
// match when a different shared region is within the limit; also exercise
// distanceReference=avg.
// Method:  Players share us-west-2 (over limit) and us-east-1 (under). maxLatency 100.
// Expect:  passes via us-east-1. With distanceReference=avg+maxDistance, a wide
//
//	spread in the only viable region fails.
func TestLatency_IgnoresHighRegionAndAvgDistance(t *testing.T) {
	r := &ruleset.Rule{Name: "x", Type: ruleset.RuleLatency, MaxLatency: ptrI(100)}
	ev, err := Build(r, nil)
	require.NoError(t, err)
	pl := []core.Player{
		{Latencies: map[string]int{"us-east-1": 40, "us-west-2": 250}},
		{Latencies: map[string]int{"us-east-1": 60, "us-west-2": 300}},
	}
	ok, err := ev.Evaluate(&Candidate{Players: pl})
	require.NoError(t, err)
	assert.True(t, ok, "us-east-1 under limit for both; us-west-2 ignored")

	// distanceReference=avg, maxDistance=20: us-east-1 latencies 40 and 60, avg 50,
	// each 10 from avg → within 20 → still passes.
	rAvg := &ruleset.Rule{Name: "y", Type: ruleset.RuleLatency,
		MaxLatency: ptrI(100), DistanceReference: "avg", MaxDistance: ptrF(20)}
	evAvg, err := Build(rAvg, nil)
	require.NoError(t, err)
	ok, _ = evAvg.Evaluate(&Candidate{Players: pl})
	assert.True(t, ok, "spread within maxDistance of avg")

	// Tighten maxDistance to 5: 40 and 60 are 10 from avg 50 → exceeds → fail.
	rTight := &ruleset.Rule{Name: "z", Type: ruleset.RuleLatency,
		MaxLatency: ptrI(100), DistanceReference: "avg", MaxDistance: ptrF(5)}
	evTight, err := Build(rTight, nil)
	require.NoError(t, err)
	ok, _ = evTight.Evaluate(&Candidate{Players: pl})
	assert.False(t, ok, "spread exceeds maxDistance of avg")
}

// Purpose: Verify compound not / or operators and nested statements.
// Method:  children a=avg<=50, b=max>=80; evaluate not(a), or(a,b), and(a, not(b)).
// Expect:  match the boolean algebra for each player set.
func TestCompound_NotOrNested(t *testing.T) {
	a := &ruleset.Rule{Name: "a", Type: ruleset.RuleComparison,
		Measurements: []string{"avg(players.attributes[skill])"}, ReferenceValue: json.RawMessage(`50`), Operation: "<="}
	b := &ruleset.Rule{Name: "b", Type: ruleset.RuleComparison,
		Measurements: []string{"max(players.attributes[skill])"}, ReferenceValue: json.RawMessage(`80`), Operation: ">="}
	others := map[string]Evaluator{}
	for _, r := range []*ruleset.Rule{a, b} {
		ev, err := Build(r, others)
		require.NoError(t, err)
		others[r.Name] = ev
	}
	build := func(stmt string) Evaluator {
		ev, err := Build(&ruleset.Rule{Name: "c", Type: ruleset.RuleCompound, Statement: stmt}, others)
		require.NoError(t, err)
		return ev
	}

	low := numCand(10, 20, 30)  // a:true (avg20), b:false (max30)
	high := numCand(85, 90)     // a:false (avg87), b:true (max90)
	mixed := numCand(10, 90)    // a:true (avg50), b:true (max90)

	// not(a)
	ok, _ := build("not(a)").Evaluate(low)
	assert.False(t, ok)
	ok, _ = build("not(a)").Evaluate(high)
	assert.True(t, ok)

	// or(a, b)
	ok, _ = build("or(a, b)").Evaluate(low)
	assert.True(t, ok, "a true")
	ok, _ = build("or(a, b)").Evaluate(high)
	assert.True(t, ok, "b true")

	// nested: and(a, not(b)) — true only when a holds and b does not
	ok, _ = build("and(a, not(b))").Evaluate(low)
	assert.True(t, ok, "a true, b false")
	ok, _ = build("and(a, not(b))").Evaluate(mixed)
	assert.False(t, ok, "b true → not(b) false")
}

// Purpose: Verify a 3-player party is reduced by partyAggregation before evaluation.
// Method:  One party [10,20,60]; comparison max(players.attributes[skill]) <= 15.
// Expect:  avg → party 30, max 30 > 15 → false; min → party 10, max 10 <= 15 → true.
func TestComparison_ThreePlayerParty(t *testing.T) {
	mk := func(agg string) Evaluator {
		r := &ruleset.Rule{Name: "x", Type: ruleset.RuleComparison,
			Measurements:   []string{"max(players.attributes[skill])"},
			ReferenceValue: json.RawMessage(`15`), Operation: "<=", PartyAggregation: agg}
		ev, err := Build(r, nil)
		require.NoError(t, err)
		return ev
	}
	party := []core.Player{numPlayer("a", 10), numPlayer("b", 20), numPlayer("c", 60)}
	c := &Candidate{
		Players:     party,
		Teams:       map[string][]core.Player{"red": party},
		TeamOrder:   []string{"red"},
		TeamParties: map[string][][]core.Player{"red": {party}},
	}
	ok, err := mk("avg").Evaluate(c)
	require.NoError(t, err)
	assert.False(t, ok, "avg of [10,20,60] is 30 > 15")
	ok, _ = mk("min").Evaluate(c)
	assert.True(t, ok, "min of [10,20,60] is 10 <= 15")
}

// Purpose: Verify that a rule whose measured attribute is absent (no value and no
// default) passes vacuously — there are no measurements that can fail.
// Method:  distance rule on players.attributes[skill] where players carry no skill.
// Expect:  Evaluate returns true.
func TestDistance_MissingAttributePasses(t *testing.T) {
	r := &ruleset.Rule{Name: "x", Type: ruleset.RuleDistance,
		Measurements:   []string{"players.attributes[skill]"},
		ReferenceValue: json.RawMessage(`0`), MaxDistance: ptrF(5)}
	ev, err := Build(r, nil)
	require.NoError(t, err)
	noAttr := []core.Player{{ID: "p1"}, {ID: "p2"}}
	ok, err := ev.Evaluate(&Candidate{Players: noAttr, Teams: map[string][]core.Player{"red": noAttr}})
	require.NoError(t, err)
	assert.True(t, ok, "no skill values means nothing can exceed maxDistance")
}

// Purpose: Verify the latency rule treats an empty player set as a pass (no player
// can violate the threshold).
// Method:  Evaluate a latency rule against a candidate with zero players.
// Expect:  true.
func TestLatency_EmptyCandidatePasses(t *testing.T) {
	r := &ruleset.Rule{Name: "x", Type: ruleset.RuleLatency, MaxLatency: ptrI(50)}
	ev, err := Build(r, nil)
	require.NoError(t, err)
	ok, err := ev.Evaluate(&Candidate{})
	require.NoError(t, err)
	assert.True(t, ok)
}
