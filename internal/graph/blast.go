package graph

// TreeNode is one node of a blast radius tree (data-model.md 3.4, FR-057).
// Depth is the number of reversed edges from the root (root = 0). Children
// are sorted by ID.
type TreeNode struct {
	ID       string
	Depth    int
	Children []*TreeNode
}

// BlastRadiusTree returns the shortest-path tree of dependents rooted at the
// node: a BFS from the node over reversed edges (Graph.In), visiting
// neighbors sorted by canonical ID, where each dependent is attached to its
// first discoverer. The result is deterministic. id may be a node ID or alias;
// an unknown id returns nil. Self-loops are ignored and cycle members appear
// once.
func (g *Graph) BlastRadiusTree(id string) *TreeNode {
	start, ok := g.Index[id]
	if !ok {
		return nil
	}
	root := &TreeNode{ID: g.Nodes[start].ID}
	seen := make([]bool, len(g.Nodes))
	seen[start] = true
	type item struct {
		node int
		tree *TreeNode
	}
	queue := []item{{start, root}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		// In is sorted by node ID (see Graph), so children come out sorted.
		for _, d := range g.In[cur.node] {
			if seen[d] {
				continue
			}
			seen[d] = true
			child := &TreeNode{ID: g.Nodes[d].ID, Depth: cur.tree.Depth + 1}
			cur.tree.Children = append(cur.tree.Children, child)
			queue = append(queue, item{d, child})
		}
	}
	return root
}
