package engine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/config"
	"github.com/cloudowski/exitctl/internal/engine"
	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/result"
	"github.com/cloudowski/exitctl/internal/testutil"
)

func minimalIndex(t *testing.T) *catalog.Index {
	t.Helper()
	dir := filepath.Join(testutil.RepoRoot(t), "testdata", "catalogs", "minimal")
	cat, probs, err := catalog.LoadAndValidate(os.DirFS(dir))
	if err != nil || len(probs) > 0 {
		t.Fatalf("minimal catalog: %v %v", err, probs)
	}
	return catalog.NewIndex(cat, catalog.IndexOptions{})
}

func ev(path string, line int, s string) []model.Evidence {
	return []model.Evidence{model.NewEvidence("shop", path, line, s, model.KindTerraform)}
}

// handFacts is a tiny shop: a VPC, a DynamoDB table with a stream, a Lambda
// reading the stream, a component using the DynamoDB SDK, and an unknown type.
func handFacts() model.Facts {
	id := func(addr string) string { return model.CanonicalID("shop", "infra", addr) }
	node := func(typ, addr string, line int, attrs map[string]any) model.RawNode {
		if attrs == nil {
			attrs = map[string]any{}
		}
		return model.RawNode{ID: id(addr), NodeKind: model.NodeResource, SourceKind: model.KindTerraform,
			Type: typ, NativeAddress: addr, Attributes: attrs, Multiplicity: model.MultOne,
			Evidence: ev("infra/main.tf", line, `resource "`+typ+`"`)}
	}
	comp := model.CanonicalID("shop", "services/orders", "component")
	return model.Facts{
		Sources: []model.Source{{Repo: "shop", Path: "infra/main.tf", Kind: model.KindTerraform, Status: model.StatusParsed}},
		RawNodes: []model.RawNode{
			node("aws_vpc", "aws_vpc.main", 1, nil),
			node("aws_dynamodb_table", "aws_dynamodb_table.orders", 5, map[string]any{"stream_enabled": true, "name": "orders"}),
			node("aws_lambda_function", "aws_lambda_function.proc", 12, nil),
			node("aws_lambda_event_source_mapping", "aws_lambda_event_source_mapping.m", 20, nil),
			node("acme_widget", "acme_widget.w", 30, nil),
			{ID: comp, NodeKind: model.NodeComponent, SourceKind: model.KindPackageManifest, Type: model.TypeComponent,
				NativeAddress: "services/orders", Attributes: map[string]any{}, Multiplicity: model.MultOne,
				Evidence: ev("services/orders/package.json", 1, "{")},
		},
		RawEdges: []model.RawEdge{
			{From: id("aws_dynamodb_table.orders"), To: id("aws_vpc.main"), Attribute: "vpc_id", Evidence: ev("infra/main.tf", 6, "vpc_id")},
			{From: id("aws_lambda_event_source_mapping.m"), To: id("aws_dynamodb_table.orders"), Attribute: "event_source_arn", Evidence: ev("infra/main.tf", 21, "event_source_arn")},
			{From: id("aws_lambda_event_source_mapping.m"), To: id("aws_lambda_function.proc"), Attribute: "function_name", Evidence: ev("infra/main.tf", 22, "function_name")},
			{From: id("acme_widget.w"), To: id("aws_dynamodb_table.orders"), Attribute: "table", Evidence: ev("infra/main.tf", 31, "table")},
		},
		CodeUses: []model.CodeUse{{Component: comp, Ecosystem: model.EcoNPM, PatternKind: "import", Service: "aws.dynamodb",
			Evidence: model.NewEvidence("shop", "services/orders/repo.ts", 3, `import { DynamoDBClient } from "@aws-sdk/client-dynamodb"`, model.KindCode)}},
	}
}

func TestAnalyzeProducesValidDeterministicDocument(t *testing.T) {
	ix := minimalIndex(t)
	run := func() []byte {
		a := engine.Analyze(handFacts(), ix, config.Defaults(), []engine.Input{{Repo: "shop", AsOf: "2026-10-06"}})
		b, err := result.Marshal(a.Doc)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	out := testutil.RunTwice(t, run)
	if err := result.Validate(out); err != nil {
		t.Fatalf("schema: %v\n%s", err, out)
	}
	doc, err := result.Decode("mem", out)
	if err != nil {
		t.Fatal(err)
	}
	an := doc.Analysis
	if an.Status != "ok" || an.Index == nil || an.Grade == "" && an.Confidence != "withheld" {
		t.Fatalf("analysis incomplete: %+v", an)
	}
	if len(an.Anchors) == 0 || doc.NodeByID(an.Anchors[0].Node).Service != "aws.dynamodb" {
		t.Fatalf("top anchor should be the DynamoDB table: %+v", an.Anchors)
	}
	if an.Range == nil {
		t.Fatal("unknown acme_widget must produce an index range")
	}
	if an.Lever == nil || an.Lever.Delta < 1 {
		t.Fatalf("expected a lever: %+v", an.Lever)
	}
	n := doc.NodeByID(an.Anchors[0].Node)
	found := false
	for _, f := range n.Findings {
		if f.Trait == "managed-change-stream" {
			found = true
		}
	}
	if !found {
		t.Fatalf("stream rule did not fire: %+v", n.Findings)
	}
}
