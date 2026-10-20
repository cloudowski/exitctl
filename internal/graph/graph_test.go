package graph

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/model"
)

// ---------------------------------------------------------------- fixtures

func ev(repo, path string, line int, excerpt string, kind model.SourceKind) model.Evidence {
	return model.NewEvidence(repo, path, line, excerpt, kind)
}

func tfNode(repo, dir, addr, typ, pi string, attrs map[string]any, line int, excerpt string) model.RawNode {
	if attrs == nil {
		attrs = map[string]any{}
	}
	return model.RawNode{
		ID:               repo + "//" + dir + "#" + addr,
		NodeKind:         model.NodeResource,
		SourceKind:       model.KindTerraform,
		Type:             typ,
		NativeAddress:    addr,
		ProviderIdentity: pi,
		Attributes:       attrs,
		Multiplicity:     model.MultOne,
		Evidence:         []model.Evidence{ev(repo, dir+"/main.tf", line, excerpt, model.KindTerraform)},
	}
}

func k8sNode(repo, dir, file string, kind model.SourceKind, typ, ns, name string, attrs map[string]any) model.RawNode {
	addr := typ + "/" + ns + "/" + name
	if attrs == nil {
		attrs = map[string]any{}
	}
	return model.RawNode{
		ID:            repo + "//" + dir + "#" + addr,
		NodeKind:      model.NodeResource,
		SourceKind:    kind,
		Type:          typ,
		NativeAddress: addr,
		Attributes:    attrs,
		Multiplicity:  model.MultOne,
		Evidence:      []model.Evidence{ev(repo, dir+"/"+file, 1, "kind: "+typ, kind)},
	}
}

func component(repo, dir string) model.RawNode {
	return model.RawNode{
		ID:            repo + "//" + dir + "#component",
		NodeKind:      model.NodeComponent,
		SourceKind:    model.KindPackageManifest,
		Type:          model.TypeComponent,
		NativeAddress: "component",
		Attributes:    map[string]any{},
		Multiplicity:  model.MultOne,
		Evidence:      []model.Evidence{ev(repo, dir+"/package.json", 1, "{", model.KindPackageManifest)},
	}
}

func verified() catalog.Provenance {
	return catalog.Provenance{Origin: "maintainer", Author: "t", ReviewedBy: "t", ReviewedOn: "2026-10-06", Status: catalog.StatusVerified}
}

func trait(id string, lvl catalog.Level, dims ...catalog.Dimension) catalog.Trait {
	return catalog.Trait{ID: id, Name: id, Level: lvl, Dimensions: dims, Explanation: "x", ExitHint: "y", Provenance: verified()}
}

func alt() []catalog.AlternativeMapping {
	return []catalog.AlternativeMapping{{Ref: "oss", Difficulty: "medium", Compatibility: "conceptual", ApprovedBy: "t"}}
}

