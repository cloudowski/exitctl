package terminal

import (
	"fmt"
	"io"
	"sort"

	"github.com/cloudowski/exitctl/internal/graph"
	"github.com/cloudowski/exitctl/internal/result"
)

// GroupKindType is the kind of a type collapse group (data-model.md 3.7).
const GroupKindType = "type"

// TreeOptions controls the dependency tree view (contracts/cli.md
// "exitctl graph").
type TreeOptions struct {
	// Expand disables type collapse: every node gets its own line.
	Expand bool
	// Waves appends "[wave N]" labels.
	Waves bool
	// BlastRadius, when set to a node ID, alias or display address, renders
	// the dependents tree of that node instead of the dependency tree.
	BlastRadius string
}

// treeUnit is one line candidate of the dependency tree: a single node or a
// collapsed type group.
type treeUnit struct {
	key     string // node ID or group ID
	label   string
	sortKey string
	waveMin int
	waveMax int
	deps    []int // unit indexes, sorted for display
	hasDeps bool
	indeg   int
}

// Tree renders the dependency tree of a result document: roots are nodes
// without dependents, children are their dependencies. A unit's dependencies
// are printed only at its first occurrence; later occurrences are marked
// "(see above)", which keeps the output finite with cycles and diamonds.
// Members of a type group render as one "<type> x N" line unless t.Expand is
// set. Container groups are not shown. Output is deterministic.
func Tree(w io.Writer, doc *result.Document, t TreeOptions, opts Options) error {
	if t.BlastRadius != "" {
		return blastTree(w, doc, t, opts)
	}
	units := buildUnits(doc, t.Expand)
	bw := &lineWriter{w: w}
	bw.line(opts.bold(fmt.Sprintf("Dependency tree (%d nodes, %d edges)", len(doc.Graph.Nodes), len(doc.Graph.Edges))))
	if len(units) == 0 {
		bw.line("  none")
		return bw.err
	}

	conn := treeConnectors(opts)
	expanded := make([]bool, len(units))
	printed := make([]bool, len(units))
	var walk func(u int, prefix, connector, childPrefix string)
	walk = func(u int, prefix, connector, childPrefix string) {
		printed[u] = true
		line := prefix + connector + unitLabel(units[u], t.Waves, opts)
		if expanded[u] && units[u].hasDeps {
			bw.line(line + " " + opts.dim("(see above)"))
			return
		}
		bw.line(line)
		expanded[u] = true
		deps := units[u].deps
		for i, d := range deps {
			c, next := conn.branch, conn.pipe
			if i == len(deps)-1 {
				c, next = conn.last, conn.space
			}
			walk(d, prefix+childPrefix, c, next)
		}
	}

	order := make([]int, len(units))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return unitLess(units[order[i]], units[order[j]]) })
	for _, u := range order {
		if units[u].indeg == 0 {
			walk(u, "", "", "")
		}
	}
	// Nodes reachable only through cycles have no root; start from the
	// first unprinted one in display order.
	for _, u := range order {
		if !printed[u] {
			walk(u, "", "", "")
		}
	}
	return bw.err
}

// blastTree renders the dependents tree of one node.
func blastTree(w io.Writer, doc *result.Document, t TreeOptions, opts Options) error {
	n := resolveNode(doc, t.BlastRadius)
	if n == nil {
		return fmt.Errorf("unknown resource %q", t.BlastRadius)
	}
	g := GraphFromDocument(doc)
	root := g.BlastRadiusTree(n.ID)
	if root == nil {
		return fmt.Errorf("unknown resource %q", t.BlastRadius)
	}
	addrs := Addresses(doc)
	waves := make(map[string]int, len(doc.Graph.Nodes))
	for _, dn := range doc.Graph.Nodes {
		waves[dn.ID] = dn.Wave
	}
	label := func(id string) string {
		s := addr(addrs, id)
		if t.Waves {
			s += " " + opts.dim(fmt.Sprintf("[wave %d]", waves[id]))
		}
		return s
	}

	bw := &lineWriter{w: w}
	bw.line(opts.bold(fmt.Sprintf("Blast radius of %s   dependents: %d", Address(n), countTree(root)-1)))
	bw.line(label(root.ID))
	conn := treeConnectors(opts)
	var walk func(tn *graph.TreeNode, prefix string)
	walk = func(tn *graph.TreeNode, prefix string) {
		children := append([]*graph.TreeNode(nil), tn.Children...)
		sort.SliceStable(children, func(i, j int) bool {
			ai, aj := addr(addrs, children[i].ID), addr(addrs, children[j].ID)
			if ai != aj {
				return ai < aj
			}
			return children[i].ID < children[j].ID
		})
		for i, c := range children {
			cn, next := conn.branch, conn.pipe
			if i == len(children)-1 {
				cn, next = conn.last, conn.space
			}
			bw.line(prefix + cn + label(c.ID))
			walk(c, prefix+next)
		}
	}
	walk(root, "")
	return bw.err
}

