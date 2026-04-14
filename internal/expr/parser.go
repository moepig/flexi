package expr

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Parse parses a FlexMatch property expression. Recognised forms:
//
//	NUMBER                            (e.g. 42, 3.14)
//	"STRING" / 'STRING'
//	players.<attr>
//	players.<attr>[<key>]
//	teams[<name>].players.<attr>
//	teams[*].players.<attr>
//	<func>(<expr>)                    where <func> in {flatten, avg, min, max,
//	                                  count, sum, set_intersection}
func Parse(src string) (Node, error) {
	p := &parser{src: strings.TrimSpace(src)}
	n, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos != len(p.src) {
		return nil, fmt.Errorf("expr: trailing input at %q", p.src[p.pos:])
	}
	return n, nil
}

type parser struct {
	src string
	pos int
}

func (p *parser) skipSpace() {
	for p.pos < len(p.src) && unicode.IsSpace(rune(p.src[p.pos])) {
		p.pos++
	}
}

func (p *parser) peek() byte {
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *parser) eat(b byte) bool {
	p.skipSpace()
	if p.peek() == b {
		p.pos++
		return true
	}
	return false
}

func (p *parser) expect(b byte) error {
	if !p.eat(b) {
		return fmt.Errorf("expr: expected %q at pos %d in %q", b, p.pos, p.src)
	}
	return nil
}

func (p *parser) readIdent() string {
	p.skipSpace()
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			p.pos++
			continue
		}
		break
	}
	return p.src[start:p.pos]
}

func (p *parser) parseExpr() (Node, error) {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return nil, fmt.Errorf("expr: empty expression")
	}
	c := p.peek()

	// String literal.
	if c == '"' || c == '\'' {
		return p.parseString()
	}
	// Number literal.
	if (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.' {
		return p.parseNumber()
	}

	id := p.readIdent()
	if id == "" {
		return nil, fmt.Errorf("expr: unexpected %q at pos %d", c, p.pos)
	}

	switch id {
	case "players":
		return p.parsePlayers("")
	case "teams":
		return p.parseTeams()
	}

	// Function call: id ( expr )
	if p.eat('(') {
		arg, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(')'); err != nil {
			return nil, err
		}
		return FuncCall{Name: id, Arg: arg}, nil
	}

	return nil, fmt.Errorf("expr: unknown identifier %q", id)
}

func (p *parser) parseNumber() (Node, error) {
	start := p.pos
	if p.peek() == '-' || p.peek() == '+' {
		p.pos++
	}
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-' {
			p.pos++
			continue
		}
		break
	}
	v, err := strconv.ParseFloat(p.src[start:p.pos], 64)
	if err != nil {
		return nil, fmt.Errorf("expr: bad number %q", p.src[start:p.pos])
	}
	return NumberLit{V: v}, nil
}

func (p *parser) parseString() (Node, error) {
	quote := p.src[p.pos]
	p.pos++
	start := p.pos
	for p.pos < len(p.src) && p.src[p.pos] != quote {
		p.pos++
	}
	if p.pos >= len(p.src) {
		return nil, fmt.Errorf("expr: unterminated string")
	}
	s := p.src[start:p.pos]
	p.pos++
	return StringLit{V: s}, nil
}

func (p *parser) parsePlayers(scope string) (Node, error) {
	if err := p.expect('.'); err != nil {
		return nil, err
	}
	attr := p.readIdent()
	if attr == "" {
		return nil, fmt.Errorf("expr: expected attribute name after players.")
	}
	pa := PlayerAccess{Scope: scope, Attr: attr}
	if p.eat('[') {
		key := p.readIdent()
		if key == "" {
			// allow string-literal index
			if p.peek() == '"' || p.peek() == '\'' {
				n, err := p.parseString()
				if err != nil {
					return nil, err
				}
				key = n.(StringLit).V
			} else {
				return nil, fmt.Errorf("expr: expected map key")
			}
		}
		if err := p.expect(']'); err != nil {
			return nil, err
		}
		pa.MapKey = key
		pa.HasIndex = true
	}
	return pa, nil
}

func (p *parser) parseTeams() (Node, error) {
	if err := p.expect('['); err != nil {
		return nil, err
	}
	var name string
	if p.eat('*') {
		name = "*"
	} else {
		name = p.readIdent()
		if name == "" {
			return nil, fmt.Errorf("expr: expected team name")
		}
	}
	if err := p.expect(']'); err != nil {
		return nil, err
	}
	if err := p.expect('.'); err != nil {
		return nil, err
	}
	id := p.readIdent()
	if id != "players" {
		return nil, fmt.Errorf("expr: expected .players after teams[..]")
	}
	return p.parsePlayers(name)
}