func testCatalog() *catalog.Catalog {
	yes := true
	return &catalog.Catalog{
		Manifest:   catalog.Manifest{Schema: "1.0", Kind: "catalog", CatalogVersion: "2026.10.1", Title: "test", Maintainers: []string{"t"}},
		Providers:  []catalog.Provider{{ID: "aws", Name: "AWS", Kind: "hyperscaler"}, {ID: "kubernetes", Name: "Kubernetes", Kind: "generic"}},
		Categories: []catalog.Category{{ID: "database", Name: "Database"}, {ID: "database.nosql", Name: "NoSQL"}, {ID: "messaging", Name: "Messaging"}, {ID: "identity", Name: "Identity"}, {ID: "network", Name: "Network"}},
		Traits: []catalog.Trait{
			trait("proprietary-api", catalog.LevelHigh, catalog.DimTechnical),
			trait("proprietary-sdk", catalog.LevelMedium, catalog.DimTechnical, catalog.DimSkills),
			trait("proprietary-iam-model", catalog.LevelMedium, catalog.DimOperational),
			trait("provider-network-construct", catalog.LevelLow, catalog.DimOperational),
		},
		Alternatives: []catalog.Alternative{{ID: "oss", Name: "OSS", Kind: "oss-self-hosted", License: "Apache-2.0", Homepage: "https://example.org", Provenance: verified()}},
		Services: []catalog.Service{
			{
				ID: "aws.dynamodb", Provider: "aws", Name: "DynamoDB", Category: "database.nosql",
				ResourceTypes: catalog.ResourceTypes{Terraform: []string{"aws_dynamodb_table"}},
				Traits:        []catalog.TraitRef{{ID: "proprietary-api", Level: catalog.LevelCritical, Reason: "test override"}},
				SDK:           []catalog.SdkPattern{{Ecosystem: "npm", Packages: []string{"@aws-sdk/client-dynamodb"}}},
				Alternatives:  alt(), Provenance: verified(),
			},
			{
				ID: "aws.sqs", Provider: "aws", Name: "SQS", Category: "messaging",
				ResourceTypes: catalog.ResourceTypes{Terraform: []string{"aws_sqs_queue"}},
				Traits:        []catalog.TraitRef{{ID: "proprietary-api"}},
				SDK:           []catalog.SdkPattern{{Ecosystem: "npm", Packages: []string{"@aws-sdk/client-sqs"}}},
				Alternatives:  alt(), Provenance: verified(),
			},
			{
				ID: "aws.iam", Provider: "aws", Name: "IAM", Category: "identity",
				ResourceTypes:      catalog.ResourceTypes{Terraform: []string{"aws_iam_role"}},
				Traits:             []catalog.TraitRef{{ID: "proprietary-iam-model"}},
				KubernetesBindings: []catalog.Binding{{Match: catalog.BindingMatch{Kind: "ServiceAccount", Annotation: "eks.amazonaws.com/role-arn", Exists: &yes}}},
				Alternatives:       alt(), Provenance: verified(),
			},
			{
				ID: "aws.vpc", Provider: "aws", Name: "VPC", Category: "network",
				ResourceTypes: catalog.ResourceTypes{Terraform: []string{"aws_vpc", "aws_subnet"}},
				Traits:        []catalog.TraitRef{{ID: "provider-network-construct"}},
				EdgeHints:     []catalog.EdgeHint{{ResourceType: "aws_subnet", Attribute: "vpc_id", EdgeKind: "containment"}},
				Alternatives:  alt(), Provenance: verified(),
			},
		},
		Neutrals: []catalog.Neutral{{
			ID: "kubernetes-core", Provider: "kubernetes",
			ResourceTypes: catalog.ResourceTypes{Kubernetes: []catalog.GroupKind{{Group: "", Kind: "*"}, {Group: "apps", Kind: "*"}}},
			Provenance:    verified(),
		}},
	}
}

func edgeIDs(g *Graph) []string {
	var out []string
	for _, e := range g.Edges {
		out = append(out, e.From+" -> "+e.To+" ["+string(e.Kind)+"]")
	}
	return out
}

func findEdge(g *Graph, from, to string, kind EdgeKind) *Edge {
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Kind == kind {
			return e
		}
	}
	return nil
}

// ---------------------------------------------------------------- no catalog

