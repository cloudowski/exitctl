package graph

import (
	"math/bits"
	"sort"
	"sync"

	"gonum.org/v1/gonum/graph/simple"
	"gonum.org/v1/gonum/graph/topo"
)

// Unit is one strongly connected component of the graph: a node that is
// not part of a cycle forms a unit on its own. Members are node positions in
// Graph.Nodes, sorted by node ID. ID is 1-based and follows the order of
// units sorted by their smallest member ID.
type Unit struct {
	ID      int
	Members []int
}

// algoState is the lazily computed condensation of a graph plus memoized
// reverse reachability per unit.
type algoState struct {
	mu     sync.Mutex
	units  []Unit
	unitOf []int   // node position -> unit position (0-based)
	uIn    [][]int // unit position -> unit positions that depend on it (sorted)
	words  int
	reach  [][]uint64 // unit position -> bitset of dependent units (nil = not computed)
	counts []int      // unit position -> number of dependent nodes outside the unit (-1 = not computed)
}

// Condense computes the strongly connected components with Tarjan's
// algorithm (gonum topo.TarjanSCC). Units are sorted by their smallest member
// ID and numbered from 1. Node.SCC is set to the unit ID for members of units
// with more than one member and to 0 for every other node. The returned
// slice is a copy and may be modified by the caller.
func (g *Graph) Condense() []Unit {
	st := g.state()
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]Unit, len(st.units))
	for i, u := range st.units {
		out[i] = Unit{ID: u.ID, Members: append([]int(nil), u.Members...)}
	}
	return out
}

// Dependents returns the IDs of all nodes with a directed path to the node
// (reverse reachability), excluding the node itself (FR-052). Members of the
// same cycle are mutual dependents. id may be a node ID or alias; an unknown
// id returns nil. The result is sorted by ID.
func (g *Graph) Dependents(id string) []string {
	i, ok := g.Index[id]
	if !ok {
		return nil
	}
	st := g.state()
	st.mu.Lock()
	defer st.mu.Unlock()
	u := st.unitOf[i]
	set := st.reachOf(u)
	var out []string
	for _, m := range st.units[u].Members {
		if m != i {
			out = append(out, g.Nodes[m].ID)
		}
	}
	for w, word := range set {
		for word != 0 {
			b := bits.TrailingZeros64(word)
			word &^= 1 << uint(b)
			for _, m := range st.units[w*64+b].Members {
				out = append(out, g.Nodes[m].ID)
			}
		}
	}
	sort.Strings(out)
	return out
}

// DependentsCount returns len(Dependents(id)) without building the list.
func (g *Graph) DependentsCount(id string) int {
	i, ok := g.Index[id]
	if !ok {
		return 0
	}
	st := g.state()
	st.mu.Lock()
	defer st.mu.Unlock()
	u := st.unitOf[i]
	if st.counts[u] < 0 {
		n := 0
		for w, word := range st.reachOf(u) {
			for word != 0 {
				b := bits.TrailingZeros64(word)
				word &^= 1 << uint(b)
				n += len(st.units[w*64+b].Members)
			}
		}
		st.counts[u] = n
	}
	return st.counts[u] + len(st.units[u].Members) - 1
}

// state returns the condensation, computing it on first use.
func (g *Graph) state() *algoState {
	if g.algo == nil {
		g.algo = g.condense()
	}
	return g.algo
}

// condense runs Tarjan's algorithm and builds the unit DAG. It also sets
// Node.SCC.
func (g *Graph) condense() *algoState {
	dg := simple.NewDirectedGraph()
	for i := range g.Nodes {
		dg.AddNode(simple.Node(int64(i)))
	}
	for i, outs := range g.Out {
		for _, j := range outs {
			if i == j {
				continue // self-loops are not dependencies
			}
			dg.SetEdge(dg.NewEdge(simple.Node(int64(i)), simple.Node(int64(j))))
		}
	}
	sccs := topo.TarjanSCC(dg)
	units := make([]Unit, 0, len(sccs))
	for _, c := range sccs {
		members := make([]int, len(c))
		for k, n := range c {
			members[k] = int(n.ID())
		}
		sort.Slice(members, func(a, b int) bool { return g.Nodes[members[a]].ID < g.Nodes[members[b]].ID })
		units = append(units, Unit{Members: members})
	}
	sort.Slice(units, func(a, b int) bool {
		return g.Nodes[units[a].Members[0]].ID < g.Nodes[units[b].Members[0]].ID
	})
	st := &algoState{
		units:  units,
		unitOf: make([]int, len(g.Nodes)),
		uIn:    make([][]int, len(units)),
		words:  (len(units) + 63) / 64,
		reach:  make([][]uint64, len(units)),
		counts: make([]int, len(units)),
	}
	for k := range st.units {
		st.units[k].ID = k + 1
		st.counts[k] = -1
		for _, m := range st.units[k].Members {
			st.unitOf[m] = k
			if len(st.units[k].Members) > 1 {
				g.Nodes[m].SCC = k + 1
			} else {
				g.Nodes[m].SCC = 0
			}
		}
	}
	seen := make(map[[2]int]bool)
	for i, ins := range g.In {
		ui := st.unitOf[i]
		for _, j := range ins {
			uj := st.unitOf[j]
			if uj == ui || seen[[2]int{ui, uj}] {
				continue
			}
			seen[[2]int{ui, uj}] = true
			st.uIn[ui] = append(st.uIn[ui], uj)
		}
	}
	for k := range st.uIn {
		sort.Ints(st.uIn[k])
	}
	return st
}

// reachOf returns the memoized bitset of units that have a directed path to
// unit u in the condensation DAG (u excluded). It uses an explicit stack so
// deep graphs cannot overflow.
func (st *algoState) reachOf(u int) []uint64 {
	if st.reach[u] != nil {
		return st.reach[u]
	}
	type frame struct{ unit, next int }
	stack := []frame{{unit: u}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.next < len(st.uIn[top.unit]) {
			v := st.uIn[top.unit][top.next]
			top.next++
			if st.reach[v] == nil {
				stack = append(stack, frame{unit: v})
			}
			continue
		}
		set := make([]uint64, st.words)
		for _, v := range st.uIn[top.unit] {
			set[v/64] |= 1 << uint(v%64)
			for w, word := range st.reach[v] {
				set[w] |= word
			}
		}
		st.reach[top.unit] = set
		stack = stack[:len(stack)-1]
	}
	return st.reach[u]
}
