// Package rule converts parsed ruleset.Rule entries into evaluators that
// answer "does this candidate match satisfy me?".
package rule

import (
	"encoding/json"
	"fmt"

	"github.com/moepig/flexi/internal/core"
	"github.com/moepig/flexi/internal/expr"
	"github.com/moepig/flexi/internal/ruleset"
)

// Candidate is a tentative match: the full player roster grouped by team.
// Region, when set, names the region the latency rule should evaluate against.
type Candidate struct {
	Players []core.Player
	Teams   map[string][]core.Player
	Region  string
}

func (c *Candidate) evalContext() *expr.EvalContext {
	return &expr.EvalContext{Players: c.Players, TeamPlayers: c.Teams}
}

// Evaluator answers whether a Candidate passes the rule. Errors are surfaced
// for misconfiguration; a regular "doesn't match" returns (false, nil).
type Evaluator interface {
	Name() string
	Evaluate(c *Candidate) (bool, error)
}

// Build constructs an Evaluator from a parsed rule. The compounds map allows
// compound rules to reference previously built siblings by name.
func Build(r *ruleset.Rule, compounds map[string]Evaluator) (Evaluator, error) {
	switch r.Type {
	case ruleset.RuleComparison:
		return buildComparison(r)
	case ruleset.RuleDistance:
		return buildDistance(r)
	case ruleset.RuleAbsoluteSort:
		return alwaysPass{name: r.Name}, nil
	case ruleset.RuleBatchDistance:
		return buildBatchDistance(r)
	case ruleset.RuleCollection:
		return buildCollection(r)
	case ruleset.RuleLatency:
		return buildLatency(r)
	case ruleset.RuleCompound:
		return buildCompound(r, compounds)
	}
	return nil, fmt.Errorf("rule: unknown rule type %q", r.Type)
}

// alwaysPass is used for rule kinds that affect ordering but not admission
// (currently absoluteSort).
type alwaysPass struct{ name string }

func (a alwaysPass) Name() string                          { return a.name }
func (alwaysPass) Evaluate(*Candidate) (bool, error)       { return true, nil }

// parseRefAsExpr turns the rule's referenceValue (json.RawMessage) into a
// parsed expression. Strings that fail to parse as expressions are returned
// as string literals; arrays become StringList literal values handled by
// callers that look at parsedRef.RawList.
type parsedRef struct {
	Node    expr.Node // for scalar / expression refs
	IsList  bool
	RawList []json.RawMessage
}

func parseRef(raw json.RawMessage) (parsedRef, error) {
	if len(raw) == 0 {
		return parsedRef{}, nil
	}
	switch raw[0] {
	case '[':
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return parsedRef{}, err
		}
		return parsedRef{IsList: true, RawList: list}, nil
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return parsedRef{}, err
		}
		if n, err := expr.Parse(s); err == nil {
			return parsedRef{Node: n}, nil
		}
		return parsedRef{Node: expr.StringLit{V: s}}, nil
	default:
		var f float64
		if err := json.Unmarshal(raw, &f); err == nil {
			return parsedRef{Node: expr.NumberLit{V: f}}, nil
		}
		return parsedRef{}, fmt.Errorf("rule: unsupported referenceValue %s", string(raw))
	}
}

func parseMeasurements(ms []string) ([]expr.Node, error) {
	out := make([]expr.Node, len(ms))
	for i, m := range ms {
		n, err := expr.Parse(m)
		if err != nil {
			return nil, fmt.Errorf("measurements[%d]: %w", i, err)
		}
		out[i] = n
	}
	return out, nil
}
