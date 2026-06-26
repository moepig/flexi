package rule

import (
	"fmt"

	"github.com/moepig/flexi/internal/ruleset"
)

type latency struct {
	name        string
	maxLatency  int
	maxDistance *float64
	distanceRef string // "", "min", "avg"
	partyAgg    string
}

func buildLatency(r *ruleset.Rule) (Evaluator, error) {
	if r.MaxLatency == nil {
		return nil, fmt.Errorf("latency %q: maxLatency required", r.Name)
	}
	return &latency{
		name:        r.Name,
		maxLatency:  *r.MaxLatency,
		maxDistance: r.MaxDistance,
		distanceRef: r.DistanceReference,
		partyAgg:    r.PartyAggregation,
	}, nil
}

func (l *latency) Name() string { return l.name }

// Evaluate passes when there is at least one region in which every player's
// reported latency is below maxLatency (and, if distanceReference is set, within
// maxDistance of the per-region reference latency). If c.Region is set the check
// is scoped to that region.
func (l *latency) Evaluate(c *Candidate) (bool, error) {
	if l.partyAgg != "" {
		c = aggregateCandidate(c, l.partyAgg)
	}
	if len(c.Players) == 0 {
		return true, nil
	}
	if c.Region != "" {
		return l.checkRegion(c, c.Region), nil
	}
	regions := make(map[string]struct{})
	for _, p := range c.Players {
		for r := range p.Latencies {
			regions[r] = struct{}{}
		}
	}
	for r := range regions {
		if l.checkRegion(c, r) {
			return true, nil
		}
	}
	return false, nil
}

func (l *latency) checkRegion(c *Candidate, region string) bool {
	vals := make([]float64, 0, len(c.Players))
	for _, p := range c.Players {
		v, ok := p.Latencies[region]
		if !ok {
			return false
		}
		if v > l.maxLatency {
			return false
		}
		vals = append(vals, float64(v))
	}
	if l.distanceRef == "" || l.maxDistance == nil {
		return true
	}
	ref := reduceFloat(vals, l.distanceRef) // "min" or "avg"
	for _, v := range vals {
		if v-ref > *l.maxDistance || ref-v > *l.maxDistance {
			return false
		}
	}
	return true
}
