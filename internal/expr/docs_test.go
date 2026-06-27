package expr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file pins the property-expression parser to the expressions printed
// verbatim in the AWS GameLift FlexMatch documentation, so the examples a user
// would copy out of the docs keep parsing:
//
//	https://docs.aws.amazon.com/gameliftservers/latest/flexmatchguide/match-rules-reference-property-expression.html

// Purpose: Verify every property expression printed in the FlexMatch
// property-expression documentation parses with Parse.
// Method:  Parse each expression copied verbatim from the doc tables (the
//
//	"common", "examples", and "aggregations" sections).
//
// Expect:  Parse returns no error and a non-nil node for each.
func TestDocs_PropertyExpressionsParse(t *testing.T) {
	exprs := []string{
		// "Property expression examples" table.
		"teams[red].players[playerId]",
		"teams[red].players.attributes[skill]",
		"teams[red,blue].players.attributes[skill]",
		"teams[*].players.attributes[skill]",

		// "Property expression aggregations" table.
		"flatten(teams[*].players.attributes[skill])",
		"avg(teams[red].players.attributes[skill])",
		"avg(teams[*].players.attributes[skill])",
		"avg(flatten(teams[*].players.attributes[skill]))",
		"count(teams[red].players)",
		"count(teams[*].players)",
		"max(avg(teams[*].players.attributes[skill]))",
	}
	for _, src := range exprs {
		t.Run(src, func(t *testing.T) {
			n, err := Parse(src)
			require.NoError(t, err, "documented property expression failed to parse")
			assert.NotNil(t, n)
		})
	}
}