// resolveNode finds a node by ID or alias, then by unique display or native
// address.
func resolveNode(doc *result.Document, ref string) *result.Node {
	if n := doc.NodeByID(ref); n != nil {
		return n
	}
	var found *result.Node
	for i := range doc.Graph.Nodes {
		n := &doc.Graph.Nodes[i]
		if n.NativeAddress == ref || Address(n) == ref {
			if found != nil {
				return nil
			}
			found = n
		}
	}
	return found
}

func countTree(n *graph.TreeNode) int {
	c := 1
	for _, ch := range n.Children {
		c += countTree(ch)
	}
	return c
}

type connectors struct{ branch, last, pipe, space string }

func treeConnectors(opts Options) connectors {
	if opts.UTF8 {
		return connectors{"├── ", "└── ", "│   ", "    "}
	}
	return connectors{"|-- ", "`-- ", "|   ", "    "}
}

func unitLess(a, b *treeUnit) bool {
	if a.sortKey != b.sortKey {
		return a.sortKey < b.sortKey
	}
	return a.key < b.key
}

func unitLabel(u *treeUnit, waves bool, opts Options) string {
	if !waves {
		return u.label
	}
	w := fmt.Sprintf("[wave %d]", u.waveMin)
	if u.waveMax != u.waveMin {
		w = fmt.Sprintf("[wave %d-%d]", u.waveMin, u.waveMax)
	}
	return u.label + " " + opts.dim(w)
}

// buildUnits maps nodes to tree units (type groups collapse unless expand)
// and lifts edges to units, dropping edges inside a unit.
func buildUnits(doc *result.Document, expand bool) []*treeUnit {
	nodes := doc.Graph.Nodes
	byID := make(map[string]int, len(nodes)) // node ID or alias -> node index
	for i := range nodes {
		byID[nodes[i].ID] = i
	}
	for i := range nodes {
		for _, a := range nodes[i].Aliases {
			if _, ok := byID[a]; !ok {
				byID[a] = i
			}
		}
	}

	nodeUnit := make([]int, len(nodes))
	for i := range nodeUnit {
		nodeUnit[i] = -1
	}
	var units []*treeUnit
	if !expand {
		for _, gr := range doc.Graph.Groups {
			if gr.Kind != GroupKindType {
				continue
			}
			var members []int
			for _, m := range gr.Members {
				if i, ok := byID[m]; ok && nodeUnit[i] == -1 {
					members = append(members, i)
				}
			}
			if len(members) < 2 {
				continue
			}
			typ := nodes[members[0]].Type
			u := &treeUnit{
				key:     gr.ID,
				label:   fmt.Sprintf("%s x %d", typ, len(members)),
				sortKey: typ,
				waveMin: nodes[members[0]].Wave,
				waveMax: nodes[members[0]].Wave,
			}
			for _, i := range members {
				nodeUnit[i] = len(units)
				u.waveMin = min(u.waveMin, nodes[i].Wave)
				u.waveMax = max(u.waveMax, nodes[i].Wave)
			}
			units = append(units, u)
		}
	}
	for i := range nodes {
		if nodeUnit[i] != -1 {
			continue
		}
		a := Address(&nodes[i])
		nodeUnit[i] = len(units)
		units = append(units, &treeUnit{key: nodes[i].ID, label: a, sortKey: a,
			waveMin: nodes[i].Wave, waveMax: nodes[i].Wave})
	}

	seen := map[[2]int]bool{}
	for _, e := range doc.Graph.Edges {
		fi, ok1 := byID[e.From]
		ti, ok2 := byID[e.To]
		if !ok1 || !ok2 {
			continue
		}
		f, to := nodeUnit[fi], nodeUnit[ti]
		if f == to || seen[[2]int{f, to}] {
			continue
		}
		seen[[2]int{f, to}] = true
		units[f].deps = append(units[f].deps, to)
		units[to].indeg++
	}
	for _, u := range units {
		u.hasDeps = len(u.deps) > 0
		sort.SliceStable(u.deps, func(i, j int) bool { return unitLess(units[u.deps[i]], units[u.deps[j]]) })
	}
	return units
}
