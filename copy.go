package flexi

import (
	"maps"
	"slices"

	"github.com/moepig/flexi/internal/core"
)

func cloneAttribute(a core.Attribute) core.Attribute {
	a.SL = slices.Clone(a.SL)
	a.SDM = maps.Clone(a.SDM)
	return a
}

func clonePlayer(p core.Player) core.Player {
	if p.Attributes != nil {
		attrs := make(core.Attributes, len(p.Attributes))
		for name, attr := range p.Attributes {
			attrs[name] = cloneAttribute(attr)
		}
		p.Attributes = attrs
	}
	p.Latencies = maps.Clone(p.Latencies)
	return p
}

func clonePlayers(players []core.Player) []core.Player {
	if players == nil {
		return nil
	}
	out := make([]core.Player, len(players))
	for i, p := range players {
		out[i] = clonePlayer(p)
	}
	return out
}

func cloneTicket(t core.Ticket) core.Ticket {
	t.Players = clonePlayers(t.Players)
	return t
}
