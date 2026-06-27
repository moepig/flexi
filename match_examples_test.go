package flexi_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/moepig/flexi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file pins flexi's public Enqueue→Tick behaviour against the rule sets
// published in the AWS GameLift FlexMatch documentation's "rule set examples"
// page, so that each documented example (and the rule constructs it relies on)
// has end-to-end integration coverage:
//
//	https://docs.aws.amazon.com/gameliftservers/latest/flexmatchguide/match-examples.html
//
// The JSON below is adapted from those examples (stripped of the // comments the
// docs embed, and scaled down to the smallest team sizes that still exercise the
// same rules) rather than copied verbatim; docs_test.go covers verbatim parsing
// of the rule-type reference page separately.

// soloAttrLat builds a single-player ticket carrying both attributes and
// per-region latencies (latency-rule examples need both).
func soloAttrLat(id string, attrs flexi.Attributes, lat map[string]int) flexi.Ticket {
	return flexi.Ticket{ID: id, Players: []flexi.Player{{ID: id, Attributes: attrs, Latencies: lat}}}
}

// Example 1: Create two teams with evenly matched players.
// Covers the comparison rule's "compare to reference value" form whose
// referenceValue is itself a property expression — EqualTeamSizes requires
// count(teams[red].players) = count(teams[blue].players) — alongside a
// teams[*] distance rule. The equal-size comparison must actively gate: with
// five equal-skill solos it permits only a 2v2 (rejecting 3v2), leaving one
// ticket queued.
func TestEndToEnd_Example1_EqualTeamSizes(t *testing.T) {
	// EqualTeamSizes compares team sizes with
	// count(teams[cowboys].players) = count(teams[aliens].players). flexi defers
	// such team-scoped rules in the incremental placement gate until the teams
	// they reference reach minPlayers, so the rule is enforced only once both
	// teams are populated — never against the imbalanced partial matches the
	// greedy builder must traverse while filling teams.
	body := `{
	  "name": "aliens_vs_cowboys",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name": "skill", "type": "number", "default": 10}],
	  "teams": [
	    {"name": "cowboys", "minPlayers": 2, "maxPlayers": 3},
	    {"name": "aliens",  "minPlayers": 2, "maxPlayers": 3}
	  ],
	  "rules": [
	    {"name": "FairTeamSkill", "type": "distance",
	     "measurements": ["avg(teams[*].players.attributes[skill])"],
	     "referenceValue": "avg(flatten(teams[*].players.attributes[skill]))",
	     "maxDistance": 10},
	    {"name": "EqualTeamSizes", "type": "comparison",
	     "measurements": ["count(teams[cowboys].players)"],
	     "referenceValue": "count(teams[aliens].players)",
	     "operation": "="}
	  ]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		require.NoError(t, mm.Enqueue(solo(id, 50)))
	}
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "five equal-skill solos form one 2v2 match")
	assert.Len(t, matches[0].Teams["cowboys"], 2)
	assert.Len(t, matches[0].Teams["aliens"], 2)
	assert.Equal(t, 1, mm.Pending(), "EqualTeamSizes blocks 3v2, so the fifth ticket stays queued")
}

// Example 2: Create uneven teams (Hunters vs Monster).
// Covers comparison rules whose referenceValue is a scalar (MonsterSelection
// "= 1", PlayerSelection "= 0") and a property expression with an ordering
// operator (MonsterSkill avg(...) ">=" max(...)), across asymmetric teams.
func TestEndToEnd_Example2_UnevenTeamsComparison(t *testing.T) {
	body := `{
	  "name": "players_vs_monster",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [
	    {"name": "skill", "type": "number", "default": 10},
	    {"name": "desiredSkillOfMonster", "type": "number", "default": 10},
	    {"name": "wantsToBeMonster", "type": "number", "default": 0}
	  ],
	  "teams": [
	    {"name": "players", "minPlayers": 2, "maxPlayers": 2},
	    {"name": "monster", "minPlayers": 1, "maxPlayers": 1}
	  ],
	  "rules": [
	    {"name": "MonsterSelection", "type": "comparison",
	     "measurements": ["teams[monster].players.attributes[wantsToBeMonster]"],
	     "referenceValue": 1, "operation": "="},
	    {"name": "PlayerSelection", "type": "comparison",
	     "measurements": ["teams[players].players.attributes[wantsToBeMonster]"],
	     "referenceValue": 0, "operation": "="},
	    {"name": "MonsterSkill", "type": "comparison",
	     "measurements": ["avg(teams[monster].players.attributes[skill])"],
	     "referenceValue": "max(teams[players].players.attributes[desiredSkillOfMonster])",
	     "operation": ">="}
	  ]
	}`
	hunter := func(id string, desired float64) flexi.Ticket {
		return soloAttr(id, flexi.Attributes{
			"skill":                 flexi.Number(10),
			"desiredSkillOfMonster": flexi.Number(desired),
			"wantsToBeMonster":      flexi.Number(0),
		})
	}
	monster := func(id string, skill float64) flexi.Ticket {
		return soloAttr(id, flexi.Attributes{
			"skill":                 flexi.Number(skill),
			"desiredSkillOfMonster": flexi.Number(10),
			"wantsToBeMonster":      flexi.Number(1),
		})
	}

	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	// Two hunters wanting a monster of at least skill 40, plus a weak monster.
	require.NoError(t, mm.Enqueue(hunter("h1", 40)))
	require.NoError(t, mm.Enqueue(hunter("h2", 30)))
	require.NoError(t, mm.Enqueue(monster("m_low", 20)))
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "monster skill 20 < required max desired 40, MonsterSkill fails")

	// A strong-enough monster satisfies MonsterSkill (50 >= 40).
	require.NoError(t, mm.Enqueue(monster("m_high", 50)))
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "monster skill 50 >= 40 forms the match")
	assert.Len(t, matches[0].Teams["players"], 2)
	require.Len(t, matches[0].Teams["monster"], 1)
	assert.Equal(t, "m_high", matches[0].Teams["monster"][0].ID)
}

// Example 3: Set team-level requirements and latency limits.
// Covers quantity-based team multiplication (quantity:3), a teams[*] distance
// rule, a distance rule over max/min team sizes (CloseTeamSizes), a collection
// "contains" limit (OverallMedicLimit), and a latency rule — all in one set.
func TestEndToEnd_Example3_TeamLevelAndLatency(t *testing.T) {
	body := `{
	  "name": "three_team_game",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [
	    {"name": "skill", "type": "number", "default": 10},
	    {"name": "character", "type": "string_list", "default": ["peasant"]}
	  ],
	  "teams": [{"name": "trio", "minPlayers": 2, "maxPlayers": 2, "quantity": 3}],
	  "rules": [
	    {"name": "FairTeamSkill", "type": "distance",
	     "measurements": ["avg(teams[*].players.attributes[skill])"],
	     "referenceValue": "avg(flatten(teams[*].players.attributes[skill]))",
	     "maxDistance": 10},
	    {"name": "CloseTeamSizes", "type": "distance",
	     "measurements": ["max(count(teams[*].players))"],
	     "referenceValue": "min(count(teams[*].players))",
	     "maxDistance": 1},
	    {"name": "OverallMedicLimit", "type": "collection",
	     "measurements": ["flatten(teams[*].players.attributes[character])"],
	     "operation": "contains", "referenceValue": "medic", "maxCount": 5},
	    {"name": "FastConnection", "type": "latency", "maxLatency": 50}
	  ]
	}`
	region := map[string]int{"us-east-1": 30}

	// Six medics flatten to six "medic" characters, exceeding maxCount 5.
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		require.NoError(t, mm.Enqueue(soloAttrLat(id, flexi.Attributes{
			"skill": flexi.Number(50), "character": flexi.StringList("medic"),
		}, region)))
	}
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "six medics exceed the OverallMedicLimit maxCount of 5")

	// Six peasants form three balanced teams of two within the latency limit.
	mm, err = flexi.New([]byte(body))
	require.NoError(t, err)
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		require.NoError(t, mm.Enqueue(soloAttrLat(id, flexi.Attributes{
			"skill": flexi.Number(50), "character": flexi.StringList("peasant"),
		}, region)))
	}
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1)
	for _, name := range []string{"trio_1", "trio_2", "trio_3"} {
		assert.Len(t, matches[0].Teams[name], 2, "quantity:3 expands to %s", name)
	}
}

// Example 4: Use explicit sorting to find best matches.
// Covers an absoluteSort rule over a string_number_map attribute with
// mapKey:maxValue, a distanceSort rule, and two collection "intersection"
// rules. The sort rules order the batch; the intersection rules gate the match.
func TestEndToEnd_Example4_ExplicitSorting(t *testing.T) {
	body := `{
	  "name": "multi_map_game",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [
	    {"name": "experience", "type": "number", "default": 50},
	    {"name": "gameMode", "type": "string_list", "default": ["deathmatch", "coop"]},
	    {"name": "mapPreference", "type": "string_number_map", "default": {"defaultMap": 100}},
	    {"name": "acceptableMaps", "type": "string_list", "default": ["defaultMap"]}
	  ],
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ],
	  "rules": [
	    {"name": "MapPreference", "type": "absoluteSort",
	     "sortDirection": "descending", "sortAttribute": "mapPreference", "mapKey": "maxValue"},
	    {"name": "ExperienceAffinity", "type": "distanceSort",
	     "sortDirection": "ascending", "sortAttribute": "experience"},
	    {"name": "SharedMode", "type": "collection", "operation": "intersection",
	     "measurements": ["flatten(teams[*].players.attributes[gameMode])"], "minCount": 1},
	    {"name": "MapOverlap", "type": "collection", "operation": "intersection",
	     "measurements": ["flatten(teams[*].players.attributes[acceptableMaps])"], "minCount": 1}
	  ]
	}`
	player := func(id string, exp float64, mode, mapName string) flexi.Ticket {
		return soloAttr(id, flexi.Attributes{
			"experience":     flexi.Number(exp),
			"gameMode":       flexi.StringList(mode),
			"mapPreference":  flexi.StringNumberMap(map[string]float64{mapName: 100, "alt": 10}),
			"acceptableMaps": flexi.StringList(mapName),
		})
	}

	// One player shares no game mode with the others, so SharedMode's
	// intersection is empty and the only possible 2v2 (all four) cannot form.
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(player("a", 10, "coop", "dust")))
	require.NoError(t, mm.Enqueue(player("b", 20, "coop", "dust")))
	require.NoError(t, mm.Enqueue(player("c", 30, "coop", "dust")))
	require.NoError(t, mm.Enqueue(player("d", 40, "ranked", "dust")))
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "no game mode shared by all four players")

	// A fourth coop player lets the four coop/dust players form a match while
	// the absoluteSort/distanceSort rules order the batch.
	require.NoError(t, mm.Enqueue(player("e", 25, "coop", "dust")))
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "four players sharing coop and dust form a match")
}

// Example 5: Find intersections across multiple player attributes.
// Covers a collection reference_intersection_count rule whose referenceValue is
// a set_intersection(...) property expression (the "preferred opponents" form).
func TestEndToEnd_Example5_ReferenceIntersection(t *testing.T) {
	body := `{
	  "name": "preferred_characters",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [
	    {"name": "myCharacter", "type": "string_list"},
	    {"name": "preferredOpponents", "type": "string_list"}
	  ],
	  "teams": [{"name": "red", "minPlayers": 2, "maxPlayers": 2}],
	  "rules": [{
	    "name": "OpponentMatch", "type": "collection",
	    "operation": "reference_intersection_count",
	    "measurements": ["flatten(teams[*].players.attributes[myCharacter])"],
	    "referenceValue": "set_intersection(flatten(teams[*].players.attributes[preferredOpponents]))",
	    "minCount": 1
	  }]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(soloAttr("a", flexi.Attributes{
		"myCharacter": flexi.StringList("knight"), "preferredOpponents": flexi.StringList("mage", "knight")})))
	require.NoError(t, mm.Enqueue(soloAttr("b", flexi.Attributes{
		"myCharacter": flexi.StringList("rogue"), "preferredOpponents": flexi.StringList("mage", "knight")})))
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "rogue is not in the common preferred-opponents set")

	require.NoError(t, mm.Enqueue(soloAttr("c", flexi.Attributes{
		"myCharacter": flexi.StringList("mage"), "preferredOpponents": flexi.StringList("mage", "knight")})))
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "knight and mage are both in the common preferred-opponents set")
}

