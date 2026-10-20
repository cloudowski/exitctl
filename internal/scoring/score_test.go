package scoring

import (
	"fmt"
	"sort"
	"testing"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/config"
	"github.com/cloudowski/exitctl/internal/graph"
)

// buildGraph assembles a graph by hand (sorted nodes, index, adjacency) so the
// tests do not depend on the graph builder.
func buildGraph(nodes []*graph.Node, edges []*graph.Edge) *graph.Graph {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	g := &graph.Graph{Nodes: nodes, Edges: edges, Index: map[string]int{}}
	for i, n := range nodes {
		g.Index[n.ID] = i
	}
	g.Out = make([][]int, len(nodes))
	g.In = make([][]int, len(nodes))
	for _, e := range edges {
		f, t := g.Index[e.From], g.Index[e.To]
		g.Out[f] = append(g.Out[f], t)
		g.In[t] = append(g.In[t], f)
	}
	for i := range nodes {
		sort.Ints(g.Out[i])
		sort.Ints(g.In[i])
	}
	return g
}

func deps(m map[string]int) func(string) int {
	return func(id string) int { return m[id] }
}

func svc(id, typ string, fs ...graph.Finding) *graph.Node {
	return &graph.Node{ID: id, Kind: graph.NodeResource, Class: graph.ClassService, Type: typ, Findings: fs}
}

func unknown(id, typ string) *graph.Node {
	return &graph.Node{ID: id, Kind: graph.NodeResource, Class: graph.ClassUnknown, Type: typ}
}

func fnd(trait string, lvl catalog.Level, dims ...catalog.Dimension) graph.Finding {
	return graph.Finding{Trait: trait, Level: lvl, Dimensions: dims, Status: graph.FindingActive}
}

func dimVal(t *testing.T, a *Analysis, d catalog.Dimension) int {
	t.Helper()
	p := a.Dimensions[d]
	if p == nil {
		t.Fatalf("dimension %s is nil", d)
	}
	return *p
}

func TestScoreSingleNode(t *testing.T) {
	g := buildGraph([]*graph.Node{svc("a", "aws_t", fnd("x", catalog.LevelCritical, catalog.DimData))}, nil)
	a := Score(g, deps(nil), config.DefaultScoring())
	if a.Status != StatusOK || a.Index != 100 || a.Grade != "E" || a.Confidence != ConfidenceNormal {
		t.Fatalf("got %+v", a)
	}
	if a.ShowRange || a.Lower != 100 || a.Upper != 100 {
		t.Fatalf("range: %+v", a)
	}
	if dimVal(t, a, catalog.DimData) != 100 || dimVal(t, a, catalog.DimTechnical) != 0 {
		t.Fatalf("dims wrong")
	}
	if a.Dimensions[catalog.DimEconomic] != nil {
		t.Fatalf("economic must be nil")
	}
	ns := a.Nodes["a"]
	if ns.Risk != 1 || ns.Weight != 1 || ns.Contribution != 1 || ns.Dependents != 0 {
		t.Fatalf("node %+v", ns)
	}
	if a.Coverage != (Coverage{Types: 1, Resources: 1, Weighted: 1}) {
		t.Fatalf("coverage %+v", a.Coverage)
	}
}

func TestScoreSingleNodeHighTechnical(t *testing.T) {
	// lw 0.8 * dw 0.9 = 0.72, norm 1 -> index 72 (E); r_technical = lw = 0.8.
	g := buildGraph([]*graph.Node{svc("a", "aws_t", fnd("x", catalog.LevelHigh, catalog.DimTechnical))}, nil)
	a := Score(g, deps(nil), config.DefaultScoring())
	if a.Index != 72 || a.Grade != "E" {
		t.Fatalf("got index %d grade %s", a.Index, a.Grade)
	}
	if dimVal(t, a, catalog.DimTechnical) != 80 || dimVal(t, a, catalog.DimData) != 0 {
		t.Fatalf("dims wrong")
	}
	if a.Nodes["a"].Risk != 0.72 {
		t.Fatalf("risk %v", a.Nodes["a"].Risk)
	}
}

