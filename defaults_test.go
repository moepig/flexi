package flexi

import (
	"testing"

	"github.com/moepig/flexi/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Purpose: Verify that playerAttributes defaults are parsed and applied for every
// FlexMatch attribute type (string, number, string_list, string_number_map), not
// just number. A missing attribute on a player must be filled with the declared
// default, preserving the correct Attribute kind and value.
// Method:  declare one attribute of each type with a default, build a Matchmaker,
//
//	apply defaults to a ticket whose single player declares none of them.
//
// Expect:  each attribute is present afterwards with the right kind and value.
func TestApplyDefaults_AllTypes(t *testing.T) {
	body := `{
	  "name": "defaults-all-types",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [
	    {"name": "role",  "type": "string",            "default": "tank"},
	    {"name": "skill", "type": "number",            "default": 50},
	    {"name": "modes", "type": "string_list",       "default": ["TDM", "CTF"]},
	    {"name": "ping",  "type": "string_number_map",  "default": {"us-east-1": 40}}
	  ],
	  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}]
	}`
	mm, err := New([]byte(body))
	require.NoError(t, err)

	out := mm.applyDefaults(Ticket{ID: "t", Players: []Player{{ID: "p"}}})
	require.Len(t, out.Players, 1)
	attrs := out.Players[0].Attributes

	require.Contains(t, attrs, "role")
	assert.Equal(t, core.AttrString, attrs["role"].Kind)
	assert.Equal(t, "tank", attrs["role"].S)

	require.Contains(t, attrs, "skill")
	assert.Equal(t, core.AttrNumber, attrs["skill"].Kind)
	assert.Equal(t, 50.0, attrs["skill"].N)

	require.Contains(t, attrs, "modes")
	assert.Equal(t, core.AttrStringList, attrs["modes"].Kind)
	assert.Equal(t, []string{"TDM", "CTF"}, attrs["modes"].SL)

	require.Contains(t, attrs, "ping")
	assert.Equal(t, core.AttrStringNumberMap, attrs["ping"].Kind)
	assert.Equal(t, map[string]float64{"us-east-1": 40}, attrs["ping"].SDM)
}

// Purpose: Verify defaults never override values a player already supplies, for a
// non-number type (the string_list case).
// Method:  default modes is ["TDM"]; the player supplies modes ["CTF"].
// Expect:  the player's own ["CTF"] is preserved.
func TestApplyDefaults_DoesNotOverrideProvided(t *testing.T) {
	body := `{
	  "name": "defaults-no-override",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [{"name": "modes", "type": "string_list", "default": ["TDM"]}],
	  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}]
	}`
	mm, err := New([]byte(body))
	require.NoError(t, err)

	p := Player{ID: "p", Attributes: Attributes{"modes": StringList("CTF")}}
	out := mm.applyDefaults(Ticket{ID: "t", Players: []Player{p}})
	assert.Equal(t, []string{"CTF"}, out.Players[0].Attributes["modes"].SL)
}

// Purpose: Verify Enqueue rejects a ticket whose player supplies a declared
// attribute with the wrong kind (e.g. a string for a "number" attribute), while
// accepting correctly-typed values and passing undeclared attributes through.
// Method:  rule set declares skill:number and role:string; enqueue a correctly
//
//	typed ticket, a wrongly typed one, and one with an undeclared attribute.
//
// Expect:  the correct and undeclared-attribute tickets enqueue; the mismatch
//
//	returns an error and is not enqueued.
func TestEnqueue_RejectsAttributeTypeMismatch(t *testing.T) {
	body := `{
	  "name": "typed",
	  "ruleLanguageVersion": "1.0",
	  "playerAttributes": [
	    {"name": "skill", "type": "number"},
	    {"name": "role",  "type": "string"}
	  ],
	  "teams": [{"name": "all", "minPlayers": 1, "maxPlayers": 4}]
	}`
	mm, err := New([]byte(body))
	require.NoError(t, err)

	good := Ticket{ID: "ok", Players: []Player{{ID: "p",
		Attributes: Attributes{"skill": Number(50), "role": String("tank")}}}}
	require.NoError(t, mm.Enqueue(good))

	// skill declared as number but supplied as a string.
	bad := Ticket{ID: "bad", Players: []Player{{ID: "p",
		Attributes: Attributes{"skill": String("high")}}}}
	err = mm.Enqueue(bad)
	require.Error(t, err)
	_, statusErr := mm.Status("bad")
	assert.ErrorIs(t, statusErr, ErrUnknownTicket, "rejected ticket is not enqueued")

	// Undeclared attributes are carried through without a type check.
	extra := Ticket{ID: "extra", Players: []Player{{ID: "p",
		Attributes: Attributes{"skill": Number(10), "nickname": String("ace")}}}}
	require.NoError(t, mm.Enqueue(extra))
}
