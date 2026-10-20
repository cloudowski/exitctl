package graph

import (
	"reflect"
	"testing"
)

func setPred(ids ...string) func(string) bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return func(id string) bool { return m[id] }
}

func TestChainsLinearWithGap(t *testing.T) {
	// a -> b -> c -> d -> e; c does not qualify, so the path splits.
	g := handGraph([]string{"a", "b", "c", "d", "e"}, [][2]string{
		{"a", "b"}, {"b", "c"}, {"c", "d"}, {"d", "e"},
	})
	got := g.Chains(setPred("a", "b", "d", "e"))
	want := [][]string{{"a", "b"}, {"d", "e"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chains = %v, want %v", got, want)
	}
	// All qualify: one maximal chain.
	got = g.Chains(setPred("a", "b", "c", "d", "e"))
	if want := [][]string{{"a", "b", "c", "d", "e"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Chains(all) = %v, want %v", got, want)
	}
	// Nothing qualifies, or isolated qualifying nodes: no chains.
	if got := g.Chains(setPred()); got != nil {
		t.Errorf("Chains(none) = %v", got)
	}
	if got := g.Chains(setPred("a", "c", "e")); got != nil {
		t.Errorf("Chains(isolated) = %v", got)
	}
}

func TestChainsLongestAndTieBreak(t *testing.T) {
	// top -> {right, left} -> base; left also -> mid -> base.
	// x -> base is a shorter separate start.
	g := handGraph([]string{"base", "left", "mid", "right", "top", "x"}, [][2]string{
		{"top", "right"}, {"top", "left"}, {"left", "base"}, {"right", "base"},
		{"left", "mid"}, {"mid", "base"}, {"x", "base"},
	})
	all := setPred("base", "left", "mid", "right", "top", "x")
	got := g.Chains(all)
	want := [][]string{{"top", "left", "mid", "base"}, {"x", "base"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Chains = %v, want %v", got, want)
	}
	// Without mid, left and right tie at the same length: lexicographic wins.
	got = g.Chains(setPred("base", "left", "right", "top"))
	if want := [][]string{{"top", "left", "base"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Chains(tie) = %v, want %v", got, want)
	}
	// Two independent starts with equal length sort lexicographically.
	got = g.Chains(setPred("base", "right", "x"))
	if want := [][]string{{"right", "base"}, {"x", "base"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Chains(two starts) = %v, want %v", got, want)
	}
}

func TestChainsCycleRepresentative(t *testing.T) {
	// a -> b -> c -> a is a cycle; d depends on a; c depends on e.
	g := handGraph([]string{"a", "b", "c", "d", "e"}, [][2]string{
		{"a", "b"}, {"b", "c"}, {"c", "a"}, {"d", "a"}, {"c", "e"},
	})
	// Only b qualifies inside the cycle, so the unit is represented by b.
	chains := g.Chains(setPred("b", "d", "e"))
	want := [][]string{{"d", "b", "e"}}
	if !reflect.DeepEqual(chains, want) {
		t.Fatalf("Chains = %v, want %v", chains, want)
	}
	if got := ChainsThrough(chains, "c", 3); got != nil {
		t.Errorf("ChainsThrough(c) = %v, want none (c is not the representative)", got)
	}
	if got := g.ChainsThroughNode(chains, "c", 3); !reflect.DeepEqual(got, want) {
		t.Errorf("ChainsThroughNode(c) = %v, want %v", got, want)
	}
	if got := g.ChainsThroughNode(chains, "nope", 3); got != nil {
		t.Errorf("ChainsThroughNode(nope) = %v", got)
	}
}

func TestChainsThrough(t *testing.T) {
	chains := [][]string{
		{"a", "b", "c", "d"},
		{"a", "c", "e"},
		{"b", "c"},
		{"c", "z"},
		{"x", "y"},
	}
	if got, want := ChainsThrough(chains, "c", 3), chains[:3]; !reflect.DeepEqual(got, want) {
		t.Errorf("ChainsThrough(c, 3) = %v, want %v", got, want)
	}
	if got, want := ChainsThrough(chains, "c", 0), chains[:4]; !reflect.DeepEqual(got, want) {
		t.Errorf("ChainsThrough(c, 0) = %v, want %v", got, want)
	}
	if got := ChainsThrough(chains, "q", 3); got != nil {
		t.Errorf("ChainsThrough(q) = %v", got)
	}
}
