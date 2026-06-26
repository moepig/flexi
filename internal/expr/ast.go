package expr

// Node is a parsed property expression AST node.
type Node interface{ node() }

// NumberLit is a numeric literal.
type NumberLit struct{ V float64 }

// StringLit is a string literal (quoted with single or double quotes).
type StringLit struct{ V string }

// PlayerAccess refers to player data within a team scope.
//
// Team scoping:
//
//	Teams == nil, AllTeams == false  -> all players in the match, ungrouped
//	AllTeams == true ("*")           -> every team, grouped (List<List<...>>)
//	len(Teams) == 1                  -> one team, ungrouped (List<...>)
//	len(Teams)  > 1                  -> those teams, grouped (List<List<...>>)
//
// Attr selects what to read from each player:
//
//	Attr == ""    -> the player itself (its ID); used by count(...players)
//	                 and teams[red].players[playerId].
//	Attr == name  -> players.attributes[name]. For string_number_map attrs an
//	                 optional [MapKey] index selects a single numeric value.
type PlayerAccess struct {
	Teams    []string // explicit team names (empty = all players)
	AllTeams bool     // teams[*]
	Attr     string   // attribute name; "" means the whole player (ID)
	MapKey   string   // optional, for string_number_map
	HasIndex bool
}

// grouped reports whether the access produces results grouped per team
// (a nested List), which happens for teams[*] and multi-team scopes.
func (pa PlayerAccess) grouped() bool {
	return pa.AllTeams || len(pa.Teams) > 1
}

// FuncCall is a one-argument function such as avg / min / max / sum / count /
// median / stddev / flatten / set_intersection.
type FuncCall struct {
	Name string
	Arg  Node
}

func (NumberLit) node()    {}
func (StringLit) node()    {}
func (PlayerAccess) node() {}
func (FuncCall) node()     {}
