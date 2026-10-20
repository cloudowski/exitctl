package graph

import (
	"slices"
	"sort"
)

// Chains returns lock-in chains (data-model.md 3.6, FR-058): maximal directed
// paths in the condensation DAG, following edges from dependent to
// dependency, whose units all contain at least one qualifying node.
// qualifies reports whether a node ID qualifies (the engine passes "risk >=
// normalized medium risk").
//
// For every qualifying unit that no other qualifying unit depends on, the
// longest qualifying path starting there is computed by dynamic programming;
// ties are broken by the lexicographic order of the ID sequence. A unit is
// represented by its smallest qualifying member ID. Only chains of at least
// two units are returned, sorted by length (descending) and then
// lexicographically by their IDs.
func (g *Graph) Chains(qualifies func(id string) bool) [][]string {
	st := g.state()
	st.mu.Lock()
	defer st.mu.Unlock()
	n := len(st.units)
	rep := make([]string, n) // "" = unit does not qualify
	for u, unit := range st.units {
		for _, m := range unit.Members { // members are sorted by ID
			if qualifies(g.Nodes[m].ID) {
				rep[u] = g.Nodes[m].ID
				break
			}
		}
	}
	out := st.unitOut()
	length := make([]int, n) // longest qualifying path from u, in units
	next := make([]int, n)   // successor on that path, -1 = end
	seq := func(u int) []string {
		var s []string
		for ; u >= 0; u = next[u] {
			s = append(s, rep[u])
		}
		return s
	}
	for _, u := range st.depsFirst() {
		if rep[u] == "" {
			continue
		}
		length[u], next[u] = 1, -1
		for _, v := range out[u] {
			if rep[v] == "" {
				continue
			}
			switch {
			case length[v]+1 > length[u]:
				length[u], next[u] = length[v]+1, v
			case length[v]+1 == length[u] && slices.Compare(seq(v), seq(next[u])) < 0:
				next[u] = v
			}
		}
	}
	var chains [][]string
	seen := make(map[string]bool)
	for u := 0; u < n; u++ {
		if rep[u] == "" || length[u] < 2 {
			continue
		}
		start := true
		for _, d := range st.uIn[u] {
			if rep[d] != "" {
				start = false
				break
			}
		}
		if !start {
			continue
		}
		c := seq(u)
		key := joinKey(c)
		if seen[key] {
			continue
		}
		seen[key] = true
		chains = append(chains, c)
	}
	sort.Slice(chains, func(a, b int) bool {
		if len(chains[a]) != len(chains[b]) {
			return len(chains[a]) > len(chains[b])
		}
		return slices.Compare(chains[a], chains[b]) < 0
	})
	return chains
}

// ChainsThrough returns up to max chains (in their given order) that contain
// id. max <= 0 means no limit. Chains name units by their smallest qualifying
// member; use Graph.ChainsThroughNode to also match other cycle members.
func ChainsThrough(chains [][]string, id string, max int) [][]string {
	var res [][]string
	for _, c := range chains {
		if max > 0 && len(res) >= max {
			break
		}
		if slices.Contains(c, id) {
			res = append(res, c)
		}
	}
	return res
}

// ChainsThroughNode is ChainsThrough matching any member of the node's unit,
// so a cycle member finds the chains that name its unit by another member.
// id may be a node ID or alias; an unknown id returns nil.
func (g *Graph) ChainsThroughNode(chains [][]string, id string, max int) [][]string {
	i, ok := g.Index[id]
	if !ok {
		return nil
	}
	st := g.state()
	st.mu.Lock()
	members := make(map[string]bool)
	for _, m := range st.units[st.unitOf[i]].Members {
		members[g.Nodes[m].ID] = true
	}
	st.mu.Unlock()
	var res [][]string
	for _, c := range chains {
		if max > 0 && len(res) >= max {
			break
		}
		for _, x := range c {
			if members[x] {
				res = append(res, c)
				break
			}
		}
	}
	return res
}

func joinKey(ids []string) string {
	k := ""
	for _, id := range ids {
		k += id + "\x00"
	}
	return k
}