// Example 6: Compare attributes across all players.
// Covers comparison rules in the "compare across players" form (no
// referenceValue), with both the "=" (same map/mode) and "!=" (distinct
// character) operations.
func TestEndToEnd_Example6_CompareAcrossPlayers(t *testing.T) {
	body := `{
	  "name": "compare_across",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [
	    {"name": "gameMode", "type": "string", "default": "turn-based"},
	    {"name": "gameMap", "type": "number", "default": 1},
	    {"name": "character", "type": "number"}
	  ],
	  "teams": [{"name": "player", "minPlayers": 1, "maxPlayers": 1, "quantity": 2}],
	  "rules": [
	    {"name": "SameGameMode", "type": "comparison", "operation": "=",
	     "measurements": ["flatten(teams[*].players.attributes[gameMode])"]},
	    {"name": "SameGameMap", "type": "comparison", "operation": "=",
	     "measurements": ["flatten(teams[*].players.attributes[gameMap])"]},
	    {"name": "DifferentCharacter", "type": "comparison", "operation": "!=",
	     "measurements": ["flatten(teams[*].players.attributes[character])"]}
	  ]
	}`
	player := func(id string, mode string, gmap, char float64) flexi.Ticket {
		return soloAttr(id, flexi.Attributes{
			"gameMode": flexi.String(mode), "gameMap": flexi.Number(gmap), "character": flexi.Number(char),
		})
	}

	// Same mode and map but the SAME character violates DifferentCharacter.
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(player("a", "coop", 1, 7)))
	require.NoError(t, mm.Enqueue(player("b", "coop", 1, 7)))
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "identical characters violate DifferentCharacter (!=)")

	// A distinct character with the same mode/map satisfies all three rules.
	require.NoError(t, mm.Enqueue(player("c", "coop", 1, 9)))
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "same mode and map with distinct characters matches")
}

