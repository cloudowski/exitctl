package scoring

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/graph"
)

// Anchor is a scored node ranked by contribution (data-model.md 4.5).
type Anchor struct {
	Rank            int
	Node            string
	Contribution    float64
	DependentsCount int
	ComponentsUsing int // distinct components with usage edges to the node
	SDKFiles        int // distinct (repo, path) evidence files of those usage edges
	Alternatives    []catalog.AlternativeMapping
	Reason          string
}

// maxReasonTraits caps how many trait names appear in a reason sentence.
const maxReasonTraits = 2

// Anchors returns every node of S with contribution > 0, sorted by
// contribution descending then ID ascending, ranked from 1. ix may be nil
// (then alternatives are empty and trait IDs are used instead of names).
func Anchors(g *graph.Graph, a *Analysis, ix *catalog.Index) []Anchor {
	if a == nil {
		return nil
	}
	// Usage edges into each node, from component nodes only.
	usage := map[string][]*graph.Edge{}
	for _, e := range g.Edges {
		if e.Kind != graph.EdgeUsage {
			continue
		}
		from := g.Node(e.From)
		if from == nil || from.Kind != graph.NodeComponent {
			continue
		}
		to := e.To
		if n := g.Node(e.To); n != nil {
			to = n.ID
		}
		usage[to] = append(usage[to], e)
	}

	var out []Anchor
	for _, id := range a.Scored {
		ns := a.Nodes[id]
		if ns == nil || ns.Contribution <= 0 {
			continue
		}
		n := g.Node(id)
		comps := map[string]bool{}
		files := map[string]bool{}
		for _, e := range usage[id] {
			comps[g.Node(e.From).ID] = true
			for _, ev := range e.Evidence {
				files[ev.Repo+"\x00"+ev.Path] = true
			}
		}
		an := Anchor{
			Node:            id,
			Contribution:    ns.Contribution,
			DependentsCount: ns.Dependents,
			ComponentsUsing: len(comps),
			SDKFiles:        len(files),
		}
		if ix != nil && n != nil && n.Service != "" {
			an.Alternatives = ix.Alternatives(n.Service)
		}
		an.Reason = reason(n, ix, an.SDKFiles, an.DependentsCount)
		out = append(out, an)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Contribution != out[j].Contribution {
			return out[i].Contribution > out[j].Contribution
		}
		return out[i].Node < out[j].Node
	})
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

// reason builds a factual sentence: the names of the highest-level active
// findings (at most maxReasonTraits, level desc then name asc), then the SDK
// file count and dependents count when non-zero, joined by " + ".
func reason(n *graph.Node, ix *catalog.Index, sdkFiles, dependents int) string {
	type tn struct {
		name string
		rank int
	}
	var traits []tn
	seen := map[string]bool{}
	if n != nil {
		for _, f := range n.Findings {
			if f.Status != graph.FindingActive || seen[f.Trait] {
				continue
			}
			seen[f.Trait] = true
			name := f.Trait
			if ix != nil {
				if t := ix.Trait(f.Trait); t != nil && t.Name != "" {
					name = t.Name
				}
			}
			traits = append(traits, tn{name, catalog.LevelRank(f.Level)})
		}
	}
	sort.Slice(traits, func(i, j int) bool {
		if traits[i].rank != traits[j].rank {
			return traits[i].rank > traits[j].rank
		}
		return traits[i].name < traits[j].name
	})
	var parts []string
	for i, t := range traits {
		if i == maxReasonTraits {
			break
		}
		parts = append(parts, t.name)
	}
	if sdkFiles > 0 {
		parts = append(parts, plural(sdkFiles, "SDK file", "SDK files"))
	}
	if dependents > 0 {
		parts = append(parts, plural(dependents, "dependent resource", "dependent resources"))
	}
	return strings.Join(parts, " + ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
