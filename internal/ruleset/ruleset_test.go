package ruleset

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const minimalRS = `{
  "name": "minimal",
  "ruleLanguageVersion": "1.0",
  "teams": [{"name": "red", "minPlayers": 1, "maxPlayers": 5}]
}`

const fullRS = `{
  "name": "full",
  "ruleLanguageVersion": "1.0",
  "playerAttributes": [
    {"name": "skill", "type": "number", "default": 10},
    {"name": "char",  "type": "string", "default": "tank"},
    {"name": "modes", "type": "string_list"},
    {"name": "items", "type": "string_number_map"}
  ],
  "algorithm": {
    "strategy": "balanced",
    "batchingPreference": "fastestRegion",
    "balancedAttribute": "skill",
    "backfillPriority": "high"
  },
  "teams": [
    {"name": "red",  "minPlayers": 2, "maxPlayers": 5, "quantity": 1},
    {"name": "blue", "minPlayers": 2, "maxPlayers": 5, "quantity": 1}
  ],
  "rules": [
    {"name": "FairSkill", "type": "distance",
     "measurements": ["avg(teams[red].players.skill)"],
     "referenceValue": "avg(teams[blue].players.skill)",
     "maxDistance": 10},
    {"name": "Ping", "type": "latency", "maxLatency": 150},
    {"name": "ModeMatch", "type": "comparison",
     "measurements": ["players.char"], "referenceValue": "tank", "operation": "="},
    {"name": "ModeIntersect", "type": "collection",
     "measurements": ["flatten(players.modes)"],
     "operation": "reference_intersection_count",
     "referenceValue": ["TDM", "CTF"], "minCount": 1},
    {"name": "Sort", "type": "absoluteSort",
     "sortDirection": "ascending", "sortAttribute": "skill"},
    {"name": "Batch", "type": "batchDistance",
     "batchAttribute": "skill", "maxAttributeDistance": 5},
    {"name": "All", "type": "compound",
     "statement": {"condition": "and", "rules": ["FairSkill", "Ping"]}}
  ],
  "expansions": [
    {"target": "rules[FairSkill].maxDistance",
     "steps": [{"waitTimeSeconds": 5, "value": 25}, {"waitTimeSeconds": 15, "value": 100}]}
  ]
}`

func TestParse_Minimal(t *testing.T) {
	rs, err := Parse([]byte(minimalRS))
	require.NoError(t, err)
	assert.Equal(t, "minimal", rs.Name)
	require.Len(t, rs.Teams, 1)
	assert.Equal(t, "red", rs.Teams[0].Name)
}

func TestParse_Full(t *testing.T) {
	rs, err := Parse([]byte(fullRS))
	require.NoError(t, err)
	assert.Len(t, rs.PlayerAttributes, 4)
	assert.Equal(t, "balanced", rs.Algorithm.Strategy)
	assert.Equal(t, "skill", rs.Algorithm.BalancedAttribute)
	assert.Len(t, rs.Teams, 2)
	assert.Len(t, rs.Rules, 7)
	require.Len(t, rs.Expansions, 1)
	assert.Equal(t, 2, len(rs.Expansions[0].Steps))
}

func TestParse_Errors(t *testing.T) {
	cases := map[string]string{
		"no teams": `{"name":"x"}`,
		"bad json": `not json`,
		"unknown rule type": `{"name":"x","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
		  "rules":[{"name":"y","type":"bogus"}]}`,
		"compound to unknown rule": `{"name":"x","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
		  "rules":[{"name":"y","type":"compound","statement":{"condition":"and","rules":["nope"]}}]}`,
		"balanced needs attr": `{"name":"x","algorithm":{"strategy":"balanced"},
		  "teams":[{"name":"r","minPlayers":1,"maxPlayers":2}]}`,
		"expansion bad target": `{"name":"x","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
		  "expansions":[{"target":"bogus","steps":[{"waitTimeSeconds":1,"value":1}]}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(body))
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidRuleSet), "err: %v", err)
		})
	}
}
