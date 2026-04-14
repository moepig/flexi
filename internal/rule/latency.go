package rule

import (
	"fmt"

	"github.com/moepig/flexi/internal/ruleset"
)

type latency struct {
	name       string
	maxLatency int
}

func buildLatency(r *ruleset.Rule) (Evaluator, error) {
	if r.MaxLatency == nil {
		return nil, fmt.Errorf("latency %q: maxLatency required", r.Name)
	}
	return &latency{name: r.Name, maxLatency: *r.MaxLatency}, nil
}

func (l *latency) Name() string { return l.name }

// Evaluate passes when there is at least one region in which every player's
// reported latency is below maxLatency. If c.Region is set the check is
// scoped to that region.
func (l *latency) Evaluate(c *Candidate) (bool, error) {
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
	for _, p := range c.Players {
		v, ok := p.Latencies[region]
		if !ok {
			return false
		}
		if v > l.maxLatency {
			return false
		}
	}
	return true
}