// diamond: a -> b, a -> c, b -> d, c -> d (From depends on To).
// D: d=3, b=1, c=1, a=0 -> w: d=3, b=2, c=2, a=1; Σw = 8.
// r: d critical data = 1; b medium operational = 0.35; c none = 0;
// a low skills = 0.125. Σwr = 3 + 0.7 + 0 + 0.125 = 3.825.
// index = 382.5/8 = 47.8125 -> 48 (C).
// data = 100*3/8 = 37.5 -> 38; operational = 100*1/8 = 12.5 -> 13;
// skills = 100*0.25/8 = 3.125 -> 3; technical 0.
func diamond() (*graph.Graph, map[string]int) {
	nodes := []*graph.Node{
		svc("a", "t_a", fnd("ta", catalog.LevelLow, catalog.DimSkills)),
		svc("b", "t_b", fnd("tb", catalog.LevelMedium, catalog.DimOperational)),
		svc("c", "t_c"),
		svc("d", "t_d", fnd("td", catalog.LevelCritical, catalog.DimData)),
	}
	edges := []*graph.Edge{
		{From: "a", To: "b", Kind: graph.EdgeReference},
		{From: "a", To: "c", Kind: graph.EdgeReference},
		{From: "b", To: "d", Kind: graph.EdgeReference},
		{From: "c", To: "d", Kind: graph.EdgeReference},
	}
	return buildGraph(nodes, edges), map[string]int{"a": 0, "b": 1, "c": 1, "d": 3}
}

func TestScoreDiamond(t *testing.T) {
	g, d := diamond()
	a := Score(g, deps(d), config.DefaultScoring())
	if a.Index != 48 || a.Grade != "D" || a.Confidence != ConfidenceNormal {
		t.Fatalf("got %d %s %s", a.Index, a.Grade, a.Confidence)
	}
	want := map[catalog.Dimension]int{catalog.DimData: 38, catalog.DimOperational: 13, catalog.DimSkills: 3, catalog.DimTechnical: 0}
	for dim, v := range want {
		if got := dimVal(t, a, dim); got != v {
			t.Errorf("dim %s = %d want %d", dim, got, v)
		}
	}
	wantW := map[string]float64{"a": 1, "b": 2, "c": 2, "d": 3}
	wantC := map[string]float64{"a": 0.125, "b": 0.7, "c": 0, "d": 3}
	for id, w := range wantW {
		if a.Nodes[id].Weight != w || a.Nodes[id].Contribution != wantC[id] {
			t.Errorf("node %s: %+v", id, a.Nodes[id])
		}
	}
	if fmt.Sprint(a.Scored) != "[a b c d]" {
		t.Errorf("scored %v", a.Scored)
	}
	idx, ok := IndexWith(g, deps(d), config.DefaultScoring(), nil)
	if !ok || idx != a.Index {
		t.Errorf("IndexWith = %d %v", idx, ok)
	}
}

func TestScoreIndexWithReplacedFindings(t *testing.T) {
	// Replace d's critical data finding with low data: r(d) = 0.25.
	// Σwr = 0.75 + 0.7 + 0.125 = 1.575 -> 157.5/8 = 19.6875 -> 20.
	g, d := diamond()
	idx, ok := IndexWith(g, deps(d), config.DefaultScoring(), func(n *graph.Node) []graph.Finding {
		if n.ID == "d" {
			return []graph.Finding{fnd("td", catalog.LevelLow, catalog.DimData)}
		}
		return n.Findings
	})
	if !ok || idx != 20 {
		t.Fatalf("got %d %v", idx, ok)
	}
}

func TestScoreUnknownBounds(t *testing.T) {
	// x (critical data, D=0, w=1, r=1), y (no findings, w=1), z unknown with
	// one dependent (x -> z), w=2. Σ_S w = 2, Σ_S wr = 1, Σ_U w = 2.
	// index = 50; lower = 100*1/4 = 25; upper = 100*3/4 = 75; coverage_w = 0.5.
	nodes := []*graph.Node{
		svc("x", "aws_x", fnd("t", catalog.LevelCritical, catalog.DimData)),
		svc("y", "aws_y"),
		unknown("z", "foo_z"),
		unknown("z2", "foo_z"), // same unknown type: one distinct type
	}
	edges := []*graph.Edge{{From: "x", To: "z", Kind: graph.EdgeReference}}
	g := buildGraph(nodes, edges)
	a := Score(g, deps(map[string]int{"z": 1}), config.DefaultScoring())
	// Σ_U w = 2 (z) + 1 (z2) = 3; denominators = 5.
	// lower = 100/5 = 20; upper = 100*(1+3)/5 = 80; coverage_w = 0.4.
	if a.Index != 50 || a.Lower != 20 || a.Upper != 80 || !a.ShowRange {
		t.Fatalf("got index %d range %d-%d show %v", a.Index, a.Lower, a.Upper, a.ShowRange)
	}
	if a.Confidence != ConfidenceWithheld || a.Grade != "" {
		t.Fatalf("confidence %s grade %q", a.Confidence, a.Grade)
	}
	if a.Coverage.Weighted != 0.4 || a.Coverage.Resources != 0.5 || a.Coverage.UnknownTypes != 1 {
		t.Fatalf("coverage %+v", a.Coverage)
	}
	// types: known {aws_x, aws_y}, all {aws_x, aws_y, foo_z} -> 0.6667.
	if a.Coverage.Types != 0.6667 {
		t.Fatalf("types %v", a.Coverage.Types)
	}
	if a.Nodes["z"].Weight != 2 || a.Nodes["z"].Risk != 0 {
		t.Fatalf("unknown node %+v", a.Nodes["z"])
	}
	if len(a.Scored) != 2 {
		t.Fatalf("scored %v", a.Scored)
	}
}

