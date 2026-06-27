package flexi_test

import (
	"testing"

	"github.com/moepig/flexi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file pins flexi's parser to the rule examples printed verbatim in the
// AWS GameLift FlexMatch documentation, so a future doc revision (or a parser
// regression) that breaks "copy the example, it just works" is caught here.
//
// The rule snippets below are copied character-for-character from:
//
//	https://docs.aws.amazon.com/gameliftservers/latest/flexmatchguide/match-rules-reference-ruletype.html
//
// Each is a single rule object. FlexMatch's CreateMatchmakingRuleSet (mirrored
// by flexi.New) accepts a whole rule set, so each documented rule is spliced
// verbatim into the smallest valid surrounding rule set via docRuleSet; a
// successful flexi.New therefore proves the documented JSON loads unchanged.

// docRuleSet embeds the given rule objects (each already a complete JSON object,
// exactly as printed in the docs) into the smallest rule set flexi.New accepts.
func docRuleSet(rules string) []byte {
	return []byte(`{
	  "name": "doc-example",
	  "ruleLanguageVersion": "1.0",
	  "teams": [{"name": "all", "minPlayers": 2, "maxPlayers": 4}],
	  "rules": [` + rules + `]
	}`)
}

// Verbatim "Batch distance rule" examples. Note maxDistance is quoted ("500"):
// the schema page types it as a number, but the rule-type page prints a string,
// and flexi accepts both forms.
const (
	docBatchDistanceNumeric = `{
  "name":"SimilarSkillRatings",
  "description":"All players must have similar skill ratings",
  "type":"batchDistance",
  "batchAttribute":"SkillRating",
  "maxDistance":"500"
}`

	// String-attribute batching form: no maxDistance at all.
	docBatchDistanceString = `{
  "name":"SameGameMode",
  "description":"All players must have the same game mode",
  "type":"batchDistance",
  "batchAttribute":"GameMode"
}`

	// Verbatim "Absolute sort rule" example.
	docAbsoluteSort = `{
    "name":"AbsoluteSortExample",
    "type":"absoluteSort",
    "sortDirection":"ascending",
    "sortAttribute":"skill",
    "partyAggregation":"avg"
}`

	// Verbatim "Compound rule" example. Its statement references four rules that
	// must be defined earlier in the rule set, so docCompoundSupport supplies
	// them; only the compound object itself is reproduced from the docs.
	docCompound = `{
    "name": "CompoundRuleExample",
    "type": "compound",
    "statement": "or(and(SeriousPlayers, VeryCloseSkill), and(CasualPlayers, SomewhatCloseSkill))"
}`
)

// docCompoundSupport defines the four leaf rules the documented compound
// statement references. They are not reproduced from the docs (the docs leave
// them implicit); they exist only so the verbatim compound rule resolves.
const docCompoundSupport = `
    {"name":"SeriousPlayers","type":"comparison","measurements":["players.attributes[mode]"],"referenceValue":"ranked","operation":"="},
    {"name":"CasualPlayers","type":"comparison","measurements":["players.attributes[mode]"],"referenceValue":"casual","operation":"="},
    {"name":"VeryCloseSkill","type":"distance","measurements":["avg(players.attributes[skill])"],"referenceValue":0,"maxDistance":10},
    {"name":"SomewhatCloseSkill","type":"distance","measurements":["avg(players.attributes[skill])"],"referenceValue":0,"maxDistance":50}`

// Purpose: Verify the rule examples printed verbatim in the FlexMatch rule-type
// documentation load through flexi.New unchanged.
// Method:  Splice each documented rule object, copied character-for-character
//
//	from the docs, into the smallest valid surrounding rule set and call
//	flexi.New. The compound example additionally pulls in the leaf rules its
//	statement references.
//
// Expect:  flexi.New returns no error for every documented example.
func TestDocs_RuleTypeExamplesLoadVerbatim(t *testing.T) {
	cases := map[string]string{
		"batchDistance numeric": docBatchDistanceNumeric,
		"batchDistance string":  docBatchDistanceString,
		"absoluteSort":          docAbsoluteSort,
		"compound":              docCompoundSupport + ",\n" + docCompound,
	}
	for name, rules := range cases {
		t.Run(name, func(t *testing.T) {
			mm, err := flexi.New(docRuleSet(rules))
			require.NoError(t, err, "documented rule example failed to load")
			assert.NotNil(t, mm)
		})
	}
}
