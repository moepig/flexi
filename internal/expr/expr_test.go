package expr

import (
	"sort"
	"testing"

	"github.com/moepig/flexi/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func num(v float64) core.Attribute { return core.Attribute{Kind: core.AttrNumber, N: v} }
func sl(v ...string) core.Attribute {
	cp := append([]string(nil), v...)
	return core.Attribute{Kind: core.AttrStringList, SL: cp}
}
func sdm(m map[string]float64) core.Attribute {
	return core.Attribute{Kind: core.AttrStringNumberMap, SDM: m}
}

func players(skills ...float64) []core.Player {
	out := make([]core.Player, len(skills))
	for i, s := range skills {
		out[i] = core.Player{
			ID: "p", Attributes: core.Attributes{"skill": num(s)},
		}
	}
	return out
}

// Purpose: Verify that numeric aggregate expressions (avg/sum/min/max/count/flatten/literal) evaluate to the expected numbers.
// Method:  For a player set with skill=[10,20,30], Parse and Eval each representative expression and check AsNumber.
// Expect:  Results match 20/60/10/30/3/20/42 respectively.
func TestParseAndEval_Numbers(t *testing.T) {
	ctx := &EvalContext{Players: players(10, 20, 30)}

	cases := map[string]float64{
		"avg(players.attributes[skill])":          20,
		"sum(players.attributes[skill])":          60,
		"min(players.attributes[skill])":          10,
		"max(players.attributes[skill])":          30,
		"median(players.attributes[skill])":       20,
		"count(players.attributes[skill])":        3,
		"avg(flatten(players.attributes[skill]))": 20,
		"42": 42,
	}
	for src, want := range cases {
		t.Run(src, func(t *testing.T) {
			n, err := Parse(src)
			require.NoError(t, err)
			v, err := Eval(n, ctx)
			require.NoError(t, err)
			got, ok := v.AsNumber()
			require.True(t, ok, "want number, got %v", v)
			assert.Equal(t, want, got)
		})
	}
}

// Purpose: Verify that teams[<name>].players.<attr> references resolve per-team correctly.
// Method:  Build an EvalContext with red=[10,20] and blue=[40,60]; evaluate avg for each team.
// Expect:  avg(teams[red].players.skill)=15.0 and avg(teams[blue].players.skill)=50.0.
func TestParseAndEval_TeamScope(t *testing.T) {
	ctx := &EvalContext{
		TeamPlayers: map[string][]core.Player{
			"red":  players(10, 20),
			"blue": players(40, 60),
		},
	}
	n, err := Parse("avg(teams[red].players.attributes[skill])")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	got, _ := v.AsNumber()
	assert.Equal(t, 15.0, got)

	n, err = Parse("avg(teams[blue].players.attributes[skill])")
	require.NoError(t, err)
	v, err = Eval(n, ctx)
	require.NoError(t, err)
	got, _ = v.AsNumber()
	assert.Equal(t, 50.0, got)
}

// Purpose: Verify per-team (nested) aggregation semantics for teams[*].
// Method:  red=[10,20], blue=[40,60] in deterministic order; evaluate
//
//	avg(teams[*].players.attributes[skill]) and max(avg(...)).
//
// Expect:  avg(...) is the per-team list [15,50]; max(avg(...)) is the scalar 50.
func TestParseAndEval_NestedAggregation(t *testing.T) {
	ctx := &EvalContext{
		TeamPlayers: map[string][]core.Player{
			"red":  players(10, 20),
			"blue": players(40, 60),
		},
		TeamOrder: []string{"red", "blue"},
	}
	n, err := Parse("avg(teams[*].players.attributes[skill])")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	nums, ok := v.FlattenNumbers()
	require.True(t, ok)
	assert.Equal(t, []float64{15, 50}, nums)

	n, err = Parse("max(avg(teams[*].players.attributes[skill]))")
	require.NoError(t, err)
	v, err = Eval(n, ctx)
	require.NoError(t, err)
	got, ok := v.AsNumber()
	require.True(t, ok)
	assert.Equal(t, 50.0, got)
}

// Purpose: Verify that set_intersection(players.modes) returns the common elements across all players' mode lists.
// Method:  Evaluate against three players whose modes intersect only at "CTF"; sort and inspect the result.
// Expect:  Result is KindStringList containing exactly ["CTF"].
func TestSetIntersection(t *testing.T) {
	ctx := &EvalContext{
		Players: []core.Player{
			{Attributes: core.Attributes{"modes": sl("TDM", "CTF", "FFA")}},
			{Attributes: core.Attributes{"modes": sl("CTF", "FFA")}},
			{Attributes: core.Attributes{"modes": sl("CTF", "TDM")}},
		},
	}
	n, err := Parse("set_intersection(players.attributes[modes])")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	strs, ok := v.FlattenStrings()
	require.True(t, ok)
	sort.Strings(strs)
	assert.Equal(t, []string{"CTF"}, strs)
}

// Purpose: Verify that players.<attr>[<key>] correctly extracts values from a string_number_map attribute.
// Method:  Two players with items[sword]=5 and items[sword]=10; evaluate avg(players.items[sword]).
// Expect:  Result is 7.5.
func TestStringNumberMap(t *testing.T) {
	ctx := &EvalContext{
		Players: []core.Player{
			{Attributes: core.Attributes{"items": sdm(map[string]float64{"sword": 5})}},
			{Attributes: core.Attributes{"items": sdm(map[string]float64{"sword": 10})}},
		},
	}
	n, err := Parse("avg(players.attributes[items][sword])")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	got, _ := v.AsNumber()
	assert.Equal(t, 7.5, got)
}

// Purpose: Verify that representative malformed expressions are rejected by Parse.
// Method:  Run sub-tests for: empty string, attribute access missing attributes[...],
//
//	team scope missing .players, unsupported syntax, unclosed call.
//
// Expect:  Every case returns a non-nil error.
func TestParseErrors(t *testing.T) {
	bad := []string{
		"",
		"players.",
		"players.skill",      // missing attributes[...]
		"teams[red]",         // missing .players
		"players.attributes", // missing [attr]
		"foo bar",
		"avg(",
	}
	for _, src := range bad {
		t.Run(src, func(t *testing.T) {
			_, err := Parse(src)
			assert.Error(t, err)
		})
	}
}
