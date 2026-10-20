package graph

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
)

// ---------------------------------------------------------------- fixtures

func gNode(id, typ string, kinds ...model.SourceKind) *Node {
	addr := id[strings.Index(id, "#")+1:]
	return &Node{ID: id, Kind: NodeResource, Type: typ, NativeAddress: addr, SourceKinds: kinds}
}

func gEdge(from, to string) *Edge {
	return &Edge{From: from, To: to, Kind: EdgeReference, Confidence: ConfExact}
}

// fanGraph: n nodes of type aws_x all depending on one shared role.
func fanGraph(n int) *Graph {
	nodes := []*Node{gNode("r//#aws_iam_role.shared", "aws_iam_role", model.KindTerraform)}
	var edges []*Edge
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("r//#aws_x.n%03d", i)
		nodes = append(nodes, gNode(id, "aws_x", model.KindTerraform))
		edges = append(edges, gEdge(id, "r//#aws_iam_role.shared"))
	}
	return New(nodes, edges)
}

func typeGroupsOf(gs []Group) []Group {
	var out []Group
	for _, gr := range gs {
		if gr.Kind == GroupType {
			out = append(out, gr)
		}
	}
	return out
}

func groupsOfKind(gs []Group, k GroupKind) []Group {
	var out []Group
	for _, gr := range gs {
		if gr.Kind == k {
			out = append(out, gr)
		}
	}
	return out
}

// ---------------------------------------------------------------- type collapse

func TestGroupsTypeCollapse(t *testing.T) {
	g := fanGraph(200)
	gs := g.Groups(1000)
	if len(gs) != 1 {
		t.Fatalf("groups = %d, want 1: %+v", len(gs), gs)
	}
	gr := gs[0]
	if gr.Kind != GroupType || gr.Label != "aws_x x 200" || len(gr.Members) != 200 || gr.Parent != "" {
		t.Fatalf("group = %+v", gr)
	}
	if !strings.HasPrefix(gr.ID, "g-") || len(gr.ID) != 14 {
		t.Errorf("group ID %q, want g- + 12 hex", gr.ID)
	}
	for i := 1; i < len(gr.Members); i++ {
		if gr.Members[i-1] >= gr.Members[i] {
			t.Fatalf("members not sorted at %d", i)
		}
	}
	if n := g.Node("r//#aws_x.n007"); n.Group != gr.ID {
		t.Errorf("node group = %q, want %q", n.Group, gr.ID)
	}
	if n := g.Node("r//#aws_iam_role.shared"); n.Group != "" {
		t.Errorf("role group = %q, want none", n.Group)
	}
}

func TestGroupsTypeCollapseNeedsThree(t *testing.T) {
	if gs := fanGraph(2).Groups(1000); len(gs) != 0 {
		t.Fatalf("N=2 collapsed: %+v", gs)
	}
	if gs := fanGraph(3).Groups(1000); len(gs) != 1 || gs[0].Label != "aws_x x 3" {
		t.Fatalf("N=3 groups = %+v", gs)
	}
}

func TestGroupsDifferentNeighborsDoNotCollapse(t *testing.T) {
	nodes := []*Node{
		gNode("r//#aws_iam_role.a", "aws_iam_role", model.KindTerraform),
		gNode("r//#aws_iam_role.b", "aws_iam_role", model.KindTerraform),
		gNode("r//#aws_iam_role.c", "aws_iam_role", model.KindTerraform),
	}
	var edges []*Edge
	for i, role := range []string{"a", "b", "c"} {
		id := fmt.Sprintf("r//#aws_x.n%d", i)
		nodes = append(nodes, gNode(id, "aws_x", model.KindTerraform))
		edges = append(edges, gEdge(id, "r//#aws_iam_role."+role))
	}
	g := New(nodes, edges)
	gs := g.Groups(1000)
	// The three roles share type and have no out-neighbors, but each has a
	// different dependent, so nothing collapses.
	if len(gs) != 0 {
		t.Fatalf("groups = %+v, want none", gs)
	}

	// Same neighbors but a different service also keeps nodes apart.
	f := fanGraph(3)
	f.Node("r//#aws_x.n000").Service = "aws.other"
	if gs := f.Groups(1000); len(gs) != 0 {
		t.Fatalf("service differs, groups = %+v", gs)
	}
}

// ---------------------------------------------------------------- containers

func containerGraph() *Graph {
	var nodes []*Node
	for i := 0; i < 3; i++ {
		nodes = append(nodes,
			gNode(fmt.Sprintf("r//infra#module.vpc.aws_subnet.s%d", i), "aws_subnet", model.KindTerraform),
			gNode(fmt.Sprintf(`r//infra#module.app["a"].module.db.aws_db_instance.d%d`, i), "aws_db_instance", model.KindTerraform),
			gNode(fmt.Sprintf("r//#apps/Deployment/shop/web%d", i), "apps/Deployment", model.KindKubernetes),
		)
	}
	nodes = append(nodes,
		gNode("r//infra#aws_s3_bucket.root", "aws_s3_bucket", model.KindTerraform),
		gNode("r//#/Namespace/_/shop", "Namespace", model.KindKubernetes),
	)
	// Every chain subnet <- db <- deployment has its own edges, so no two
	// nodes share a signature and no type group forms.
	var edges []*Edge
	for i := 0; i < 3; i++ {
		edges = append(edges,
			gEdge(fmt.Sprintf(`r//infra#module.app["a"].module.db.aws_db_instance.d%d`, i), fmt.Sprintf("r//infra#module.vpc.aws_subnet.s%d", i)),
			gEdge(fmt.Sprintf("r//#apps/Deployment/shop/web%d", i), fmt.Sprintf(`r//infra#module.app["a"].module.db.aws_db_instance.d%d`, i)),
		)
	}
	return New(nodes, edges)
}