func TestBuildNoCatalogIdentityMerge(t *testing.T) {
	a1 := tfNode("r", "infra", "aws_sqs_queue.q", "aws_sqs_queue", "", nil, 3, `resource "aws_sqs_queue" "q" {`)
	a2 := a1
	a2.Evidence = []model.Evidence{ev("r", "infra/other.tf", 9, "q", model.KindTerraform)}
	a2.Attributes = map[string]any{"extra": "v"}

	p1 := tfNode("r", "infra", "aws_dynamodb_table.orders", "aws_dynamodb_table", "aws:dynamodb:table/orders", nil, 1, "t")
	p2 := model.RawNode{
		ID: "r//cfn#Orders", NodeKind: model.NodeResource, SourceKind: model.KindCloudFormation,
		Type: "AWS::DynamoDB::Table", NativeAddress: "Orders", ProviderIdentity: "aws:dynamodb:table/orders",
		Attributes: map[string]any{}, Multiplicity: model.MultUnknown,
		Evidence: []model.Evidence{ev("r", "cfn/t.yaml", 4, "Orders:", model.KindCloudFormation)},
	}
	chart := k8sNode("r", "charts/app", "templates/deploy.yaml", model.KindHelm, "apps/Deployment", "default", "web", nil)
	raw := k8sNode("r", "k8s", "deploy.yaml", model.KindKubernetes, "apps/Deployment", "default", "web", nil)
	other := k8sNode("q", "k8s", "deploy.yaml", model.KindKubernetes, "apps/Deployment", "default", "web", nil)

	g := Build(model.Facts{RawNodes: []model.RawNode{raw, a2, p2, chart, a1, p1, other}}, nil)

	if len(g.Nodes) != 4 {
		t.Fatalf("nodes = %d, want 4: %v", len(g.Nodes), g.Index)
	}
	q := g.Node(a1.ID)
	if q == nil || len(q.Sources) != 2 || q.Attributes["extra"] != "v" || q.Evidence[0].Path != "infra/main.tf" {
		t.Errorf("same-ID merge wrong: %+v", q)
	}
	d := g.Node("r//cfn#Orders")
	if d == nil || d.ID != "r//cfn#Orders" || !reflect.DeepEqual(d.Aliases, []string{p1.ID}) {
		t.Fatalf("provider identity merge wrong: %+v", d)
	}
	if d.Multiplicity != model.MultUnknown || len(d.SourceKinds) != 2 || d.ProviderIdentity != "aws:dynamodb:table/orders" {
		t.Errorf("merged fields wrong: %+v", d)
	}
	w := g.Node(chart.ID)
	if w == nil || w.ID != chart.ID || !reflect.DeepEqual(w.Aliases, []string{raw.ID}) || g.Node(raw.ID) != w {
		t.Errorf("k8s chart+manifest merge wrong: %+v", w)
	}
	if g.Node(other.ID) == w {
		t.Error("k8s objects of different repos must not merge")
	}
	// Unknown types aggregated per (repo, type).
	var gaps []string
	for _, gp := range g.Gaps {
		gaps = append(gaps, string(gp.Kind)+":"+gp.Repo+":"+gp.Subject+":"+string(rune('0'+gp.Count)))
	}
	wantGaps := []string{
		"unknown_type:r:AWS::DynamoDB::Table:1",
		"unknown_type:q:apps/Deployment:1",
		"unknown_type:r:apps/Deployment:1",
		"unknown_type:r:aws_sqs_queue:1",
	}
	if !reflect.DeepEqual(gaps, wantGaps) {
		t.Errorf("gaps = %v, want %v", gaps, wantGaps)
	}
}

func TestBuildNoCatalogEdges(t *testing.T) {
	a := tfNode("r", "m", "aws_lambda_function.f", "aws_lambda_function", "", nil, 1, "f")
	b := tfNode("r", "m", "aws_sqs_queue.q", "aws_sqs_queue", "", nil, 2, "q")
	c := tfNode("r", "m", "aws_iam_role.x", "aws_iam_role", "", nil, 3, "x")
	e1 := model.RawEdge{From: a.ID, To: b.ID, Attribute: "queue", Evidence: []model.Evidence{ev("r", "m/main.tf", 5, "q1", model.KindTerraform)}}
	e2 := model.RawEdge{From: a.ID, To: b.ID, Attribute: "queue2", Evidence: []model.Evidence{ev("r", "m/main.tf", 6, "q2", model.KindTerraform)}}
	e3 := model.RawEdge{From: a.ID, To: c.ID, KindHint: "ordering", Evidence: []model.Evidence{ev("r", "m/main.tf", 7, "depends_on", model.KindTerraform)}}
	self := model.RawEdge{From: a.ID, To: a.ID, Evidence: []model.Evidence{ev("r", "m/main.tf", 8, "self", model.KindTerraform)}}
	bad := model.RawEdge{From: a.ID, To: "r//m#module.gone", Evidence: []model.Evidence{ev("r", "m/main.tf", 9, "gone", model.KindTerraform)}}
	g := Build(model.Facts{RawNodes: []model.RawNode{a, b, c}, RawEdges: []model.RawEdge{e1, e2, e3, self, bad, bad}}, nil)

	want := []string{
		a.ID + " -> " + c.ID + " [ordering]",
		a.ID + " -> " + b.ID + " [reference]",
	}
	if got := edgeIDs(g); !reflect.DeepEqual(got, want) {
		t.Fatalf("edges = %v, want %v", got, want)
	}
	if e := findEdge(g, a.ID, b.ID, EdgeReference); len(e.Evidence) != 2 || e.Confidence != ConfExact {
		t.Errorf("duplicate edges not merged: %+v", e)
	}
	var unresolved *model.Gap
	for i := range g.Gaps {
		if g.Gaps[i].Kind == model.GapUnresolvedReference {
			unresolved = &g.Gaps[i]
		}
	}
	if unresolved == nil || unresolved.Subject != "r//m#module.gone" || unresolved.Count != 2 || unresolved.Path != "m/main.tf" {
		t.Errorf("unresolved gap = %+v", unresolved)
	}
	ia := g.Index[a.ID]
	if len(g.Out[ia]) != 2 || len(g.In[g.Index[b.ID]]) != 1 {
		t.Errorf("adjacency wrong: out=%v in=%v", g.Out, g.In)
	}
	if got := g.Dependents(b.ID); !reflect.DeepEqual(got, []string{a.ID}) {
		t.Errorf("Dependents = %v", got)
	}
}

