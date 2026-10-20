package graph

// New assembles a Graph from already-built nodes and edges (for example
// rebuilt from a saved result) and links adjacency lists. Algorithms such as
// Dependents, BlastRadiusTree, and Waves work on the returned graph.
func New(nodes []*Node, edges []*Edge) *Graph {
	g := &Graph{Nodes: nodes, Edges: edges}
	g.link()
	return g
}
