package terminal

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/result"
)

func covEv(path string, line int, kind model.SourceKind) []model.Evidence {
	return []model.Evidence{{Repo: "r", Path: path, Line: line, Excerpt: "x", SourceKind: kind}}
}

// coverageDoc has two unknown Terraform types (one with more dependents), an
// unknown CloudFormation type, an unverified match, every unresolved input
// kind, a failed and a partial source, and a parse failure gap whose subject
// carries raw parser text.
func coverageDoc() *result.Document {
	tf, cfn := model.KindTerraform, model.KindCloudFormation
	d := &result.Document{Inputs: []result.Input{{Repo: "r"}}}
	d.Facts.RawNodes = []model.RawNode{
		{ID: "w1", SourceKind: tf, Type: "acme_widget"},
		{ID: "w2", SourceKind: tf, Type: "acme_widget"},
		{ID: "b1", SourceKind: tf, Type: "exotic_bucket"},
		{ID: "c1", SourceKind: cfn, Type: "Acme::Thing"},
		{ID: "q1", SourceKind: tf, Type: "aws_sqs_queue"},
		{ID: "u1", SourceKind: tf, Type: "aws_new_thing"},
		{ID: "x1", SourceKind: tf, Type: "aws_lambda_function"},
		{ID: "x2", SourceKind: tf, Type: "aws_iam_role"},
	}
	d.Graph.Nodes = []result.Node{
		// Deliberately not sorted by ID.
		{ID: "x2", Kind: "resource", Type: "aws_iam_role", Class: "service"},
		{ID: "w2", Kind: "resource", Type: "acme_widget", Class: "unknown", NativeAddress: "acme_widget.b",
			Evidence: covEv("infra/w.tf", 9, tf), DependentsCount: 1},
		{ID: "w1", Kind: "resource", Type: "acme_widget", Class: "unknown", NativeAddress: "acme_widget.a",
			Evidence: covEv("infra/w.tf", 3, tf), DependentsCount: 3},
		{ID: "b1", Kind: "resource", Type: "exotic_bucket", Class: "unknown", NativeAddress: "exotic_bucket.a",
			Evidence: covEv("infra/b.tf", 1, tf)},
		{ID: "c1", Kind: "resource", Type: "Acme::Thing", Class: "unknown", NativeAddress: "Thing",
			Evidence: covEv("cfn/t.yaml", 4, cfn)},
		{ID: "u1", Kind: "resource", Type: "aws_new_thing", Class: "unverified", NativeAddress: "aws_new_thing.a",
			Evidence: covEv("infra/n.tf", 2, tf), Notes: []string{"unverified_match:aws.newthing"}},
		{ID: "q1", Kind: "resource", Type: "aws_sqs_queue", Class: "service"},
		{ID: "x1", Kind: "resource", Type: "aws_lambda_function", Class: "service"},
		{ID: "comp", Kind: "component", Type: "component", Class: "unknown"},
	}
	// w2 -> w1 (same type: not counted for the type), q1 -> w1, x1 -> q1, x2 -> w2.
	d.Graph.Edges = []result.Edge{
		{From: "w2", To: "w1", Kind: "reference"},
		{From: "q1", To: "w1", Kind: "reference"},
		{From: "x1", To: "q1", Kind: "reference"},
		{From: "x2", To: "w2", Kind: "reference"},
	}
	d.Facts.Gaps = []model.Gap{
		{Kind: model.GapUnresolvedReference, Subject: "r//infra#aws_iam_role.missing", Repo: "r", Path: "infra/app.tf", Count: 2},
		{Kind: model.GapRemoteKustomize, Subject: "github.com/org/base//x?ref=v1", Repo: "r", Path: "k8s/kustomization.yaml", Count: 1},
		{Kind: model.GapUnresolvedChart, Subject: "oci://charts.example/app", Repo: "r", Path: "helm/Chart.yaml", Count: 1},
		{Kind: model.GapUnresolvedModule, Subject: "terraform-aws-modules/vpc/aws", Repo: "r", Path: "infra/net.tf", Count: 1},
		{Kind: model.GapParseFailure, Subject: "infra/bad.tf:3,1-2: Argument or block definition required", Repo: "r", Path: "infra/bad.tf", Count: 1},
		{Kind: model.GapUnknownType, Subject: "acme_widget", Repo: "r", Count: 2},
	}
	d.Facts.Sources = []model.Source{
		{Repo: "r", Path: "infra/ok.tf", Kind: model.KindTerraform, Status: model.StatusParsed},
		{Repo: "r", Path: "infra/broken.tf", Kind: model.KindTerraform, Status: model.StatusFailed, Note: "could not be parsed as HCL"},
		{Repo: "r", Path: "infra/half.tf", Kind: model.KindTerraform, Status: model.StatusPartial, Note: "parsed with errors as HCL"},
	}
	d.Analysis = result.Analysis{
		Status: "ok", Index: ip(44), Confidence: "withheld", Range: &result.Range{Lower: 17, Upper: 79},
		Coverage: result.Coverage{Types: 0.5, Resources: 0.375, Weighted: 0.3796, UnknownTypes: 4,
			UnverifiedMatches: []string{"u1"}},
	}
	return d
}

