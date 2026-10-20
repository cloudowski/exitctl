package graph

// WaveUnits lists the units of one migration wave. Each unit is the sorted
// list of its member node IDs; a unit with more than one member is a cycle
// whose members must move together (FR-055). Units are sorted by their
// smallest member ID.
type WaveUnits struct {
	Wave  int
	Units [][]string
}

// Waves computes migration waves on the condensation DAG (data-model.md 3.5,
// FR-056):
//
//	wave(u) = 1                                 if u has no outgoing dependency
//	          1 + max(wave(v) for v in deps(u)) otherwise
//
// Wave 1 holds the foundations that depend on nothing. Node.Wave is set on
// every node; all members of a cycle share a wave. The result is ordered by
// wave number.
func (g *Graph) Waves() []WaveUnits {
	st := g.state()
	st.mu.Lock()
	defer st.mu.Unlock()
	order := st.depsFirst()
	out := st.unitOut()
	wave := make([]int, len(st.units))
	maxWave := 0
	for _, u := range order {
		w := 1
		for _, v := range out[u] {
			if wave[v]+1 > w {
				w = wave[v] + 1
			}
		}
		wave[u] = w
		if w > maxWave {
			maxWave = w
		}
	}
	res := make([]WaveUnits, maxWave)
	for k := range res {
		res[k].Wave = k + 1
	}
	// Units are sorted by smallest member ID, so appending in unit order keeps
	// each wave sorted.
	for u, unit := range st.units {
		ids := make([]string, len(unit.Members))
		for k, m := range unit.Members {
			ids[k] = g.Nodes[m].ID
			g.Nodes[m].Wave = wave[u]
		}
		res[wave[u]-1].Units = append(res[wave[u]-1].Units, ids)
	}
	return res
}

// unitOut returns, per unit position, the sorted unit positions it depends
// on (the reverse of uIn).
func (st *algoState) unitOut() [][]int {
	out := make([][]int, len(st.units))
	for v, ins := range st.uIn {
		for _, u := range ins {
			out[u] = append(out[u], v)
		}
	}
	// Iterating v in increasing order already yields sorted lists.
	return out
}

// depsFirst returns unit positions in a topological order of the
// condensation DAG where every unit comes after all units it depends on.
// Ties are broken by unit position, so the order is deterministic.
func (st *algoState) depsFirst() []int {
	n := len(st.units)
	outdeg := make([]int, n)
	for _, ins := range st.uIn {
		for _, u := range ins {
			outdeg[u]++
		}
	}
	order := make([]int, 0, n)
	for u := 0; u < n; u++ {
		if outdeg[u] == 0 {
			order = append(order, u)
		}
	}
	for k := 0; k < len(order); k++ {
		for _, u := range st.uIn[order[k]] {
			outdeg[u]--
			if outdeg[u] == 0 {
				order = append(order, u)
			}
		}
	}
	return order
}
