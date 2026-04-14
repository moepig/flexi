package expr

// Node is a parsed property expression AST node.
type Node interface{ node() }

// NumberLit is a numeric literal.
type NumberLit struct{ V float64 }

// StringLit is a string literal (quoted with single or double quotes).
type StringLit struct{ V string }

// PlayerAccess refers to a player attribute, optionally indexed for SDM types.
// Scope is the team name; "" means "all players" (no team scope), "*" means
// "every team".
type PlayerAccess struct {
	Scope    string // "", team name, or "*"
	Attr     string
	MapKey   string // optional, for string_number_map
	HasIndex bool
}

// FuncCall is a one-argument function such as avg / min / max / count / sum /
// flatten / set_intersection.
type FuncCall struct {
	Name string
	Arg  Node
}

func (NumberLit) node()    {}
func (StringLit) node()    {}
func (PlayerAccess) node() {}
func (FuncCall) node()     {}
