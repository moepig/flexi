package rule

import (
	"encoding/json"
	"fmt"

	"github.com/moepig/flexi/internal/core"
	"github.com/moepig/flexi/internal/expr"
	"github.com/moepig/flexi/internal/ruleset"
)

type collection struct {
	name     string
	measures []expr.Node
	op       string
	ref      parsedRef
	minCount *int
	maxCount *int
	partyAgg string // "", "union", "intersection"
}

func buildCollection(r *ruleset.Rule) (Evaluator, error) {
	ms, err := parseMeasurements(r.Measurements)
	if err != nil {
		return nil, err
	}
	// referenceValue may be a string literal, a JSON array of strings, or a
	// property expression (e.g. set_intersection(flatten(...))). parseRef keeps
	// all three forms; they are resolved per-candidate in Evaluate.
	ref, err := parseRef(r.ReferenceValue)
	if err != nil {
		return nil, err
	}
	return &collection{
		name: r.Name, measures: ms, op: r.Operation, ref: ref,
		minCount: r.MinCount, maxCount: r.MaxCount,
		partyAgg: r.PartyAggregation,
	}, nil
}

func (c *collection) Name() string { return c.name }

func (c *collection) Evaluate(cand *Candidate) (bool, error) {
	// partyAggregation defaults to "union" per the FlexMatch spec.
	mode := c.partyAgg
	if mode == "" {
		mode = "union"
	}
	cand = aggregateCandidateWith(cand, func(party []core.Player) core.Player {
		return aggregatePartySets(party, mode)
	})
	ctx := cand.evalContext()
	for _, m := range c.measures {
		v, err := expr.Eval(m, ctx)
		if err != nil {
			return false, err
		}
		ok, err := c.evalMeasure(v, ctx)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// evalMeasure applies the collection operation to a single measurement value.
//
//   - contains:        count occurrences of the (scalar) reference value across
//     the whole measurement, bounded by minCount/maxCount (e.g. "no more than
//     5 medics"). With no bounds the value must be present at least once.
//   - not_contains:    a flexi extension; passes only when the reference value
//     is absent from the measurement.
//   - intersection:    the number of values shared by every player's collection
//     (the measurement is a list of per-player string lists). No referenceValue.
//   - reference_intersection_count: every per-player string list must intersect
//     the reference value collection within minCount/maxCount.
func (c *collection) evalMeasure(v expr.Value, ctx *expr.EvalContext) (bool, error) {
	switch c.op {
	case "contains":
		ref, err := c.refScalar(ctx)
		if err != nil {
			return false, err
		}
		flat, ok := v.FlattenStrings()
		if !ok {
			return false, fmt.Errorf("collection %q: measurement is not a string set", c.name)
		}
		return c.withinBounds(countOccurrences(flat, ref)), nil

	case "not_contains":
		ref, err := c.refScalar(ctx)
		if err != nil {
			return false, err
		}
		flat, ok := v.FlattenStrings()
		if !ok {
			return false, fmt.Errorf("collection %q: measurement is not a string set", c.name)
		}
		return countOccurrences(flat, ref) == 0, nil

	case "intersection":
		sets, err := perPlayerSets(v)
		if err != nil {
			return false, fmt.Errorf("collection %q: %v", c.name, err)
		}
		return c.withinBounds(len(intersectAll(sets))), nil

	case "reference_intersection_count":
		refSet, err := c.refStrings(ctx)
		if err != nil {
			return false, err
		}
		sets, err := perPlayerSets(v)
		if err != nil {
			return false, fmt.Errorf("collection %q: %v", c.name, err)
		}
		for _, s := range sets {
			n := intersectCount(s, refSet)
			if c.minCount != nil && n < *c.minCount {
				return false, nil
			}
			if c.maxCount != nil && n > *c.maxCount {
				return false, nil
			}
		}
		return true, nil
	}
	return false, fmt.Errorf("collection %q: unknown operation %q", c.name, c.op)
}

// withinBounds reports whether a count satisfies the rule's minCount/maxCount.
// When neither bound is set the count must be positive (the value is present /
// there is a non-empty intersection).
func (c *collection) withinBounds(n int) bool {
	if c.minCount == nil && c.maxCount == nil {
		return n > 0
	}
	if c.minCount != nil && n < *c.minCount {
		return false
	}
	if c.maxCount != nil && n > *c.maxCount {
		return false
	}
	return true
}

// refScalar resolves the reference value to a single string (for contains /
// not_contains). It accepts a string literal or a property expression that
// evaluates to a string.
func (c *collection) refScalar(ctx *expr.EvalContext) (string, error) {
	if c.ref.IsList {
		return "", fmt.Errorf("collection %q: operation %q needs a scalar referenceValue, got a list", c.name, c.op)
	}
	if c.ref.Node == nil {
		return "", fmt.Errorf("collection %q: operation %q requires a referenceValue", c.name, c.op)
	}
	v, err := expr.Eval(c.ref.Node, ctx)
	if err != nil {
		return "", err
	}
	if s, ok := v.AsString(); ok {
		return s, nil
	}
	if ss, ok := v.FlattenStrings(); ok && len(ss) == 1 {
		return ss[0], nil
	}
	return "", fmt.Errorf("collection %q: referenceValue did not resolve to a single string", c.name)
}

// refStrings resolves the reference value to a string list (for
// reference_intersection_count). It accepts a JSON array of strings or a
// property expression that evaluates to a string list.
func (c *collection) refStrings(ctx *expr.EvalContext) ([]string, error) {
	if c.ref.IsList {
		out := make([]string, 0, len(c.ref.RawList))
		for _, raw := range c.ref.RawList {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, fmt.Errorf("collection %q: referenceValue array element: %w", c.name, err)
			}
			out = append(out, s)
		}
		return out, nil
	}
	if c.ref.Node == nil {
		return nil, fmt.Errorf("collection %q: operation %q requires a referenceValue", c.name, c.op)
	}
	v, err := expr.Eval(c.ref.Node, ctx)
	if err != nil {
		return nil, err
	}
	ss, ok := v.FlattenStrings()
	if !ok {
		return nil, fmt.Errorf("collection %q: referenceValue did not resolve to a string list", c.name)
	}
	return ss, nil
}

// perPlayerSets interprets a measurement value as one string list per player.
// A list whose elements are themselves lists (e.g. players.attributes[modes],
// or flatten(teams[*]...) which removes only the team level) yields one set per
// element. A flat list of strings is treated as a single set.
func perPlayerSets(v expr.Value) ([][]string, error) {
	if v.Kind == expr.KindList && allLists(v.List) {
		out := make([][]string, 0, len(v.List))
		for _, e := range v.List {
			s, ok := e.FlattenStrings()
			if !ok {
				return nil, fmt.Errorf("measurement is not a string set")
			}
			out = append(out, s)
		}
		return out, nil
	}
	s, ok := v.FlattenStrings()
	if !ok {
		return nil, fmt.Errorf("measurement is not a string set")
	}
	return [][]string{s}, nil
}

func allLists(vs []expr.Value) bool {
	if len(vs) == 0 {
		return false
	}
	for _, e := range vs {
		if e.Kind != expr.KindList {
			return false
		}
	}
	return true
}

// intersectAll returns the distinct values present in every set, preserving the
// order of the first set.
func intersectAll(sets [][]string) []string {
	if len(sets) == 0 {
		return nil
	}
	common := make(map[string]struct{}, len(sets[0]))
	for _, s := range sets[0] {
		common[s] = struct{}{}
	}
	for _, s := range sets[1:] {
		have := make(map[string]struct{}, len(s))
		for _, x := range s {
			have[x] = struct{}{}
		}
		for k := range common {
			if _, ok := have[k]; !ok {
				delete(common, k)
			}
		}
	}
	out := make([]string, 0, len(common))
	seen := make(map[string]struct{}, len(common))
	for _, s := range sets[0] {
		if _, ok := common[s]; !ok {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func countOccurrences(set []string, v string) int {
	n := 0
	for _, s := range set {
		if s == v {
			n++
		}
	}
	return n
}

func intersectCount(a, b []string) int {
	idx := make(map[string]struct{}, len(b))
	for _, s := range b {
		idx[s] = struct{}{}
	}
	n := 0
	seen := make(map[string]struct{}, len(a))
	for _, s := range a {
		if _, dup := seen[s]; dup {
			continue
		}
		if _, ok := idx[s]; ok {
			seen[s] = struct{}{}
			n++
		}
	}
	return n
}
