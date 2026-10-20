package graphviz

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/cloudowski/exitctl/internal/result"
)

// Options controls the DOT output.
type Options struct {
	// Expand disables type collapse and container grouping (contracts/cli.md
	// "exitctl graph" --expand): every node is drawn on its own, without clusters.
	Expand bool
	// Waves adds the migration wave to node labels.
	Waves bool
}

// Group kinds (data-model.md 3.7).
const (
	groupType      = "type"
	groupModule    = "module"
	groupNamespace = "namespace"
	groupComponent = "component"
)

// isContainer reports whether a group kind is drawn as a cluster.
func isContainer(kind string) bool {
	return kind == groupModule || kind == groupNamespace || kind == groupComponent
}

// band is a risk band: fill color plus the text shown in the label, so color
// is never the only signal.
type band struct {
	name  string
	color string
}

// riskBand maps a 0..1 node risk to a band. Node risk is the worst finding of
// one node, so the thresholds are fixed and independent of the grade bands.
func riskBand(r float64) band {
	v := r * 100
	switch {
	case r <= 0:
		return band{"none", "#f2f2f2"}
	case v < 20:
		return band{"low", "#c8e6c9"}
	case v < 40:
		return band{"moderate", "#e6ee9c"}
	case v < 60:
		return band{"elevated", "#ffe082"}
	case v < 80:
		return band{"high", "#ffb74d"}
	}
	return band{"critical", "#ef9a9a"}
}

// edgeStyle returns the DOT style of an edge kind.
func edgeStyle(kind string) string {
	switch kind {
	case "containment":
		return "dashed"
	case "usage":
		return "dotted"
	case "data-flow":
		return "bold"
	}
	return "solid"
}

// address returns the display address of a node.
func address(n *result.Node) string { return result.DisplayAddress(n) }

// quote returns s as a DOT double-quoted string. Newlines become the DOT
// "\n" line break; backslashes and quotes are escaped so that nothing in the
// input is interpreted as a DOT escape sequence.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// vertex is one drawn DOT node: a graph node or a collapsed type group.
type vertex struct {
	id        string // DOT node ID
	label     string
	risk      float64
	shape     string
	style     string
	container string // innermost container group ID, "" for top level
}

type dotEdge struct {
	from, to, kind string
}

// layout is the grouped view to draw.
type layout struct {
	vertices  []*vertex           // sorted by id
	clusters  map[string][]string // container ID -> child container IDs (sorted)
	roots     []string            // top-level container IDs (sorted)
	members   map[string][]*vertex
	groups    map[string]*result.Group
	edges     []dotEdge
	topLevel  []*vertex
	usedGroup map[string]bool // containers holding at least one vertex, directly or nested
}

// DOT writes the grouped graph of doc as a Graphviz DOT digraph. The output is
// deterministic: nodes, clusters and edges are sorted by ID.
func DOT(w io.Writer, doc *result.Document, opts Options) error {
	l := build(doc, opts)
	bw := bufio.NewWriter(w)
	p := func(indent int, format string, args ...any) {
		_, _ = bw.WriteString(strings.Repeat("  ", indent))
		_, _ = fmt.Fprintf(bw, format, args...)
		_ = bw.WriteByte('\n')
	}
	p(0, "digraph %s {", quote("exitctl"))
	p(1, "rankdir=LR;")
	p(1, `node [shape=box, style="rounded,filled", fontname="Helvetica", fontsize=10];`)
	p(1, `edge [fontname="Helvetica", fontsize=9];`)
	writeVertex := func(indent int, v *vertex) {
		b := riskBand(v.risk)
		p(indent, "%s [label=%s, shape=%s, style=%s, fillcolor=%s];",
			quote(v.id), quote(v.label), v.shape, quote(v.style), quote(b.color))
	}
	for _, v := range l.topLevel {
		writeVertex(1, v)
	}
	var writeCluster func(indent int, id string)
	writeCluster = func(indent int, id string) {
		g := l.groups[id]
		label := g.Label
		if label == "" {
			label = g.ID
		}
		p(indent, "subgraph %s {", quote("cluster_"+g.ID))
		p(indent+1, "label=%s;", quote(g.Kind+": "+label))
		p(indent+1, `style="rounded,dashed";`)
		for _, child := range l.clusters[id] {
			if l.usedGroup[child] {
				writeCluster(indent+1, child)
			}
		}
		for _, v := range l.members[id] {
			writeVertex(indent+1, v)
		}
		p(indent, "}")
	}
	for _, id := range l.roots {
		if l.usedGroup[id] {
			writeCluster(1, id)
		}
	}
	for _, e := range l.edges {
		p(1, "%s -> %s [style=%s, tooltip=%s];", quote(e.from), quote(e.to), edgeStyle(e.kind), quote(e.kind))
	}
	p(0, "}")
	return bw.Flush()
}

