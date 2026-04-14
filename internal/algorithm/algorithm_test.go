package algorithm

import (
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

func TestBuild_FormsTwoTeams(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ]
	}`)
	tickets := []core.Ticket{solo("a", 10), solo("b", 11), solo("c", 12), solo("d", 13)}
	out, remaining := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.Empty(t, remaining)
	assert.Len(t, out[0].Teams["red"], 2)
	assert.Len(t, out[0].Teams["blue"], 2)
	assert.Len(t, out[0].TicketIDs, 4)
}

func TestBuild_RespectsBatchDistance(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "teams": [{"name": "all", "minPlayers": 3, "maxPlayers": 3}],
	  "rules": [{"name": "BD", "type": "batchDistance",
	    "batchAttribute": "skill", "maxAttributeDistance": 5}]
	}`)
	tickets := []core.Ticket{solo("a", 10), solo("b", 100), solo("c", 11), solo("d", 12)}
	out, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	ids := append([]string(nil), out[0].TicketIDs...)
	sort.Strings(ids)
	assert.NotContains(t, ids, "b")
}

func TestBuild_NoMatchUnderMin(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "teams": [{"name": "all", "minPlayers": 4, "maxPlayers": 4}]
	}`)
	tickets := []core.Ticket{solo("a", 10), solo("b", 11)}
	out, remaining := Build(rs, evals(t, rs), tickets)
	assert.Empty(t, out)
	assert.Len(t, remaining, 2)
}

func TestBuild_QuantityExpansion(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "teams": [{"name": "team", "minPlayers": 2, "maxPlayers": 2, "quantity": 2}]
	}`)
	tickets := []core.Ticket{solo("a", 1), solo("b", 2), solo("c", 3), solo("d", 4)}
	out, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	assert.Len(t, out[0].Teams["team_1"], 2)
	assert.Len(t, out[0].Teams["team_2"], 2)
}

func TestBuild_BalancedStrategy(t *testing.T) {
	rs := newRS(t, `{
	  "name": "x",
	  "playerAttributes": [{"name":"skill","type":"number"}],
	  "algorithm": {"strategy": "balanced", "balancedAttribute": "skill"},
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ]
	}`)
	tickets := []core.Ticket{solo("a", 10), solo("b", 100), solo("c", 11), solo("d", 99)}
	out, _ := Build(rs, evals(t, rs), tickets)
	require.Len(t, out, 1)
	red := sumSkill(out[0].Teams["red"])
	blue := sumSkill(out[0].Teams["blue"])
	assert.InDelta(t, red, blue, 25, "red=%v blue=%v", red, blue)
}

func sumSkill(ps []core.Player) float64 {
	var s float64
	for _, p := range ps {
		s += p.Attributes["skill"].N
	}
	return s
}
