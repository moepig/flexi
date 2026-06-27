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
     "measurements": ["avg(teams[red].players.attributes[skill])"],
     "referenceValue": "avg(teams[blue].players.attributes[skill])",
     "maxDistance": 10},
    {"name": "Ping", "type": "latency", "maxLatency": 150},
    {"name": "ModeMatch", "type": "comparison",
     "measurements": ["players.attributes[char]"], "referenceValue": "tank", "operation": "="},
    {"name": "ModeIntersect", "type": "collection",
     "measurements": ["flatten(players.attributes[modes])"],
     "operation": "reference_intersection_count",
     "referenceValue": ["TDM", "CTF"], "minCount": 1},
    {"name": "Sort", "type": "absoluteSort",
     "sortDirection": "ascending", "sortAttribute": "skill"},
    {"name": "Batch", "type": "batchDistance",
     "batchAttribute": "skill", "maxDistance": 5},
    {"name": "All", "type": "compound",
     "statement": "and(FairSkill, Ping)"}
  ],
  "expansions": [
    {"target": "rules[FairSkill].maxDistance",
     "steps": [{"waitTimeSeconds": 5, "value": 25}, {"waitTimeSeconds": 15, "value": 100}]}
  ]
}`

// Purpose: Verify that a rule set containing only the required minimum fields parses without error.
// Method:  Parse minimalRS, which contains only "name" and "teams".
// Expect:  No error; rs.Name and Teams[0].Name match the expected values.
func TestParse_Minimal(t *testing.T) {
	rs, err := Parse([]byte(minimalRS))
	require.NoError(t, err)
	assert.Equal(t, "minimal", rs.Name)
	require.Len(t, rs.Teams, 1)
	assert.Equal(t, "red", rs.Teams[0].Name)
}

// Purpose: Verify that a fully-populated rule set is parsed faithfully across all field types.
// Method:  Parse fullRS, which includes playerAttributes, all algorithm fields, all 7 rule kinds, and expansions.
// Expect:  4 player attributes, 7 rules, 1 expansion with 2 steps, and algorithm fields set as specified.
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

// Purpose: Verify the comparison "compare across players" form parses without a
// referenceValue when the operation is = or != (FlexMatch spec).
// Method:  Parse a rule set whose comparison rule omits referenceValue and uses !=.
// Expect:  No error.
func TestParse_ComparisonAcrossPlayers(t *testing.T) {
	body := `{"name":"x","ruleLanguageVersion":"1.0","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
	  "rules":[{"name":"diff","type":"comparison","measurements":["players.attributes[char]"],"operation":"!="}]}`
	rs, err := Parse([]byte(body))
	require.NoError(t, err)
	require.Len(t, rs.Rules, 1)
}

// Purpose: Verify that maxDistance/minDistance accept string-encoded numbers, as
// shown in the FlexMatch rule-type examples (e.g. "maxDistance":"500"), in
// addition to the JSON-number form in the schema page.
// Method:  Parse a batchDistance rule whose maxDistance is the string "500".
// Expect:  No error; MaxDistance resolves to the float64 500.
func TestParse_StringEncodedMaxDistance(t *testing.T) {
	body := `{"name":"x","ruleLanguageVersion":"1.0",
	  "teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
	  "rules":[{"name":"SimilarSkill","type":"batchDistance",
	    "batchAttribute":"SkillRating","maxDistance":"500"}]}`
	rs, err := Parse([]byte(body))
	require.NoError(t, err)
	require.Len(t, rs.Rules, 1)
	require.NotNil(t, rs.Rules[0].MaxDistance)
	assert.Equal(t, 500.0, *rs.Rules[0].MaxDistance)

	// A non-numeric string is still rejected.
	bad := `{"name":"x","ruleLanguageVersion":"1.0",
	  "teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
	  "rules":[{"name":"Bad","type":"batchDistance",
	    "batchAttribute":"SkillRating","maxDistance":"lots"}]}`
	_, err = Parse([]byte(bad))
	assert.Error(t, err)
}

// Purpose: Verify the compound statement parser enforces operator arity: not is
// unary, and/or/xor take two or more arguments, and only and/or/not/xor are
// recognised operators.
// Method:  Parse representative well-formed and malformed statements.
// Expect:  well-formed parse cleanly; malformed return an error.
func TestParseCompound_Arity(t *testing.T) {
	for _, s := range []string{"and(a,b)", "or(a,b,c)", "not(a)", "xor(a,b)", "and(a, not(b), c)"} {
		if _, err := ParseCompound(s); err != nil {
			t.Errorf("ParseCompound(%q) unexpected error: %v", s, err)
		}
	}
	bad := map[string]string{
		"not with two args": "not(a,b)",
		"and with one arg":  "and(a)",
		"or with one arg":   "or(a)",
		"unknown operator":  "nand(a,b)",
		"unterminated":      "and(a,b",
	}
	for name, s := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := ParseCompound(s)
			assert.Error(t, err)
		})
	}
}

// Purpose: Verify that representative invalid inputs are rejected by Parse with ErrInvalidRuleSet.
// Method:  Run sub-tests for: missing teams, malformed JSON, unknown rule type, unknown rule reference,
//
//	balanced strategy without balancedAttribute, and an invalid expansion target.
//
// Expect:  Every case returns an error wrapping ErrInvalidRuleSet.
func TestParse_Errors(t *testing.T) {
	cases := map[string]string{
		"no teams":                        `{"name":"x","ruleLanguageVersion":"1.0"}`,
		"bad json":                        `not json`,
		"missing ruleLanguageVersion":     `{"name":"x","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}]}`,
		"unsupported ruleLanguageVersion": `{"name":"x","ruleLanguageVersion":"2.0","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}]}`,
		"unknown rule type": `{"name":"x","ruleLanguageVersion":"1.0","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
		  "rules":[{"name":"y","type":"bogus"}]}`,
		"compound to unknown rule": `{"name":"x","ruleLanguageVersion":"1.0","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
		  "rules":[{"name":"y","type":"compound","statement":"nope"}]}`,
		"balanced needs attr": `{"name":"x","ruleLanguageVersion":"1.0","algorithm":{"strategy":"balanced"},
		  "teams":[{"name":"r","minPlayers":1,"maxPlayers":2}]}`,
		"expansion bad target": `{"name":"x","ruleLanguageVersion":"1.0","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
		  "expansions":[{"target":"bogus","steps":[{"waitTimeSeconds":1,"value":1}]}]}`,
		"invalid batchingPreference": `{"name":"x","ruleLanguageVersion":"1.0","algorithm":{"batchingPreference":"balanced"},
		  "teams":[{"name":"r","minPlayers":1,"maxPlayers":2}]}`,
		"sorted needs sortByAttributes": `{"name":"x","ruleLanguageVersion":"1.0","algorithm":{"strategy":"exhaustiveSearch","batchingPreference":"sorted"},
		  "teams":[{"name":"r","minPlayers":1,"maxPlayers":2}]}`,
		"bad expansionAgeSelection": `{"name":"x","ruleLanguageVersion":"1.0","algorithm":{"expansionAgeSelection":"middle"},
		  "teams":[{"name":"r","minPlayers":1,"maxPlayers":2}]}`,
		"distanceSort needs attr": `{"name":"x","ruleLanguageVersion":"1.0","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
		  "rules":[{"name":"s","type":"distanceSort","sortDirection":"ascending"}]}`,
		"compound rejects batchDistance": `{"name":"x","ruleLanguageVersion":"1.0","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
		  "rules":[{"name":"bd","type":"batchDistance","batchAttribute":"skill","maxDistance":5},
		           {"name":"c","type":"compound","statement":"not(bd)"}]}`,
		"latency distanceReference needs maxDistance": `{"name":"x","ruleLanguageVersion":"1.0","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
		  "rules":[{"name":"l","type":"latency","maxLatency":100,"distanceReference":"min"}]}`,
		"comparison without referenceValue needs = or !=": `{"name":"x","ruleLanguageVersion":"1.0","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
		  "rules":[{"name":"c","type":"comparison","measurements":["players.attributes[skill]"],"operation":"<="}]}`,
		"collection contains needs referenceValue": `{"name":"x","ruleLanguageVersion":"1.0","teams":[{"name":"r","minPlayers":1,"maxPlayers":2}],
		  "rules":[{"name":"c","type":"collection","measurements":["players.attributes[modes]"],"operation":"contains"}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(body))
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidRuleSet), "err: %v", err)
		})
	}
}