// build computes the drawn vertices, cluster tree and re-targeted edges.
func build(doc *result.Document, opts Options) *layout {
	l := &layout{
		clusters:  map[string][]string{},
		members:   map[string][]*vertex{},
		groups:    map[string]*result.Group{},
		usedGroup: map[string]bool{},
	}
	nodes := make([]*result.Node, 0, len(doc.Graph.Nodes))
	for i := range doc.Graph.Nodes {
		n := &doc.Graph.Nodes[i]
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	groups := make([]*result.Group, 0, len(doc.Graph.Groups))
	if !opts.Expand {
		for i := range doc.Graph.Groups {
			groups = append(groups, &doc.Graph.Groups[i])
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	for _, g := range groups {
		l.groups[g.ID] = g
	}

	// containerOf walks the parent chain from group id to the first container.
	containerOf := func(id string) string {
		seen := map[string]bool{}
		for id != "" && !seen[id] {
			seen[id] = true
			g, ok := l.groups[id]
			if !ok {
				return ""
			}
			if isContainer(g.Kind) {
				return id
			}
			id = g.Parent
		}
		return ""
	}
	depth := func(id string) int {
		d := 0
		seen := map[string]bool{}
		for id != "" && !seen[id] {
			seen[id] = true
			g, ok := l.groups[id]
			if !ok {
				break
			}
			d++
			id = g.Parent
		}
		return d
	}

	// Innermost group of each node: Node.Group when set, else derived from
	// group members (a type group wins, then the deepest container).
	memberOf := map[string]string{}
	for _, g := range groups {
		for _, m := range g.Members {
			cur, ok := memberOf[m]
			switch {
			case !ok:
				memberOf[m] = g.ID
			case l.groups[cur].Kind == groupType:
			case g.Kind == groupType || depth(g.ID) > depth(cur):
				memberOf[m] = g.ID
			}
		}
	}
	innermost := func(n *result.Node) string {
		if opts.Expand {
			return ""
		}
		if n.Group != "" {
			if _, ok := l.groups[n.Group]; ok {
				return n.Group
			}
		}
		return memberOf[n.ID]
	}

	// Collapse type groups.
	target := make(map[string]string, len(nodes)) // node ID -> DOT vertex ID
	typeMembers := map[string][]*result.Node{}
	var typeIDs []string
	for _, n := range nodes {
		g := innermost(n)
		if g != "" && l.groups[g].Kind == groupType {
			if _, ok := typeMembers[g]; !ok {
				typeIDs = append(typeIDs, g)
			}
			typeMembers[g] = append(typeMembers[g], n)
			target[n.ID] = "group:" + g
			continue
		}
		target[n.ID] = n.ID
		v := &vertex{
			id:        n.ID,
			label:     nodeLabel(address(n), n.Risk, n.Wave, n.Wave, opts.Waves),
			risk:      n.Risk,
			shape:     "box",
			style:     "rounded,filled",
			container: containerOf(g),
		}
		switch n.Kind {
		case "component":
			v.shape = "component"
			v.style = "filled"
		case "implied_service":
			v.style = "rounded,filled,dashed"
		}
		l.vertices = append(l.vertices, v)
	}
	for _, gid := range typeIDs {
		ms := typeMembers[gid]
		g := l.groups[gid]
		label := g.Label
		if t := commonType(ms); t != "" {
			label = fmt.Sprintf("%s x %d", t, len(ms))
		}
		risk, minW, maxW := 0.0, ms[0].Wave, ms[0].Wave
		for _, m := range ms {
			risk = max(risk, m.Risk)
			minW, maxW = min(minW, m.Wave), max(maxW, m.Wave)
		}
		l.vertices = append(l.vertices, &vertex{
			id:        "group:" + gid,
			label:     nodeLabel(label, risk, minW, maxW, opts.Waves),
			risk:      risk,
			shape:     "box3d",
			style:     "filled",
			container: containerOf(g.Parent),
		})
	}
	sort.Slice(l.vertices, func(i, j int) bool { return l.vertices[i].id < l.vertices[j].id })

	// Place vertices and build the cluster tree.
	for _, v := range l.vertices {
		if v.container == "" {
			l.topLevel = append(l.topLevel, v)
			continue
		}
		l.members[v.container] = append(l.members[v.container], v)
		seen := map[string]bool{}
		for id := v.container; id != "" && !seen[id]; id = containerOf(l.groups[id].Parent) {
			seen[id] = true
			l.usedGroup[id] = true
		}
	}
	for _, g := range groups {
		if !isContainer(g.Kind) {
			continue
		}
		if parent := containerOf(g.Parent); parent != "" && parent != g.ID {
			l.clusters[parent] = append(l.clusters[parent], g.ID)
		} else {
			l.roots = append(l.roots, g.ID)
		}
	}

	// Re-target and deduplicate edges.
	seen := map[dotEdge]bool{}
	for _, e := range doc.Graph.Edges {
		from, okF := target[e.From]
		to, okT := target[e.To]
		if !okF || !okT {
			continue
		}
		if from == to && e.From != e.To {
			continue // internal to a collapsed group
		}
		de := dotEdge{from, to, e.Kind}
		if seen[de] {
			continue
		}
		seen[de] = true
		l.edges = append(l.edges, de)
	}
	sort.Slice(l.edges, func(i, j int) bool {
		a, b := l.edges[i], l.edges[j]
		if a.from != b.from {
			return a.from < b.from
		}
		if a.to != b.to {
			return a.to < b.to
		}
		return a.kind < b.kind
	})
	return l
}

// commonType returns the type shared by all nodes, or "".
func commonType(ns []*result.Node) string {
	t := ns[0].Type
	for _, n := range ns[1:] {
		if n.Type != t {
			return ""
		}
	}
	return t
}

// nodeLabel builds a vertex label: title, risk band (text) and optionally wave.
func nodeLabel(title string, risk float64, minWave, maxWave int, waves bool) string {
	b := riskBand(risk)
	s := title + "\nrisk: " + b.name
	if risk > 0 {
		s += " (" + strconv.FormatFloat(result.Round4(risk), 'f', 2, 64) + ")"
	}
	if waves {
		if minWave == maxWave {
			s += fmt.Sprintf("\n[wave %d]", minWave)
		} else {
			s += fmt.Sprintf("\n[waves %d-%d]", minWave, maxWave)
		}
	}
	return s
}