func TestGroupsContainersAboveThreshold(t *testing.T) {
	g := containerGraph()
	gs := g.Groups(5) // 11 nodes > 5

	mods := groupsOfKind(gs, GroupModule)
	var labels []string
	for _, m := range mods {
		labels = append(labels, m.Label)
	}
	if want := []string{"module.app.module.db", "module.vpc"}; !sameSet(labels, want) {
		t.Fatalf("module labels = %v, want %v", labels, want)
	}
	nss := groupsOfKind(gs, GroupNamespace)
	if len(nss) != 1 || nss[0].Label != "shop" || len(nss[0].Members) != 3 {
		t.Fatalf("namespace groups = %+v", nss)
	}
	// Root-module and cluster-scoped resources stay ungrouped.
	for _, id := range []string{"r//infra#aws_s3_bucket.root", "r//#/Namespace/_/shop"} {
		if n := g.Node(id); n.Group != "" {
			t.Errorf("%s group = %q, want none", id, n.Group)
		}
	}
	if n := g.Node(`r//infra#module.app["a"].module.db.aws_db_instance.d1`); groupKind(gs, n.Group) != GroupModule {
		t.Errorf("db instance group = %q", n.Group)
	}
	if tg := typeGroupsOf(gs); len(tg) != 0 {
		t.Errorf("unexpected type groups %+v", tg)
	}
}

func TestGroupsTypeGroupParent(t *testing.T) {
	var nodes []*Node
	for i := 0; i < 4; i++ {
		nodes = append(nodes, gNode(fmt.Sprintf("r//infra#module.vpc.aws_subnet.s%d", i), "aws_subnet", model.KindTerraform))
	}
	nodes = append(nodes, gNode("r//infra#module.vpc.aws_vpc.main", "aws_vpc", model.KindTerraform))
	g := New(nodes, nil)
	gs := g.Groups(2)
	tg := typeGroupsOf(gs)
	mods := groupsOfKind(gs, GroupModule)
	if len(tg) != 1 || len(mods) != 1 {
		t.Fatalf("groups = %+v", gs)
	}
	if tg[0].Parent != mods[0].ID || len(mods[0].Members) != 5 {
		t.Fatalf("type group %+v, module %+v", tg[0], mods[0])
	}
	if n := g.Node("r//infra#module.vpc.aws_subnet.s0"); n.Group != tg[0].ID {
		t.Errorf("subnet group = %q, want innermost %q", n.Group, tg[0].ID)
	}
	if n := g.Node("r//infra#module.vpc.aws_vpc.main"); n.Group != mods[0].ID {
		t.Errorf("vpc group = %q, want %q", n.Group, mods[0].ID)
	}
}

func TestGroupsContainersNotBelowThreshold(t *testing.T) {
	g := containerGraph()
	gs := g.Groups(50)
	for _, gr := range gs {
		if gr.Kind != GroupType {
			t.Fatalf("container group below threshold: %+v", gr)
		}
	}
	if n := g.Node("r//#apps/Deployment/shop/web0"); n.Group != "" {
		t.Errorf("group = %q, want none", n.Group)
	}
}

func TestGroupsStableIDs(t *testing.T) {
	a := containerGraph().Groups(5)
	b := containerGraph().Groups(5)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("groups differ across runs:\n%+v\n%+v", a, b)
	}
	for i := 1; i < len(a); i++ {
		if a[i-1].ID >= a[i].ID {
			t.Fatalf("groups not sorted by ID")
		}
	}
	x := fanGraph(10).Groups(1000)
	y := fanGraph(10).Groups(1000)
	if x[0].ID != y[0].ID {
		t.Fatalf("type group ID unstable: %s vs %s", x[0].ID, y[0].ID)
	}
}

func TestScoreGroups(t *testing.T) {
	gs := fanGraph(3).Groups(1000)
	risk := map[string]float64{"r//#aws_x.n000": 0.2, "r//#aws_x.n001": 0.7, "r//#aws_x.n002": 0.5}
	ScoreGroups(gs, func(id string) (float64, float64) { return risk[id], risk[id] * 2 })
	if gs[0].MaxRisk != 0.7 || gs[0].Contribution < 2.799 || gs[0].Contribution > 2.801 {
		t.Fatalf("scored group = %+v", gs[0])
	}
}

func groupKind(gs []Group, id string) GroupKind {
	for _, gr := range gs {
		if gr.ID == id {
			return gr.Kind
		}
	}
	return ""
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, s := range a {
		if m[s] != 0 {
			return false
		}
	}
	return true
}
