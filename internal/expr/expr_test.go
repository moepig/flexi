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

// Purpose: Verify the stddev aggregation (population standard deviation).
// Method:  stddev(players.attributes[skill]) over skill=[10,20,30].
// Expect:  sqrt(((10-20)^2+(20-20)^2+(30-20)^2)/3) = sqrt(200/3) ≈ 8.1650.
func TestStddev(t *testing.T) {
	ctx := &EvalContext{Players: players(10, 20, 30)}
	n, err := Parse("stddev(players.attributes[skill])")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	got, ok := v.AsNumber()
	require.True(t, ok)
	assert.InDelta(t, 8.16497, got, 1e-4)
}

// Purpose: Verify teams[<name>].players[playerId] resolves to the team's player IDs.
// Method:  Red team with players r1/r2; evaluate teams[red].players[playerId].
// Expect:  The string list ["r1","r2"].
func TestPlayerIDs(t *testing.T) {
	ctx := &EvalContext{
		TeamPlayers: map[string][]core.Player{
			"red": {{ID: "r1"}, {ID: "r2"}},
		},
	}
	n, err := Parse("teams[red].players[playerId]")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	strs, ok := v.FlattenStrings()
	require.True(t, ok)
	assert.Equal(t, []string{"r1", "r2"}, strs)
}

// Purpose: Verify count over a single team yields a scalar, and over teams[*] yields
// a per-team list (FlexMatch's nested-aggregation semantics).
// Method:  red=[10,20], blue=[40,60,80]; evaluate count(teams[red].players) and
//
//	count(teams[*].players).
//
// Expect:  count(teams[red].players)=2; count(teams[*].players)=[2,3].
func TestCount_PlayersScalarAndList(t *testing.T) {
	ctx := &EvalContext{
		TeamPlayers: map[string][]core.Player{
			"red":  players(10, 20),
			"blue": players(40, 60, 80),
		},
		TeamOrder: []string{"red", "blue"},
	}
	n, err := Parse("count(teams[red].players)")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	got, ok := v.AsNumber()
	require.True(t, ok)
	assert.Equal(t, 2.0, got)

	n, err = Parse("count(teams[*].players)")
	require.NoError(t, err)
	v, err = Eval(n, ctx)
	require.NoError(t, err)
	nums, ok := v.FlattenNumbers()
	require.True(t, ok)
	assert.Equal(t, []float64{2, 3}, nums)
}

// Purpose: Verify set_intersection maps over teams (per-team intersection) for a
// teams[*] scope.
// Method:  red players share "CTF"; blue players share "TDM"; evaluate
//
//	set_intersection(teams[*].players.attributes[modes]).
//
// Expect:  A per-team result: [["CTF"], ["TDM"]].
func TestSetIntersection_PerTeam(t *testing.T) {
	ctx := &EvalContext{
		TeamPlayers: map[string][]core.Player{
			"red":  {{Attributes: core.Attributes{"modes": sl("CTF", "FFA")}}, {Attributes: core.Attributes{"modes": sl("CTF", "TDM")}}},
			"blue": {{Attributes: core.Attributes{"modes": sl("TDM", "FFA")}}, {Attributes: core.Attributes{"modes": sl("TDM", "CTF")}}},
		},
		TeamOrder: []string{"red", "blue"},
	}
	n, err := Parse("set_intersection(teams[*].players.attributes[modes])")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	require.Equal(t, KindList, v.Kind)
	require.Len(t, v.List, 2)
	red, _ := v.List[0].FlattenStrings()
	blue, _ := v.List[1].FlattenStrings()
	assert.Equal(t, []string{"CTF"}, red)
	assert.Equal(t, []string{"TDM"}, blue)
}

// Purpose: Verify that indexing a string_number_map with a key no player has yields
// an empty aggregation (KindNone), not an error.
// Method:  Players carry items[sword]; evaluate avg(players.attributes[items][shield]).
// Expect:  The result is KindNone (no values to average).
func TestStringNumberMap_MissingKey(t *testing.T) {
	ctx := &EvalContext{
		Players: []core.Player{
			{Attributes: core.Attributes{"items": sdm(map[string]float64{"sword": 5})}},
			{Attributes: core.Attributes{"items": sdm(map[string]float64{"sword": 10})}},
		},
	}
	n, err := Parse("avg(players.attributes[items][shield])")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	assert.Equal(t, KindNone, v.Kind)
}

