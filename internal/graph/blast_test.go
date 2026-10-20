package graph

import (
	"fmt"
	"strings"
	"testing"
)

// treeString renders a tree as "id@depth(children...)" for compact comparison.
func treeString(t *TreeNode) string {
	if t == nil {
		return "<nil>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s@%d", t.ID, t.Depth)
	if len(t.Children) > 0 {
		parts := make([]string, len(t.Children))
		for i, c := range t.Children {
			parts[i] = treeString(c)
		}
		b.WriteString("(" + strings.Join(parts, " ") + ")")
	}
	return b.String()
}

func TestBlastRadiusTreeDiamond(t *testing.T) {
	// top depends on left and right, both depend on base; app depends on top.
	g := handGraph([]string{"app", "base", "left", "right", "top"}, [][2]string{
		{"top", "left"}, {"top", "right"}, {"left", "base"}, {"right", "base"}, {"app", "top"},
	})
	// top is discovered first by left (sorted before right), so it hangs
	// under left only.
	want := "base@0(left@1(top@2(app@3)) right@1)"
	if got := treeString(g.BlastRadiusTree("base")); got != want {
		t.Errorf("BlastRadiusTree(base) = %s, want %s", got, want)
	}
	if got := treeString(g.BlastRadiusTree("app")); got != "app@0" {
		t.Errorf("BlastRadiusTree(app) = %s", got)
	}
	if g.BlastRadiusTree("nope") != nil {
		t.Error("unknown id must return nil")
	}
}

func TestBlastRadiusTreeCycle(t *testing.T) {
	// a -> b -> c -> a is a cycle; d depends on a; c depends on e.
	g := handGraph([]string{"a", "b", "c", "d", "e"}, [][2]string{
		{"a", "b"}, {"b", "c"}, {"c", "a"}, {"d", "a"}, {"c", "e"},
	})
	cases := map[string]string{
		"e": "e@0(c@1(b@2(a@3(d@4))))",
		"a": "a@0(c@1(b@2) d@1)",
		"b": "b@0(a@1(c@2 d@2))",
	}
	for id, want := range cases {
		for i := 0; i < 2; i++ { // deterministic across calls
			if got := treeString(g.BlastRadiusTree(id)); got != want {
				t.Errorf("BlastRadiusTree(%s) = %s, want %s", id, got, want)
			}
		}
	}
	// Every dependent appears exactly once.
	if n := countTree(g.BlastRadiusTree("e")) - 1; n != g.DependentsCount("e") {
		t.Errorf("tree size %d != DependentsCount %d", n, g.DependentsCount("e"))
	}
}

func TestBlastRadiusTreeAlias(t *testing.T) {
	g := handGraph([]string{"x", "y"}, [][2]string{{"y", "x"}})
	g.Nodes[0].Aliases = []string{"x-alias"}
	g.link()
	if got := treeString(g.BlastRadiusTree("x-alias")); got != "x@0(y@1)" {
		t.Errorf("BlastRadiusTree(x-alias) = %s", got)
	}
}

func countTree(t *TreeNode) int {
	n := 1
	for _, c := range t.Children {
		n += countTree(c)
	}
	return n
}