// Example 7: Create a large match.
// Covers a large match (single team exceeding 40 players) with the balanced
// strategy and largestPopulation batching preference, gated by a latency rule.
func TestEndToEnd_Example7_LargeMatch(t *testing.T) {
	body := `{
	  "name": "free-for-all",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name": "skill", "type": "number"}],
	  "algorithm": {
	    "balancedAttribute": "skill",
	    "strategy": "balanced",
	    "batchingPreference": "largestPopulation"
	  },
	  "teams": [{"name": "Marauders", "minPlayers": 41, "maxPlayers": 50}],
	  "rules": [{"name": "low-latency", "type": "latency", "maxLatency": 150}]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	region := map[string]int{"us-east-1": 50}
	for i := 0; i < 45; i++ {
		id := "p" + strconv.Itoa(i)
		require.NoError(t, mm.Enqueue(soloAttrLat(id, flexi.Attributes{"skill": flexi.Number(float64(i))}, region)))
	}
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "45 players exceed 40 and form one large match")
	assert.Len(t, matches[0].Teams["Marauders"], 45)
}

// Example 8: Create a multi-team large match.
// Covers quantity-based team multiplication at large-match scale (10 teams) with
// the balanced strategy and fastestRegion batching preference plus a latency
// rule. Confirms players spread evenly across the ten generated team slots.
func TestEndToEnd_Example8_MultiTeamLargeMatch(t *testing.T) {
	body := `{
	  "name": "monster-hunters",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name": "monster-kills", "type": "number", "default": 5}],
	  "algorithm": {
	    "balancedAttribute": "monster-kills",
	    "strategy": "balanced",
	    "batchingPreference": "fastestRegion"
	  },
	  "teams": [{"name": "Hunters", "minPlayers": 5, "maxPlayers": 5, "quantity": 10}],
	  "rules": [{"name": "latency-catchall", "type": "latency", "maxLatency": 200}]
	}`
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	region := map[string]int{"eu-west-1": 80}
	for i := 0; i < 50; i++ {
		id := "p" + strconv.Itoa(i)
		require.NoError(t, mm.Enqueue(soloAttrLat(id, flexi.Attributes{"monster-kills": flexi.Number(5)}, region)))
	}
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "50 players fill ten teams of five")
	for i := 1; i <= 10; i++ {
		name := "Hunters_" + strconv.Itoa(i)
		assert.Len(t, matches[0].Teams[name], 5, "quantity:10 expands to %s", name)
	}
}

// Example 9: Create a large match with players with similar attributes.
// Covers multiple batchDistance rules (numeric league/skill plus string
// map/mode) together with an expansion that relaxes a batchDistance maxDistance
// over time.
func TestEndToEnd_Example9_BatchDistanceWithExpansion(t *testing.T) {
	body := `{
	  "name": "batch-similar",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [
	    {"name": "league", "type": "number"},
	    {"name": "skill", "type": "number"},
	    {"name": "map", "type": "string"},
	    {"name": "mode", "type": "string"}
	  ],
	  "algorithm": {"strategy": "balanced", "balancedAttribute": "skill", "batchingPreference": "fastestRegion"},
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ],
	  "rules": [
	    {"name": "SimilarLeague", "type": "batchDistance", "batchAttribute": "league", "maxDistance": 2},
	    {"name": "SimilarSkill", "type": "batchDistance", "batchAttribute": "skill", "maxDistance": 10},
	    {"name": "SameMap", "type": "batchDistance", "batchAttribute": "map"},
	    {"name": "SameMode", "type": "batchDistance", "batchAttribute": "mode"}
	  ],
	  "expansions": [{
	    "target": "rules[SimilarSkill].maxDistance",
	    "steps": [{"waitTimeSeconds": 10, "value": 20}]
	  }]
	}`
	start := time.Unix(0, 0)
	clock := flexi.NewFakeClock(start)
	mm, err := flexi.New([]byte(body), flexi.WithClock(clock))
	require.NoError(t, err)

	player := func(id string, skill float64) flexi.Ticket {
		return soloAttr(id, flexi.Attributes{
			"league": flexi.Number(1), "skill": flexi.Number(skill),
			"map": flexi.String("dust"), "mode": flexi.String("ranked"),
		})
	}
	// Skill spread is 18 (28-10): above the initial maxDistance of 10.
	require.NoError(t, mm.Enqueue(player("a", 10)))
	require.NoError(t, mm.Enqueue(player("b", 12)))
	require.NoError(t, mm.Enqueue(player("c", 14)))
	require.NoError(t, mm.Enqueue(player("d", 28)))
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "skill spread 18 exceeds the initial batchDistance of 10")

	// After 11 seconds the expansion relaxes SimilarSkill.maxDistance to 20.
	clock.Advance(11 * time.Second)
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "expansion to maxDistance 20 admits the spread of 18")
}

// Example 11: Create a rule that uses a player's block list.
// Covers a collection reference_intersection_count rule with maxCount:0 whose
// referenceValue is the property expression flatten(teams[*].players[playerId]):
// a proposed match is rejected if any selected player's block list names another
// selected player.
func TestEndToEnd_Example11_BlockList(t *testing.T) {
	body := `{
	  "name": "player_block_list",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name": "BlockList", "type": "string_list", "default": []}],
	  "teams": [
	    {"name": "red",  "minPlayers": 2, "maxPlayers": 2},
	    {"name": "blue", "minPlayers": 2, "maxPlayers": 2}
	  ],
	  "rules": [{
	    "name": "PlayerIdNotInBlockList", "type": "collection",
	    "operation": "reference_intersection_count",
	    "measurements": ["flatten(teams[*].players.attributes[BlockList])"],
	    "referenceValue": "flatten(teams[*].players[playerId])",
	    "maxCount": 0
	  }]
	}`
	blocker := func(id string, blocked ...string) flexi.Ticket {
		return soloAttr(id, flexi.Attributes{"BlockList": flexi.StringList(blocked...)})
	}

	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	// "a" blocks "c"; the only possible 2v2 from {a,b,c,d} includes both.
	require.NoError(t, mm.Enqueue(blocker("a", "c")))
	require.NoError(t, mm.Enqueue(blocker("b")))
	require.NoError(t, mm.Enqueue(blocker("c")))
	require.NoError(t, mm.Enqueue(blocker("d")))
	matches, err := mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "a match containing both a and the player a blocked is rejected")

	// A fifth, unblocked player lets a match form that excludes "c".
	require.NoError(t, mm.Enqueue(blocker("e")))
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "a block-free foursome forms a match")
	for _, team := range matches[0].Teams {
		for _, p := range team {
			assert.NotEqual(t, "c", p.ID, "the blocked player must be excluded")
		}
	}
}

// Example 10: Use a compound rule to match players with similar attributes OR
// similar selections.
// Covers a compound rule with a nested or(and(...), and(...)) statement. The
// match should form when EITHER (same map AND same mode) OR (close skill AND
// close league) holds — the four base rules are combined by the compound, not
// required individually.
func TestEndToEnd_Example10_CompoundOr(t *testing.T) {
	// The four base rules are referenced only by CompoundRuleMatchmaker, so flexi
	// evaluates them solely inside that compound (never standalone, matching AWS
	// FlexMatch). The match forms when EITHER branch of the or() holds — a pair
	// sharing map+mode but with far-apart skill still matches via the first
	// branch, which would be impossible if the base rules were enforced on their
	// own.
	body := `{
	  "name": "compound_similar",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [
	    {"name": "league", "type": "number"},
	    {"name": "skill", "type": "number"},
	    {"name": "map", "type": "string"},
	    {"name": "mode", "type": "string"}
	  ],
	  "algorithm": {"strategy": "balanced", "balancedAttribute": "skill", "batchingPreference": "fastestRegion"},
	  "teams": [
	    {"name": "red",  "minPlayers": 1, "maxPlayers": 1},
	    {"name": "blue", "minPlayers": 1, "maxPlayers": 1}
	  ],
	  "rules": [
	    {"name": "SimilarLeagueDistance", "type": "distance",
	     "measurements": ["max(flatten(teams[*].players.attributes[league]))"],
	     "referenceValue": "min(flatten(teams[*].players.attributes[league]))", "maxDistance": 2},
	    {"name": "SimilarSkillDistance", "type": "distance",
	     "measurements": ["max(flatten(teams[*].players.attributes[skill]))"],
	     "referenceValue": "min(flatten(teams[*].players.attributes[skill]))", "maxDistance": 10},
	    {"name": "SameMapComparison", "type": "comparison", "operation": "=",
	     "measurements": ["flatten(teams[*].players.attributes[map])"]},
	    {"name": "SameModeComparison", "type": "comparison", "operation": "=",
	     "measurements": ["flatten(teams[*].players.attributes[mode])"]},
	    {"name": "CompoundRuleMatchmaker", "type": "compound",
	     "statement": "or(and(SameMapComparison, SameModeComparison), and(SimilarSkillDistance, SimilarLeagueDistance))"}
	  ]
	}`
	player := func(id string, league, skill float64, gmap, mode string) flexi.Ticket {
		return soloAttr(id, flexi.Attributes{
			"league": flexi.Number(league), "skill": flexi.Number(skill),
			"map": flexi.String(gmap), "mode": flexi.String(mode),
		})
	}

	// Branch 1: same map + mode, but far-apart skill/league.
	mm, err := flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(player("a", 1, 10, "dust", "ranked")))
	require.NoError(t, mm.Enqueue(player("b", 50, 80, "dust", "ranked")))
	matches, err := mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "same map+mode satisfies the first OR branch")

	// Branch 2: different map/mode, but close skill + league.
	mm, err = flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(player("a", 1, 10, "dust", "ranked")))
	require.NoError(t, mm.Enqueue(player("b", 1, 12, "forge", "coop")))
	matches, err = mm.Tick()
	require.NoError(t, err)
	require.Len(t, matches, 1, "close skill+league satisfies the second OR branch")

	// Neither branch: different map/mode AND far-apart skill/league.
	mm, err = flexi.New([]byte(body))
	require.NoError(t, err)
	require.NoError(t, mm.Enqueue(player("a", 1, 10, "dust", "ranked")))
	require.NoError(t, mm.Enqueue(player("b", 50, 80, "forge", "coop")))
	matches, err = mm.Tick()
	require.NoError(t, err)
	assert.Empty(t, matches, "neither OR branch holds, so no match forms")
}
