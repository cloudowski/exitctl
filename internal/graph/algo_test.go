package graph

import (
	"reflect"
	"testing"
)

// handGraph builds a graph from "from->to" pairs (from depends on to).
func handGraph(ids []string, edges [][2]string) *Graph {
	g := &Graph{}
	for _, id := range ids {
		g.Nodes = append(g.Nodes, &Node{ID: id, Kind: NodeResource})
	}
	for _, e := range edges {
		g.Edges = append(g.Edges, &Edge{From: e[0], To: e[1], Kind: EdgeReference, Confidence: ConfExact})
	}
	g.link()
	return g
}

func TestAlgoDependentsDiamond(t *testing.T) {
	// top depends on left and right, both depend on base.
	g := handGraph([]string{"base", "left", "right", "top"}, [][2]string{
		{"top", "left"}, {"top", "right"}, {"left", "base"}, {"right", "base"},
	})
	cases := map[string][]string{
		"base":  {"left", "right", "top"},
		"left":  {"top"},
		"right": {"top"},
		"top":   nil,
	}
	for id, want := range cases {
		got := g.Dependents(id)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Dependents(%s) = %v, want %v", id, got, want)
		}
		if n := g.DependentsCount(id); n != len(want) {
			t.Errorf("DependentsCount(%s) = %d, want %d", id, n, len(want))
		}
	}
	for _, u := range g.Condense() {
		if len(u.Members) != 1 {
			t.Errorf("unexpected multi-member unit %v", u)
		}
	}
	for _, n := range g.Nodes {
		if n.SCC != 0 {
			t.Errorf("node %s SCC = %d, want 0", n.ID, n.SCC)
		}
	}
}

func TestAlgoCondenseThreeCycle(t *testing.T) {
	// a -> b -> c -> a is a cycle; d depends on a; c depends on e.
	g := handGraph([]string{"a", "b", "c", "d", "e"}, [][2]string{
		{"a", "b"}, {"b", "c"}, {"c", "a"}, {"d", "a"}, {"c", "e"},
	})
	units := g.Condense()
	want := []Unit{
		{ID: 1, Members: []int{0, 1, 2}},
		{ID: 2, Members: []int{3}},
		{ID: 3, Members: []int{4}},
	}
	if !reflect.DeepEqual(units, want) {
		t.Fatalf("Condense = %v, want %v", units, want)
	}
	for _, id := range []string{"a", "b", "c"} {
		if s := g.Node(id).SCC; s != 1 {
			t.Errorf("%s SCC = %d, want 1", id, s)
		}
	}
	if s := g.Node("d").SCC; s != 0 {
		t.Errorf("d SCC = %d, want 0", s)
	}
	// Cycle members are mutual dependents, plus d.
	if got, w := g.Dependents("a"), []string{"b", "c", "d"}; !reflect.DeepEqual(got, w) {
		t.Errorf("Dependents(a) = %v, want %v", got, w)
	}
	if got, w := g.Dependents("b"), []string{"a", "c", "d"}; !reflect.DeepEqual(got, w) {
		t.Errorf("Dependents(b) = %v, want %v", got, w)
	}
	if got, w := g.Dependents("e"), []string{"a", "b", "c", "d"}; !reflect.DeepEqual(got, w) {
		t.Errorf("Dependents(e) = %v, want %v", got, w)
	}
	for i := 0; i < 2; i++ { // repeated calls use the memo
		if n := g.DependentsCount("c"); n != 3 {
			t.Errorf("DependentsCount(c) = %d, want 3", n)
		}
		if n := g.DependentsCount("e"); n != 4 {
			t.Errorf("DependentsCount(e) = %d, want 4", n)
		}
	}
	if n := g.DependentsCount("d"); n != 0 {
		t.Errorf("DependentsCount(d) = %d, want 0", n)
	}
	// Condense returns copies.
	units[0].Members[0] = 99
	if g.Condense()[0].Members[0] != 0 {
		t.Error("Condense result aliases internal state")
	}
}

func TestAlgoDependentsUnknownAndAlias(t *testing.T) {
	g := handGraph([]string{"x", "y"}, [][2]string{{"y", "x"}, {"x", "x"}})
	g.Nodes[0].Aliases = []string{"x-alias"}
	g.link()
	if got := g.Dependents("nope"); got != nil {
		t.Errorf("Dependents(nope) = %v", got)
	}
	if n := g.DependentsCount("nope"); n != 0 {
		t.Errorf("DependentsCount(nope) = %d", n)
	}
	if got := g.Dependents("x-alias"); !reflect.DeepEqual(got, []string{"y"}) {
		t.Errorf("Dependents(x-alias) = %v", got)
	}
	if s := g.Node("x").SCC; s != 0 {
		t.Errorf("self-loop must not form a cycle, SCC = %d", s)
	}
}

func TestAlgoDependentsLongChain(t *testing.T) {
	// A long chain exercises the iterative memo without recursion limits.
	const n = 5000
	ids := make([]string, n)
	var edges [][2]string
	for i := range ids {
		ids[i] = nodeName(i)
		if i > 0 {
			edges = append(edges, [2]string{ids[i], ids[i-1]})
		}
	}
	g := handGraph(ids, edges)
	if c := g.DependentsCount(ids[0]); c != n-1 {
		t.Fatalf("DependentsCount = %d, want %d", c, n-1)
	}
	if c := g.DependentsCount(ids[n-1]); c != 0 {
		t.Fatalf("DependentsCount(last) = %d", c)
	}
}

func nodeName(i int) string {
	const digits = "0123456789"
	b := []byte("n00000")
	for p := len(b) - 1; p > 0 && i > 0; p-- {
		b[p] = digits[i%10]
		i /= 10
	}
	return string(b)
}
