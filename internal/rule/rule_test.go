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
func ptrI(v int) *int          { return &v }

// Purpose: Verify that the comparison rule (avg(players.skill) <= 50) correctly passes and rejects candidates.
// Method:  Evaluate the same evaluator against a player set with avg=30 and another with avg=75.
// Expect:  avg=30 → true; avg=75 → false.
func TestComparison(t *testing.T) {
	r := &ruleset.Rule{
		Name: "x", Type: ruleset.RuleComparison,
		Measurements:   []string{"avg(players.skill)"},
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
		Measurements:   []string{"avg(teams[red].players.skill)"},
		ReferenceValue: json.RawMessage(`"avg(teams[blue].players.skill)"`),
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

// Purpose: Verify that the batchDistance rule limits the max skill spread within a candidate to maxAttributeDistance.
// Method:  Evaluate against a spread-15 set (10,20,25) and an over-limit set (10,30,50).
// Expect:  Spread-15 → true; over-limit → false.
func TestBatchDistance(t *testing.T) {
	r := &ruleset.Rule{
		Name: "x", Type: ruleset.RuleBatchDistance,
		BatchAttribute: "skill", MaxAttributeDistance: ptrF(15),
	}
	ev, err := Build(r, nil)
	require.NoError(t, err)
	ok, _ := ev.Evaluate(numCand(10, 20, 25))
	assert.True(t, ok)
	ok, _ = ev.Evaluate(numCand(10, 30, 50))
	assert.False(t, ok)
}

// Purpose: Verify that the collection/contains operation detects a target value in flatten(players.modes).
// Method:  Evaluate against one player with modes=["TDM","CTF"] using reference value "TDM".
// Expect:  true is returned.
func TestCollection_Contains(t *testing.T) {
	r := &ruleset.Rule{
		Name: "x", Type: ruleset.RuleCollection,
		Measurements:   []string{"flatten(players.modes)"},
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
		Measurements:   []string{"set_intersection(players.modes)"},
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
		Measurements: []string{"avg(players.skill)"}, ReferenceValue: json.RawMessage(`50`), Operation: "<="}
	r2 := &ruleset.Rule{Name: "b", Type: ruleset.RuleBatchDistance,
		BatchAttribute: "skill", MaxAttributeDistance: ptrF(20)}
	rc := &ruleset.Rule{Name: "c", Type: ruleset.RuleCompound,
		Statement: &ruleset.CompoundStatement{Condition: "and", Rules: []string{"a", "b"}}}

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