func TestBuildNoCatalogDeterministic(t *testing.T) {
	a := tfNode("r", "m", "aws_sqs_queue.a", "aws_sqs_queue", "", nil, 1, "a")
	b := tfNode("r", "m", "aws_sqs_queue.b", "aws_sqs_queue", "", nil, 2, "b")
	facts := model.Facts{RawNodes: []model.RawNode{b, a}, RawEdges: []model.RawEdge{{From: a.ID, To: b.ID}}}
	g1 := Build(facts, nil)
	g2 := Build(facts, nil)
	g1.algo, g2.algo = nil, nil
	if !reflect.DeepEqual(g1, g2) {
		t.Error("Build is not deterministic")
	}
}

// ---------------------------------------------------------------- with catalog
// These tests need the real catalog index (task T020) and rules (T023).

func newIndex(t *testing.T) *catalog.Index {
	t.Helper()
	return catalog.NewIndex(testCatalog(), catalog.IndexOptions{})
}

func TestBuildCatalogClassificationAndEdgeHints(t *testing.T) {
	ix := newIndex(t)
	vpc := tfNode("r", "net", "aws_vpc.main", "aws_vpc", "", nil, 1, "vpc")
	sub := tfNode("r", "net", "aws_subnet.a", "aws_subnet", "", nil, 5, "subnet")
	unk := tfNode("r", "net", "foo_thing.x", "foo_thing", "", nil, 9, "foo")
	cm := k8sNode("r", "k8s", "cm.yaml", model.KindKubernetes, "/ConfigMap", "default", "cfg", nil)
	facts := model.Facts{
		RawNodes: []model.RawNode{vpc, sub, unk, cm},
		RawEdges: []model.RawEdge{{From: sub.ID, To: vpc.ID, Attribute: "vpc_id", Evidence: []model.Evidence{ev("r", "net/main.tf", 6, "vpc_id = aws_vpc.main.id", model.KindTerraform)}}},
	}
	g := Build(facts, ix)
	if e := findEdge(g, sub.ID, vpc.ID, EdgeContainment); e == nil {
		t.Errorf("edge hint not applied: %v", edgeIDs(g))
	}
	if n := g.Node(vpc.ID); n.Class != ClassService || n.Service != "aws.vpc" {
		t.Errorf("vpc classified as %s/%s", n.Class, n.Service)
	}
	if n := g.Node(cm.ID); n.Class != ClassNeutral || len(n.Findings) != 0 {
		t.Errorf("configmap classified as %s with %d findings", n.Class, len(n.Findings))
	}
	if n := g.Node(unk.ID); n.Class != ClassUnknown {
		t.Errorf("unknown classified as %s", n.Class)
	}
	f := g.Node(vpc.ID).Findings
	if len(f) != 1 || f[0].ID != vpc.ID+"::provider-network-construct" || f[0].Origin != "service" || len(f[0].Evidence) == 0 || f[0].Status != FindingActive {
		t.Errorf("vpc findings = %+v", f)
	}
}

