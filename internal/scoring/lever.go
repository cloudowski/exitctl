package scoring

import (
	"sort"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/config"
	"github.com/cloudowski/exitctl/internal/graph"
)

// Lever is the single service replacement that lowers the index the most
// (FR-039, data-model.md 4.5).
type Lever struct {
	Service     string
	Alternative string // alternative ID
	Difficulty  string
	Delta       int // index points gained, >= 1
}

// PreferredAlternative returns the mapping used for the lever: the
// recommended one, else the lowest difficulty, then name (the index already
// orders alternatives this way, so it is the first one).
func PreferredAlternative(ix *catalog.Index, serviceID string) (catalog.AlternativeMapping, bool) {
	alts := ix.Alternatives(serviceID)
	if len(alts) == 0 {
		return catalog.AlternativeMapping{}, false
	}
	best := alts[0]
	if !best.Recommended {
		for _, a := range alts[1:] {
			if catalog.DifficultyRank(a.Difficulty) < catalog.DifficultyRank(best.Difficulty) {
				best = a
			}
		}
	}
	return best, true
}

// FindLever simulates replacing every service present in the scored set by
// its preferred alternative and returns the best one, or nil when no
// replacement lowers the index. The graph is unchanged during simulation:
// only the findings of the replaced service's nodes change to the
// alternative's residual traits.
func FindLever(g *graph.Graph, a *Analysis, dependents func(string) int, cfg config.Scoring, ix *catalog.Index) *Lever {
	if a == nil || a.Status != StatusOK || ix == nil {
		return nil
	}
	services := map[string]bool{}
	for _, id := range a.Scored {
		if n := g.Node(id); n != nil && n.Service != "" {
			services[n.Service] = true
		}
	}
	ids := make([]string, 0, len(services))
	for s := range services {
		ids = append(ids, s)
	}
	sort.Strings(ids)

	var best *Lever
	for _, svc := range ids {
		alt, ok := PreferredAlternative(ix, svc)
		if !ok {
			continue
		}
		replace := func(n *graph.Node) []graph.Finding {
			if n.Service != svc {
				return n.Findings
			}
			return residualFindings(n, alt, ix)
		}
		idx, ok := IndexWith(g, dependents, cfg, replace)
		if !ok {
			continue
		}
		delta := a.Index - idx
		if delta <= 0 {
			continue
		}
		cand := &Lever{Service: svc, Alternative: alt.Ref, Difficulty: alt.Difficulty, Delta: delta}
		if best == nil || better(cand, best) {
			best = cand
		}
	}
	return best
}

func better(a, b *Lever) bool {
	if a.Delta != b.Delta {
		return a.Delta > b.Delta
	}
	if da, db := catalog.DifficultyRank(a.Difficulty), catalog.DifficultyRank(b.Difficulty); da != db {
		return da < db
	}
	return a.Service < b.Service
}

// residualFindings builds the findings a node would keep after switching to
// the alternative: one per residual trait, evidence = the declaration.
// Accepted findings stay accepted (they were not counted anyway).
func residualFindings(n *graph.Node, alt catalog.AlternativeMapping, ix *catalog.Index) []graph.Finding {
	var out []graph.Finding
	for _, f := range n.Findings {
		if f.Status != graph.FindingActive {
			out = append(out, f)
		}
	}
	ev := n.Evidence
	if len(ev) == 0 && len(n.Findings) > 0 {
		ev = n.Findings[0].Evidence
	}
	for _, rt := range alt.ResidualTraits {
		t := ix.Trait(rt.ID)
		if t == nil {
			continue
		}
		lvl := t.Level
		if rt.Level != "" {
			lvl = rt.Level
		}
		out = append(out, graph.Finding{
			ID: n.ID + "::" + rt.ID, Trait: rt.ID, Level: lvl, Dimensions: t.Dimensions,
			Origin: "service", Evidence: ev, Status: graph.FindingActive,
		})
	}
	return out
}
