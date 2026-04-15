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
		"avg(players.skill)":          20,
		"sum(players.skill)":          60,
		"min(players.skill)":          10,
		"max(players.skill)":          30,
		"count(players.skill)":        3,
		"avg(flatten(players.skill))": 20,
		"42":                          42,
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
	n, err := Parse("avg(teams[red].players.skill)")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	got, _ := v.AsNumber()
	assert.Equal(t, 15.0, got)

	n, err = Parse("avg(teams[blue].players.skill)")
	require.NoError(t, err)
	v, err = Eval(n, ctx)
	require.NoError(t, err)
	got, _ = v.AsNumber()
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
	n, err := Parse("set_intersection(players.modes)")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	require.Equal(t, KindStringList, v.Kind)
	sort.Strings(v.SL)
	assert.Equal(t, []string{"CTF"}, v.SL)
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
	n, err := Parse("avg(players.items[sword])")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	got, _ := v.AsNumber()
	assert.Equal(t, 7.5, got)
}

// Purpose: Verify that representative malformed expressions are rejected by Parse.
// Method:  Run sub-tests for: empty string, bare "players", incomplete dotted references, unsupported syntax, unclosed call.
// Expect:  Every case returns a non-nil error.
func TestParseErrors(t *testing.T) {
	bad := []string{
		"",
		"players",
		"players.",
		"teams[red]",
		"teams[red].players",
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
