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