func TestBuildCatalogUsageConfidenceAndOverride(t *testing.T) {
	ix := newIndex(t)
	orders := tfNode("r", "infra", "aws_dynamodb_table.orders", "aws_dynamodb_table", "aws:dynamodb:table/orders", nil, 1, "orders")
	users := tfNode("r", "infra", "aws_dynamodb_table.users", "aws_dynamodb_table", "aws:dynamodb:table/users", nil, 10, "users")
	comp := component("r", "svc")
	use := model.CodeUse{
		Component: comp.ID, Ecosystem: model.EcoNPM, PatternKind: "import", Service: "aws.dynamodb",
		Evidence: ev("r", "svc/db.ts", 12, `await client.send(new GetItemCommand({ TableName: "orders" }))`, model.KindCode),
	}
	g := Build(model.Facts{RawNodes: []model.RawNode{orders, users, comp}, CodeUses: []model.CodeUse{use}}, ix)

	eo := findEdge(g, comp.ID, orders.ID, EdgeUsage)
	eu := findEdge(g, comp.ID, users.ID, EdgeUsage)
	if eo == nil || eo.Confidence != ConfResourceLevel {
		t.Errorf("orders usage edge = %+v", eo)
	}
	if eu == nil || eu.Confidence != ConfServiceLevel {
		t.Errorf("users usage edge = %+v", eu)
	}
	for _, id := range []string{orders.ID, users.ID} {
		fs := g.Node(id).Findings
		if len(fs) != 2 || fs[0].Trait != "proprietary-api" || fs[0].Level != catalog.LevelCritical || fs[1].Trait != "proprietary-sdk" || fs[1].Origin != "sdk" {
			t.Errorf("%s findings = %+v", id, fs)
			continue
		}
		if fs[1].Evidence[0].Path != "svc/db.ts" {
			t.Errorf("sdk evidence = %+v", fs[1].Evidence)
		}
	}
	if g.DependentsCount(orders.ID) != 1 {
		t.Errorf("component should depend on orders")
	}
}

func TestBuildCatalogImpliedServiceFromPackage(t *testing.T) {
	ix := newIndex(t)
	comp := component("r", "svc")
	pu := model.PackageUse{Component: comp.ID, Ecosystem: model.EcoNPM, Name: "@aws-sdk/client-sqs", Manifest: ev("r", "svc/package.json", 7, `"@aws-sdk/client-sqs": "^3"`, model.KindPackageManifest)}
	cu := model.CodeUse{Component: comp.ID, Ecosystem: model.EcoNPM, PatternKind: "import", Service: "aws.sqs", Evidence: ev("r", "svc/q.ts", 1, `import { SQSClient } from "@aws-sdk/client-sqs"`, model.KindCode)}
	g := Build(model.Facts{RawNodes: []model.RawNode{comp}, PackageUses: []model.PackageUse{pu}, CodeUses: []model.CodeUse{cu}}, ix)

	n := g.Node(ImpliedPrefix + "aws.sqs")
	if n == nil || n.Kind != NodeImpliedService || n.Class != ClassService || n.Service != "aws.sqs" {
		t.Fatalf("implied node = %+v", n)
	}
	e := findEdge(g, comp.ID, n.ID, EdgeUsage)
	if e == nil || e.Confidence != ConfServiceLevel || len(e.Evidence) != 2 {
		t.Fatalf("usage edge = %+v (%v)", e, edgeIDs(g))
	}
	if len(n.Findings) != 1 || n.Findings[0].Trait != "proprietary-sdk" || n.Findings[0].Origin != "sdk" || len(n.Findings[0].Evidence) != 2 {
		t.Errorf("implied findings = %+v", n.Findings)
	}
	count := 0
	for _, x := range g.Nodes {
		if strings.HasPrefix(x.ID, ImpliedPrefix) {
			count++
		}
	}
	if count != 1 {
		t.Errorf("implied nodes = %d, want 1", count)
	}
}