func TestScoreUnknownBoundsExact(t *testing.T) {
	// x (r=1, w=1), y (r=0, w=1), z unknown w=2: lower 25, upper 75, cov 0.5.
	nodes := []*graph.Node{
		svc("x", "aws_x", fnd("t", catalog.LevelCritical, catalog.DimData)),
		svc("y", "aws_y"),
		{ID: "z", Kind: graph.NodeResource, Class: graph.ClassUnverified, Type: "aws_z"},
	}
	g := buildGraph(nodes, []*graph.Edge{{From: "x", To: "z"}})
	a := Score(g, deps(map[string]int{"z": 1}), config.DefaultScoring())
	if a.Index != 50 || a.Lower != 25 || a.Upper != 75 || a.Coverage.Weighted != 0.5 {
		t.Fatalf("got %+v", a)
	}
}

func coverageGraph(nS, nU int) *graph.Graph {
	var nodes []*graph.Node
	for i := 0; i < nS; i++ {
		nodes = append(nodes, svc(fmt.Sprintf("s%02d", i), "aws_s", fnd("t", catalog.LevelMedium, catalog.DimData)))
	}
	for i := 0; i < nU; i++ {
		nodes = append(nodes, unknown(fmt.Sprintf("u%02d", i), "foo"))
	}
	return buildGraph(nodes, nil)
}

func TestScoreCoverageThresholds(t *testing.T) {
	cases := []struct {
		s, u  int
		conf  string
		grade string
	}{
		{10, 0, ConfidenceNormal, "D"},
		{9, 1, ConfidenceNormal, "D"},      // 0.90 exactly
		{8, 1, ConfidenceProvisional, "D"}, // 0.8889
		{7, 3, ConfidenceProvisional, "D"}, // 0.70 exactly
		{6, 3, ConfidenceWithheld, ""},     // 0.6667
	}
	for _, c := range cases {
		a := Score(coverageGraph(c.s, c.u), deps(nil), config.DefaultScoring())
		if a.Index != 50 || a.Confidence != c.conf || a.Grade != c.grade {
			t.Errorf("%d/%d: index %d conf %s grade %q", c.s, c.u, a.Index, a.Confidence, a.Grade)
		}
	}
}

func TestScoreHalfUp(t *testing.T) {
	// r = 0.25 (low data) and r = 0: 100*0.25/2 = 12.5 -> 13.
	g := buildGraph([]*graph.Node{
		svc("a", "t", fnd("x", catalog.LevelLow, catalog.DimData)),
		svc("b", "t"),
	}, nil)
	a := Score(g, deps(nil), config.DefaultScoring())
	if a.Index != 13 || a.Grade != "B" {
		t.Fatalf("got %d %s", a.Index, a.Grade)
	}
	if roundHalfUp(37.5) != 38 || roundHalfUp(37.4999) != 37 || roundHalfUp(0.5) != 1 {
		t.Fatal("roundHalfUp")
	}
	if Round4(2.0/3.0) != 0.6667 || Round4(1.0/3.0) != 0.3333 {
		t.Fatal("Round4 thirds")
	}
}

func TestScoreNothingScored(t *testing.T) {
	g := buildGraph([]*graph.Node{
		unknown("u", "foo"),
		{ID: "app", Kind: graph.NodeComponent, Class: graph.ClassComponent},
	}, nil)
	a := Score(g, deps(nil), config.DefaultScoring())
	if a.Status != StatusNothingScored || a.Grade != "" || a.Index != 0 || len(a.Scored) != 0 {
		t.Fatalf("got %+v", a)
	}
	if a.Coverage.Resources != 0 || a.Coverage.Types != 0 || a.Coverage.Weighted != 0 {
		t.Fatalf("coverage %+v", a.Coverage)
	}
	if _, ok := IndexWith(g, deps(nil), config.DefaultScoring(), nil); ok {
		t.Fatal("IndexWith must report not ok")
	}
	empty := Score(buildGraph(nil, nil), deps(nil), config.DefaultScoring())
	if empty.Status != StatusNothingScored || empty.Coverage.Resources != 1 || empty.Coverage.Types != 1 {
		t.Fatalf("empty %+v", empty)
	}
}