// Purpose: Verify the data-type contracts of the aggregation functions: numeric
// aggregations require numbers, and set_intersection requires string lists.
// Method:  apply avg/sum/min/max/median/stddev to a string attribute, and
//
//	set_intersection to a numeric attribute.
//
// Expect:  every type-mismatched evaluation returns an error.
func TestAggregation_TypeMismatchErrors(t *testing.T) {
	strPlayers := []core.Player{
		{Attributes: core.Attributes{"char": {Kind: core.AttrString, S: "mage"}}},
		{Attributes: core.Attributes{"char": {Kind: core.AttrString, S: "rogue"}}},
	}
	ctx := &EvalContext{Players: strPlayers}
	for _, fn := range []string{"avg", "sum", "min", "max", "median", "stddev"} {
		t.Run(fn+" on strings", func(t *testing.T) {
			n, err := Parse(fn + "(players.attributes[char])")
			require.NoError(t, err)
			_, err = Eval(n, ctx)
			assert.Error(t, err, "%s should reject string values", fn)
		})
	}

	numCtx := &EvalContext{Players: players(10, 20)}
	n, err := Parse("set_intersection(players.attributes[skill])")
	require.NoError(t, err)
	_, err = Eval(n, numCtx)
	assert.Error(t, err, "set_intersection should reject numeric values")
}

// Purpose: Verify flatten and count operate on string_list values. flatten
// merges one nesting level; count is type-agnostic and, over a nested list,
// produces a per-sublist count (List<?> handled per the nested-aggregation rule).
// Method:  two players with modes lists [TDM,CTF] and [FFA]; flatten one level,
//
//	count over the per-player lists, and count(players) for the roster size.
//
// Expect:  flatten yields the concatenated strings; count yields per-player
//
//	element counts [2,1]; count(players) yields the scalar 2.
func TestFlattenAndCount_Strings(t *testing.T) {
	ctx := &EvalContext{Players: []core.Player{
		{Attributes: core.Attributes{"modes": sl("TDM", "CTF")}},
		{Attributes: core.Attributes{"modes": sl("FFA")}},
	}}

	n, err := Parse("flatten(players.attributes[modes])")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	strs, ok := v.FlattenStrings()
	require.True(t, ok)
	sort.Strings(strs)
	assert.Equal(t, []string{"CTF", "FFA", "TDM"}, strs)

	n, err = Parse("count(players.attributes[modes])")
	require.NoError(t, err)
	v, err = Eval(n, ctx)
	require.NoError(t, err)
	counts, ok := v.FlattenNumbers()
	require.True(t, ok)
	assert.Equal(t, []float64{2, 1}, counts, "per-player mode counts")

	n, err = Parse("count(players)")
	require.NoError(t, err)
	v, err = Eval(n, ctx)
	require.NoError(t, err)
	got, ok := v.AsNumber()
	require.True(t, ok)
	assert.Equal(t, 2.0, got, "roster size")
}

// Purpose: Verify the teams[a,b] property-expression form (a set of specific
// named teams) resolves to a per-team grouping (List<List<number>>) and that
// aggregations map over each team sublist.
// Method:  red=[10,20], blue=[40,60]; evaluate teams[red,blue].players.attributes[skill]
//
//	and avg(teams[red,blue].players.attributes[skill]).
//
// Expect:  the raw form is [[10,20],[40,60]]; the avg form is the per-team [15,50].
func TestParseAndEval_MultipleNamedTeams(t *testing.T) {
	ctx := &EvalContext{
		TeamPlayers: map[string][]core.Player{
			"red":  players(10, 20),
			"blue": players(40, 60),
		},
		TeamOrder: []string{"red", "blue"},
	}
	n, err := Parse("teams[red,blue].players.attributes[skill]")
	require.NoError(t, err)
	v, err := Eval(n, ctx)
	require.NoError(t, err)
	require.Equal(t, KindList, v.Kind)
	require.Len(t, v.List, 2)
	red, _ := v.List[0].FlattenNumbers()
	blue, _ := v.List[1].FlattenNumbers()
	assert.Equal(t, []float64{10, 20}, red)
	assert.Equal(t, []float64{40, 60}, blue)

	n, err = Parse("avg(teams[red,blue].players.attributes[skill])")
	require.NoError(t, err)
	v, err = Eval(n, ctx)
	require.NoError(t, err)
	nums, ok := v.FlattenNumbers()
	require.True(t, ok)
	assert.Equal(t, []float64{15, 50}, nums)
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
		"players[bogus]",                  // only [playerId] is valid
		"teams[].players",                 // empty team name
		"teams[*]",                        // missing .players
		"avg(players.attributes[skill]",   // unclosed call
		"set_intersection(players.modes)", // missing .attributes
	}
	for _, src := range bad {
		t.Run(src, func(t *testing.T) {
			_, err := Parse(src)
			assert.Error(t, err)
		})
	}
}
