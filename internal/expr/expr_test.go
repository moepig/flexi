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