func TestBuildCatalogBindingToImplied(t *testing.T) {
	ix := newIndex(t)
	sa := k8sNode("r", "k8s", "sa.yaml", model.KindKubernetes, "/ServiceAccount", "default", "app", map[string]any{
		"metadata": map[string]any{"annotations": map[string]any{"eks.amazonaws.com/role-arn": "arn:aws:iam::1:role/app"}},
	})
	sa.Evidence = append(sa.Evidence, ev("r", "k8s/sa.yaml", 6, "eks.amazonaws.com/role-arn: arn:aws:iam::1:role/app", model.KindKubernetes))
	plain := k8sNode("r", "k8s", "sa2.yaml", model.KindKubernetes, "/ServiceAccount", "default", "plain", nil)
	g := Build(model.Facts{RawNodes: []model.RawNode{sa, plain}}, ix)

	id := ImpliedPrefix + "aws.iam"
	e := findEdge(g, sa.ID, id, EdgeBinding)
	if e == nil || e.Evidence[0].Line != 6 {
		t.Fatalf("binding edge = %+v (%v)", e, edgeIDs(g))
	}
	if findEdge(g, plain.ID, id, EdgeBinding) != nil {
		t.Error("service account without annotation must not bind")
	}
	n := g.Node(id)
	if len(n.Findings) != 1 || n.Findings[0].Trait != "proprietary-iam-model" || n.Findings[0].Origin != "binding" {
		t.Errorf("implied iam findings = %+v", n.Findings)
	}
	if g.Node(sa.ID).Class != ClassNeutral || len(g.Node(sa.ID).Findings) != 0 {
		t.Errorf("service account must stay neutral without findings: %+v", g.Node(sa.ID))
	}
	// With a declared role in the same repo the binding targets it instead.
	role := tfNode("r", "infra", "aws_iam_role.app", "aws_iam_role", "", nil, 1, "role")
	g = Build(model.Facts{RawNodes: []model.RawNode{sa, role}}, ix)
	if findEdge(g, sa.ID, role.ID, EdgeBinding) == nil || g.Node(id) != nil {
		t.Errorf("binding should target the declared role: %v", edgeIDs(g))
	}
}

func TestBuildUnknownSDKGaps(t *testing.T) {
	ix := newIndex(t)
	comp := component("r", "svc")
	pkg := func(eco model.Ecosystem, name string, line int) model.PackageUse {
		return model.PackageUse{Component: comp.ID, Ecosystem: eco, Name: name, Manifest: ev("r", "svc/package.json", line, name, model.KindPackageManifest)}
	}
	boto := func(svc string, line int) model.CodeUse {
		return model.CodeUse{Component: comp.ID, Ecosystem: model.EcoPyPI, PatternKind: model.CodeKindSDKName, Match: svc, Evidence: ev("r", "svc/app.py", line, `boto3.client("`+svc+`")`, model.KindCode)}
	}
	facts := model.Facts{
		RawNodes: []model.RawNode{comp},
		PackageUses: []model.PackageUse{
			pkg(model.EcoNPM, "@aws-sdk/client-sqs", 1),     // mapped by the catalog
			pkg(model.EcoNPM, "@aws-sdk/client-kinesis", 2), // unmapped cloud SDK
			pkg(model.EcoNPM, "@aws-sdk/types", 3),          // shared, not a service
			pkg(model.EcoNPM, "express", 4),                 // not a cloud SDK
			pkg(model.EcoGo, "cloud.google.com/go/spanner", 5),
		},
		CodeUses: []model.CodeUse{
			boto("kinesis", 10),
			boto("kinesis", 11),
			boto("dynamodb", 12),
			// dynamodb at line 12 is also mapped by a catalog pattern
			{Component: comp.ID, Ecosystem: model.EcoPyPI, PatternKind: "usage", Service: "aws.dynamodb", Evidence: ev("r", "svc/app.py", 12, `boto3.client("dynamodb")`, model.KindCode)},
		},
	}
	g := Build(facts, ix)

	got := map[string]model.Gap{}
	for _, gp := range g.Gaps {
		if gp.Kind == model.GapUnknownSDK {
			got[gp.Subject] = gp
		}
	}
	want := map[string]model.Gap{
		"npm:@aws-sdk/client-kinesis":    {Provider: "aws", Service: "kinesis", Count: 1, Path: "svc/package.json"},
		"go:cloud.google.com/go/spanner": {Provider: "gcp", Service: "spanner", Count: 1, Path: "svc/package.json"},
		"boto3:kinesis":                  {Provider: "aws", Service: "kinesis", Count: 2, Path: "svc/app.py"},
	}
	if len(got) != len(want) {
		t.Fatalf("unknown_sdk gaps = %+v, want %d", got, len(want))
	}
	for subj, w := range want {
		gp, ok := got[subj]
		if !ok || gp.Provider != w.Provider || gp.Service != w.Service || gp.Count != w.Count || gp.Path != w.Path || gp.Repo != "r" {
			t.Errorf("%s = %+v (present %v), want %+v", subj, gp, ok, w)
		}
	}
}
