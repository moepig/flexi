package ruleset

import (
	"fmt"
	"strings"
	"unicode"
)

// CompoundNode is a parsed compound-rule statement. A leaf references another
// rule by name (Rule set, Op empty); an internal node combines arguments with a
// logical operator (Op in {and, or, not, xor}).
type CompoundNode struct {
	Op   string
	Rule string
	Args []*CompoundNode
}

// ParseCompound parses a compound statement string such as
// "or(and(A, B), not(C))" into a CompoundNode tree.
func ParseCompound(s string) (*CompoundNode, error) {
	p := &compoundParser{src: strings.TrimSpace(s)}
	n, err := p.parse()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.src) {
		return nil, fmt.Errorf("trailing input %q", p.src[p.pos:])
	}
	return n, nil
}

// RuleNames returns the names of all leaf rules referenced by the statement.
func (n *CompoundNode) RuleNames() []string {
	var out []string
	var walk func(*CompoundNode)
	walk = func(c *CompoundNode) {
		if c.Op == "" {
			out = append(out, c.Rule)
			return
		}
		for _, a := range c.Args {
			walk(a)
		}
	}
	walk(n)
	return out
}

type compoundParser struct {
	src string
	pos int
}

func (p *compoundParser) skipSpace() {
	for p.pos < len(p.src) && unicode.IsSpace(rune(p.src[p.pos])) {
		p.pos++
	}
}

func (p *compoundParser) parse() (*CompoundNode, error) {
	p.skipSpace()
	id := p.readIdent()
	if id == "" {
		return nil, fmt.Errorf("expected operator or rule name at pos %d", p.pos)
	}
	p.skipSpace()
	if p.pos < len(p.src) && p.src[p.pos] == '(' {
		op := id
		switch op {
		case "and", "or", "not", "xor":
		default:
			return nil, fmt.Errorf("unknown operator %q", op)
		}
		p.pos++ // consume '('
		var args []*CompoundNode
		for {
			arg, err := p.parse()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			p.skipSpace()
			if p.pos >= len(p.src) {
				return nil, fmt.Errorf("unterminated %q", op)
			}
			if p.src[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.src[p.pos] == ')' {
				p.pos++
				break
			}
			return nil, fmt.Errorf("expected ',' or ')' at pos %d", p.pos)
		}
		switch op {
		case "not":
			if len(args) != 1 {
				return nil, fmt.Errorf("not requires exactly one argument")
			}
		default:
			if len(args) < 2 {
				return nil, fmt.Errorf("%s requires at least two arguments", op)
			}
		}
		return &CompoundNode{Op: op, Args: args}, nil
	}
	// Leaf rule reference.
	return &CompoundNode{Rule: id}, nil
}

func (p *compoundParser) readIdent() string {
	p.skipSpace()
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			p.pos++
			continue
		}
		break
	}
	return p.src[start:p.pos]
}
