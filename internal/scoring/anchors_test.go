package scoring

import (
	"testing"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/config"
	"github.com/cloudowski/exitctl/internal/graph"
	"github.com/cloudowski/exitctl/internal/model"
)

func ev(repo, path string, line int) model.Evidence {
	return model.Evidence{Repo: repo, Path: path, Line: line}
}

// anchorGraph is the diamond plus two components using d via SDK:
// app1 uses d with evidence in two files (one repeated), app2 uses d with one
// more file and b with one file; app1 also references d (not usage).
func anchorGraph() (*graph.Graph, map[string]int) {
	nodes := []*graph.Node{
		svc("a", "t_a", fnd("ta", catalog.LevelLow, catalog.DimSkills)),
		svc("b", "t_b", fnd("tb", catalog.LevelMedium, catalog.DimOperational)),
		svc("c", "t_c"),
		svc("d", "t_d",
			fnd("change-stream", catalog.LevelCritical, catalog.DimData),
			fnd("proprietary-sdk", catalog.LevelHigh, catalog.DimTechnical),
			fnd("iam", catalog.LevelMedium, catalog.DimOperational)),
		{ID: "app1", Kind: graph.NodeComponent, Class: graph.ClassComponent},
		{ID: "app2", Kind: graph.NodeComponent, Class: graph.ClassComponent},
	}
	nodes[3].Service = "aws.dynamodb"
	edges := []*graph.Edge{
		{From: "a", To: "b", Kind: graph.EdgeReference},
		{From: "a", To: "c", Kind: graph.EdgeReference},
		{From: "b", To: "d", Kind: graph.EdgeReference},
		{From: "c", To: "d", Kind: graph.EdgeReference},
		{From: "app1", To: "d", Kind: graph.EdgeUsage, Evidence: []model.Evidence{ev("r", "x.go", 1), ev("r", "x.go", 9), ev("r", "y.go", 2)}},
		{From: "app1", To: "d", Kind: graph.EdgeReference, Evidence: []model.Evidence{ev("r", "z.go", 1)}},
		{From: "app2", To: "d", Kind: graph.EdgeUsage, Evidence: []model.Evidence{ev("r2", "x.go", 1)}},
		{From: "app2", To: "b", Kind: graph.EdgeUsage, Evidence: []model.Evidence{ev("r2", "b.go", 1)}},
	}
	return buildGraph(nodes, edges), map[string]int{"a": 2, "b": 3, "c": 3, "d": 5, "app1": 0, "app2": 0}
}

func TestAnchorsWithoutIndex(t *testing.T) {
	g, d := anchorGraph()
	a := Score(g, deps(d), config.DefaultScoring())
	got := Anchors(g, a, nil)
	// w: d = 1+log2(6) = 3.585 (r 1), b = 3 (r 0.35 -> 1.05),
	// a = 1+log2(3) = 2.585 (r 0.125 -> 0.3231); c has r 0 and is excluded.
	if len(got) != 3 {
		t.Fatalf("anchors %+v", got)
	}
	wantOrder := []string{"d", "b", "a"}
	for i, id := range wantOrder {
		if got[i].Node != id || got[i].Rank != i+1 {
			t.Errorf("rank %d: %+v", i+1, got[i])
		}
	}
	top := got[0]
	if top.Contribution != 3.585 || top.DependentsCount != 5 || top.ComponentsUsing != 2 || top.SDKFiles != 3 {
		t.Errorf("top %+v", top)
	}
	if top.Reason != "change-stream + proprietary-sdk + 3 SDK files + 5 dependent resources" {
		t.Errorf("reason %q", top.Reason)
	}
	if got[1].ComponentsUsing != 1 || got[1].SDKFiles != 1 || got[1].Reason != "tb + 1 SDK file + 3 dependent resources" {
		t.Errorf("b %+v", got[1])
	}
	if got[2].Reason != "ta + 2 dependent resources" || got[2].Contribution != 0.3231 {
		t.Errorf("a %+v", got[2])
	}
}

func TestAnchorsTieBreakByID(t *testing.T) {
	g := buildGraph([]*graph.Node{
		svc("z", "t", fnd("x", catalog.LevelHigh, catalog.DimData)),
		svc("m", "t", fnd("x", catalog.LevelHigh, catalog.DimData)),
	}, nil)
	got := Anchors(g, Score(g, deps(nil), config.DefaultScoring()), nil)
	if len(got) != 2 || got[0].Node != "m" || got[1].Node != "z" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Reason != "x" {
		t.Fatalf("reason %q", got[0].Reason)
	}
}

// TestAnchorsWithIndex needs catalog.NewIndex (task T020).
func TestAnchorsWithIndex(t *testing.T) {
	verified := catalog.Provenance{Origin: "maintainer", Status: catalog.StatusVerified}
	cat := &catalog.Catalog{
		Providers: []catalog.Provider{{ID: "aws", Name: "AWS", Kind: "hyperscaler"}},
		Traits: []catalog.Trait{
			{ID: "change-stream", Name: "Managed change stream", Level: catalog.LevelCritical, Dimensions: []catalog.Dimension{catalog.DimData}, Provenance: verified},
			{ID: "proprietary-sdk", Name: "Proprietary SDK", Level: catalog.LevelHigh, Dimensions: []catalog.Dimension{catalog.DimTechnical}, Provenance: verified},
		},
		Alternatives: []catalog.Alternative{
			{ID: "scylla", Name: "ScyllaDB", Kind: "oss-self-hosted", Provenance: verified},
			{ID: "mongo", Name: "MongoDB", Kind: "oss-self-hosted", Provenance: verified},
		},
		Services: []catalog.Service{{
			ID: "aws.dynamodb", Provider: "aws", Name: "DynamoDB",
			Alternatives: []catalog.AlternativeMapping{
				{Ref: "mongo", Difficulty: "high"},
				{Ref: "scylla", Difficulty: "medium", Recommended: true},
			},
			Provenance: verified,
		}},
	}
	ix := catalog.NewIndex(cat, catalog.IndexOptions{})
	g, d := anchorGraph()
	got := Anchors(g, Score(g, deps(d), config.DefaultScoring()), ix)
	top := got[0]
	if top.Reason != "Managed change stream + Proprietary SDK + 3 SDK files + 5 dependent resources" {
		t.Errorf("reason %q", top.Reason)
	}
	if len(top.Alternatives) != 2 || top.Alternatives[0].Ref != "scylla" {
		t.Errorf("alternatives %+v", top.Alternatives)
	}
}
