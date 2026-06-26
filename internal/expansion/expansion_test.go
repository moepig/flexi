package expansion

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/moepig/flexi/internal/ruleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Purpose: Verify that a rules[<name>].<field> expansion applies the latest qualifying step based on elapsed time.
// Method:  Set up maxDistance=10 with steps 5s→50 and 15s→200; call Apply at elapsed times 0, 4, 5, 14, 15, and 60 seconds.
// Expect:  maxDistance values are 10/10/50/50/200/200 respectively, and the original RuleSet is not mutated.
func TestApply_RuleField(t *testing.T) {
	max := 10.0
	rs := &ruleset.RuleSet{
		Rules: []ruleset.Rule{{
			Name: "Skill", Type: ruleset.RuleDistance, MaxDistance: &max,
		}},
		Expansions: []ruleset.Expansion{{
			Target: "rules[Skill].maxDistance",
			Steps: []ruleset.ExpansionStep{
				{WaitTimeSeconds: 5, Value: json.RawMessage(`50`)},
				{WaitTimeSeconds: 15, Value: json.RawMessage(`200`)},
			},
		}},
	}
	cases := []struct {
		elapsed time.Duration
		want    float64
	}{
		{0, 10},
		{4 * time.Second, 10},
		{5 * time.Second, 50},
		{14 * time.Second, 50},
		{15 * time.Second, 200},
		{60 * time.Second, 200},
	}
	for _, c := range cases {
		out, err := Apply(rs, c.elapsed)
		require.NoError(t, err)
		require.Len(t, out.Rules, 1)
		assert.Equal(t, c.want, *out.Rules[0].MaxDistance, "elapsed=%v", c.elapsed)
	}
	// original is not mutated
	assert.Equal(t, 10.0, *rs.Rules[0].MaxDistance)
}

// Purpose: Verify that an algorithm.<field> expansion (switching batchingPreference) is applied correctly.
// Method:  Start with "largestPopulation"; add a step at 10s to switch to "fastestRegion"; Apply at 11 seconds.
// Expect:  The output Algorithm.BatchingPreference equals "fastestRegion".
func TestApply_AlgorithmField(t *testing.T) {
	rs := &ruleset.RuleSet{
		Algorithm: ruleset.Algorithm{BatchingPreference: "largestPopulation"},
		Expansions: []ruleset.Expansion{{
			Target: "algorithm.batchingPreference",
			Steps:  []ruleset.ExpansionStep{{WaitTimeSeconds: 10, Value: json.RawMessage(`"fastestRegion"`)}},
		}},
	}
	out, err := Apply(rs, 11*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "fastestRegion", out.Algorithm.BatchingPreference)
}

// Purpose: Verify that a teams[<names>].<field> expansion relaxes team size for
// every named team and leaves the original unmutated.
// Method:  Two teams minPlayers=3; target "teams[red, blue].minPlayers" step 10s→2.
// Expect:  Both teams' minPlayers become 2 at 10s; original stays 3.
func TestApply_TeamField(t *testing.T) {
	rs := &ruleset.RuleSet{
		Teams: []ruleset.Team{
			{Name: "red", MinPlayers: 3, MaxPlayers: 5},
			{Name: "blue", MinPlayers: 3, MaxPlayers: 5},
		},
		Expansions: []ruleset.Expansion{{
			Target: "teams[red, blue].minPlayers",
			Steps:  []ruleset.ExpansionStep{{WaitTimeSeconds: 10, Value: json.RawMessage(`2`)}},
		}},
	}
	out, err := Apply(rs, 11*time.Second)
	require.NoError(t, err)
	assert.Equal(t, 2, out.Teams[0].MinPlayers)
	assert.Equal(t, 2, out.Teams[1].MinPlayers)
	// original not mutated
	assert.Equal(t, 3, rs.Teams[0].MinPlayers)
}

// Purpose: Verify expansions can relax the remaining rule/team fields the schema
// allows: maxLatency, minCount, a comparison referenceValue, and team maxPlayers.
// Method:  One rule set with four expansions, each a single 10s step; Apply at 11s.
// Expect:  Each target field takes its expanded value; the original is unmodified.
func TestApply_VariousFields(t *testing.T) {
	maxLat := 100
	minCount := 2
	rs := &ruleset.RuleSet{
		Teams: []ruleset.Team{{Name: "red", MinPlayers: 2, MaxPlayers: 2}},
		Rules: []ruleset.Rule{
			{Name: "Ping", Type: ruleset.RuleLatency, MaxLatency: &maxLat},
			{Name: "Modes", Type: ruleset.RuleCollection, Operation: "reference_intersection_count", MinCount: &minCount},
			{Name: "Skill", Type: ruleset.RuleComparison, ReferenceValue: json.RawMessage(`50`), Operation: "<="},
		},
		Expansions: []ruleset.Expansion{
			{Target: "rules[Ping].maxLatency", Steps: []ruleset.ExpansionStep{{WaitTimeSeconds: 10, Value: json.RawMessage(`250`)}}},
			{Target: "rules[Modes].minCount", Steps: []ruleset.ExpansionStep{{WaitTimeSeconds: 10, Value: json.RawMessage(`1`)}}},
			{Target: "rules[Skill].referenceValue", Steps: []ruleset.ExpansionStep{{WaitTimeSeconds: 10, Value: json.RawMessage(`80`)}}},
			{Target: "teams[red].maxPlayers", Steps: []ruleset.ExpansionStep{{WaitTimeSeconds: 10, Value: json.RawMessage(`4`)}}},
		},
	}
	out, err := Apply(rs, 11*time.Second)
	require.NoError(t, err)
	assert.Equal(t, 250, *out.Rules[0].MaxLatency)
	assert.Equal(t, 1, *out.Rules[1].MinCount)
	assert.Equal(t, "80", string(out.Rules[2].ReferenceValue))
	assert.Equal(t, 4, out.Teams[0].MaxPlayers)

	// original not mutated
	assert.Equal(t, 100, *rs.Rules[0].MaxLatency)
	assert.Equal(t, "50", string(rs.Rules[2].ReferenceValue))
	assert.Equal(t, 2, rs.Teams[0].MaxPlayers)
}