func renderCoverage(t *testing.T, d *result.Document, o Options) string {
	t.Helper()
	var b bytes.Buffer
	if err := Coverage(&b, BuildCoverage(d), o); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestBuildCoverageUnknownTypes(t *testing.T) {
	v := BuildCoverage(coverageDoc())
	var got []string
	for _, r := range v.UnknownTypes {
		got = append(got, r.SourceKind+"/"+r.Type)
	}
	// Dependents desc, then type: acme_widget (q1, x1, x2) first; the rest
	// have none and sort by type.
	want := []string{"terraform/acme_widget", "cloudformation/Acme::Thing", "terraform/exotic_bucket"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
	aw := v.UnknownTypes[0]
	if aw.Resources != 2 || aw.Dependents != 3 {
		t.Errorf("acme_widget resources=%d dependents=%d, want 2 and 3 (w2 is the same type)", aw.Resources, aw.Dependents)
	}
	if aw.Example == nil || aw.Example.Path != "infra/w.tf" || aw.Example.Line != 3 {
		t.Errorf("example = %+v, want infra/w.tf:3", aw.Example)
	}
	if len(v.Unverified) != 1 || v.Unverified[0].Type != "aws_new_thing" ||
		strings.Join(v.Unverified[0].Entries, ",") != "aws.newthing" {
		t.Errorf("unverified = %+v", v.Unverified)
	}
	if len(v.UnknownNodes) != 5 || v.UnknownNodes[0].ID != "b1" {
		t.Errorf("unknown nodes = %+v (components excluded, sorted by ID)", v.UnknownNodes)
	}
	var kinds []string
	for _, g := range v.Unresolved {
		kinds = append(kinds, string(g.Kind))
	}
	if strings.Join(kinds, ",") != "unresolved_module,unresolved_chart,remote_kustomize,unresolved_reference" {
		t.Errorf("unresolved order = %v", kinds)
	}
	for _, g := range v.Gaps {
		if g.Kind == model.GapParseFailure && g.Subject != "" {
			t.Errorf("parse failure subject kept: %q", g.Subject)
		}
	}
	if len(v.NotRead) != 2 || v.NotRead[0].Path != "infra/broken.tf" {
		t.Errorf("not read = %+v", v.NotRead)
	}
}

func TestCoverageText(t *testing.T) {
	out := renderCoverage(t, coverageDoc(), plainOpts())
	for _, want := range []string{
		"Coverage: 50% of types · 37% of resources · 37% weighted\n",
		"  Confidence: withheld - insufficient coverage for a grade  range 17-79\n",
		"Unknown types (3)\n",
		"  terraform        resources  dependents  example\n",
		"    acme_widget            2           3  infra/w.tf:3\n",
		"    exotic_bucket          1           0  infra/b.tf:1\n",
		"  cloudformation   resources  dependents  example\n",
		"    Acme::Thing            1           0  cfn/t.yaml:4\n",
		"Unresolved inputs (4)\n",
		"  module not available offline          terraform-aws-modules/vpc/aws  infra/net.tf\n",
		"  chart not available offline           oci://charts.example/app       helm/Chart.yaml\n",
		"  remote kustomize base not fetched     github.com/org/base//x?ref=v1  k8s/kustomization.yaml\n",
		"  reference outside the scanned inputs  aws_iam_role.missing           infra/app.tf (2 times)\n",
		"Unverified knowledge (1 type, 1 resource)\n",
		"  matched only by draft catalog entries; not scored until the entries are verified\n",
		"    aws_new_thing          1           0  infra/n.tf:2  aws.newthing\n",
		"Parse failures (3)\n",
		"  infra/broken.tf (terraform): could not be parsed as HCL\n",
		"  infra/half.tf (terraform): partly read; parsed with errors as HCL\n",
		"  infra/bad.tf: could not be parsed\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Argument or block definition") {
		t.Errorf("raw parser text leaked:\n%s", out)
	}
	// Unknown types are grouped: the terraform group (higher dependents)
	// comes before cloudformation.
	if strings.Index(out, "  terraform ") > strings.Index(out, "  cloudformation ") {
		t.Errorf("groups not ordered by dependents:\n%s", out)
	}
	if strings.ContainsAny(out, string([]rune{0x2013, 0x2014})) {
		t.Error("output contains en or em dash")
	}
}

func TestCoverageConfidenceVariants(t *testing.T) {
	d := coverageDoc()
	d.Analysis.Confidence, d.Analysis.Grade = "provisional", "C"
	if out := renderCoverage(t, d, plainOpts()); !strings.Contains(out, "  Confidence: provisional - grade C (provisional)  range 17-79\n") {
		t.Errorf("provisional:\n%s", out)
	}
	d.Analysis.Confidence, d.Analysis.Range = "normal", nil
	if out := renderCoverage(t, d, plainOpts()); !strings.Contains(out, "  Confidence: normal - grade C\n") {
		t.Errorf("normal:\n%s", out)
	}
	d.Analysis.Status, d.Analysis.Confidence = "nothing_scored", "withheld"
	if out := renderCoverage(t, d, plainOpts()); !strings.Contains(out, "  Confidence: withheld - nothing scored\n") {
		t.Errorf("nothing scored:\n%s", out)
	}
}

func TestCoverageEmpty(t *testing.T) {
	d := &result.Document{Analysis: result.Analysis{Status: "ok", Index: ip(10), Grade: "A", Confidence: "normal",
		Coverage: result.Coverage{Types: 1, Resources: 1, Weighted: 1}}}
	out := renderCoverage(t, d, plainOpts())
	for _, want := range []string{
		"Unknown types (0)\n  none: every resource type matched the catalog\n",
		"Unresolved inputs (0)\n  none\n",
		"Unverified knowledge (0 types, 0 resources)\n  none\n",
		"Parse failures (0)\n  none: every input was read\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	v := BuildCoverage(d)
	if v.UnknownTypes == nil || v.UnknownNodes == nil || v.Unverified == nil || v.Unresolved == nil ||
		v.NotRead == nil || v.ParseGaps == nil || v.Gaps == nil {
		t.Error("empty slices must not be nil (stable JSON)")
	}
}

func TestCoverageASCII(t *testing.T) {
	o := plainOpts()
	o.UTF8, o.Emoji = false, false
	out := renderCoverage(t, coverageDoc(), o)
	for _, r := range out {
		if r > 127 {
			t.Fatalf("non-ASCII rune %q in ASCII output:\n%s", r, out)
		}
	}
	if !strings.Contains(out, "50% of types - 37% of resources - 37% weighted") {
		t.Errorf("ASCII separator missing:\n%s", out)
	}
}

var covANSI = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestCoverageColorOnlyReinforces(t *testing.T) {
	d := coverageDoc()
	d.Analysis.Confidence, d.Analysis.Grade = "provisional", "C"
	plain := renderCoverage(t, d, plainOpts())
	o := plainOpts()
	o.Color = true
	colored := renderCoverage(t, d, o)
	if colored == plain {
		t.Fatal("color option had no effect")
	}
	if got := covANSI.ReplaceAllString(colored, ""); got != plain {
		t.Errorf("colored output differs from plain after stripping ANSI:\n%s\n---\n%s", got, plain)
	}
}

func TestCoverageDeterministic(t *testing.T) {
	a := renderCoverage(t, coverageDoc(), plainOpts())
	d := coverageDoc()
	// Reverse nodes, gaps, and sources: output must not change.
	for i, j := 0, len(d.Graph.Nodes)-1; i < j; i, j = i+1, j-1 {
		d.Graph.Nodes[i], d.Graph.Nodes[j] = d.Graph.Nodes[j], d.Graph.Nodes[i]
	}
	for i, j := 0, len(d.Facts.Gaps)-1; i < j; i, j = i+1, j-1 {
		d.Facts.Gaps[i], d.Facts.Gaps[j] = d.Facts.Gaps[j], d.Facts.Gaps[i]
	}
	for i, j := 0, len(d.Facts.Sources)-1; i < j; i, j = i+1, j-1 {
		d.Facts.Sources[i], d.Facts.Sources[j] = d.Facts.Sources[j], d.Facts.Sources[i]
	}
	if b := renderCoverage(t, d, plainOpts()); a != b {
		t.Errorf("output depends on input order:\n%s\n---\n%s", a, b)
	}
}

func TestCoverageMultiRepoPaths(t *testing.T) {
	d := coverageDoc()
	d.Inputs = append(d.Inputs, result.Input{Repo: "s"})
	out := renderCoverage(t, d, plainOpts())
	if !strings.Contains(out, "r/infra/w.tf:3") || !strings.Contains(out, "r/infra/broken.tf (terraform)") {
		t.Errorf("repo prefix missing for merged results:\n%s", out)
	}
}