func TestScoreCustomWeights(t *testing.T) {
	// Technical weight 0.5: lw 0.8 * 0.5 = 0.4, norm 1*1 -> 40 (D).
	g := buildGraph([]*graph.Node{svc("a", "t", fnd("x", catalog.LevelHigh, catalog.DimTechnical))}, nil)
	cfg := config.DefaultScoring()
	cfg.DimensionWeights[catalog.DimTechnical] = 0.5
	a := Score(g, deps(nil), cfg)
	if a.Index != 40 || a.Grade != "D" {
		t.Fatalf("got %d %s", a.Index, a.Grade)
	}

	// Critical weight 0.8 (max level weight): norm = 0.8; critical data -> 1.
	// Diamond with these weights: r(d) = 1, r(b) = 0.5*0.7/0.8 = 0.4375,
	// r(a) = 0.25*0.5/0.8 = 0.15625. Σwr = 3 + 0.875 + 0.15625 = 4.03125
	// -> 403.125/8 = 50.390625 -> 50 (default 48).
	cfg2 := config.DefaultScoring()
	cfg2.LevelWeights[catalog.LevelCritical] = 0.8
	gd, d := diamond()
	a2 := Score(gd, deps(d), cfg2)
	if a2.Index != 50 {
		t.Fatalf("custom levels index %d", a2.Index)
	}

	// Economic weight raised to 1: economic dimension now gets a score.
	cfg3 := config.DefaultScoring()
	cfg3.DimensionWeights[catalog.DimEconomic] = 1
	g3 := buildGraph([]*graph.Node{svc("a", "t", fnd("x", catalog.LevelMedium, catalog.DimEconomic))}, nil)
	a3 := Score(g3, deps(nil), cfg3)
	if a3.Index != 50 || dimVal(t, a3, catalog.DimEconomic) != 50 {
		t.Fatalf("economic %d", a3.Index)
	}
}

func TestScoreSetMembership(t *testing.T) {
	// Accepted findings do not count; components are never scored; implied
	// services and neutral resources are scored.
	acc := fnd("x", catalog.LevelCritical, catalog.DimData)
	acc.Status = graph.FindingAccepted
	g := buildGraph([]*graph.Node{
		svc("a", "t", acc),
		{ID: "n", Kind: graph.NodeResource, Class: graph.ClassNeutral, Type: "t_n"},
		{ID: "i", Kind: graph.NodeImpliedService, Class: graph.ClassService, Findings: []graph.Finding{fnd("y", catalog.LevelCritical, catalog.DimData)}},
		{ID: "c", Kind: graph.NodeComponent, Class: graph.ClassComponent, Findings: []graph.Finding{fnd("z", catalog.LevelCritical, catalog.DimData)}},
	}, nil)
	a := Score(g, deps(nil), config.DefaultScoring())
	// S = {a, i, n}: Σwr = 1 -> 100/3 = 33.33 -> 33.
	if fmt.Sprint(a.Scored) != "[a i n]" || a.Index != 33 {
		t.Fatalf("scored %v index %d", a.Scored, a.Index)
	}
	if _, ok := a.Nodes["c"]; ok {
		t.Fatal("component scored")
	}
	// Implied services are not resources: types/resources coverage covers a, n.
	if a.Coverage.Resources != 1 || a.Coverage.Types != 1 {
		t.Fatalf("coverage %+v", a.Coverage)
	}
}

func TestScoreDeterministicLog2Weights(t *testing.T) {
	// D = 2 -> w = 1 + log2(3) = 2.5850; high data r = 0.8 -> contrib 2.068.
	g := buildGraph([]*graph.Node{svc("a", "t", fnd("x", catalog.LevelHigh, catalog.DimData))}, nil)
	a := Score(g, deps(map[string]int{"a": 2}), config.DefaultScoring())
	ns := a.Nodes["a"]
	if ns.Weight != 2.585 || ns.Contribution != 2.068 || a.Index != 80 {
		t.Fatalf("got %+v index %d", ns, a.Index)
	}
}
