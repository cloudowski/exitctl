package result

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/version"
)

func ev(repo, path string, line int, excerpt string, k model.SourceKind) model.Evidence {
	return model.NewEvidence(repo, path, line, excerpt, k)
}

func vers(catalog string) Versions {
	return Versions{Tool: "0.1.0", Catalog: catalog, CatalogSchema: "1.0", Rules: "1.0.0", ConfigFingerprint: "abcd1234"}
}

func infraDoc() *Document {
	e := ev("infra", "db.tf", 1, `resource "aws_dynamodb_table" "orders" {`, model.KindTerraform)
	return &Document{
		Format: version.ResultFormat, Kind: KindResult, Versions: vers("2026.10.1"),
		Inputs: []Input{{Repo: "infra", AsOf: "2026-10-06", Versions: vers("2026.10.1")}},
		Facts: model.Facts{
			Sources: []model.Source{
				{Repo: "infra", Path: "queue.tf", Kind: model.KindTerraform, Status: model.StatusParsed},
				{Repo: "infra", Path: "db.tf", Kind: model.KindTerraform, Status: model.StatusParsed},
			},
			RawNodes: []model.RawNode{{
				ID: "infra//#aws_dynamodb_table.orders", NodeKind: model.NodeResource, SourceKind: model.KindTerraform,
				Type: "aws_dynamodb_table", NativeAddress: "aws_dynamodb_table.orders",
				ProviderIdentity: "aws:dynamodb:table/orders", Attributes: map[string]any{"name": "orders"},
				Multiplicity: model.MultOne, Evidence: []model.Evidence{e},
			}},
			RawEdges: []model.RawEdge{{From: "infra//#a", To: "infra//#b", Evidence: []model.Evidence{e}}},
			Gaps:     []model.Gap{{Kind: model.GapUnresolvedModule, Subject: "m", Repo: "infra", Path: "main.tf", Count: 1}},
		},
	}
}

func ordersDoc() *Document {
	e := ev("orders-app", "src/repo.ts", 1, `import { DynamoDBClient } from "@aws-sdk/client-dynamodb";`, model.KindCode)
	m := ev("orders-app", "package.json", 7, `"@aws-sdk/client-dynamodb": "^3.600.0",`, model.KindPackageManifest)
	return &Document{
		Format: version.ResultFormat, Kind: KindResult, Versions: vers("2026.10.1"),
		Inputs: []Input{{Repo: "orders-app", AsOf: "2026-10-06", Versions: vers("2026.10.1")}},
		Facts: model.Facts{
			Sources: []model.Source{{Repo: "orders-app", Path: "package.json", Kind: model.KindPackageManifest, Status: model.StatusParsed}},
			PackageUses: []model.PackageUse{{Component: "orders-app//#component", Ecosystem: model.EcoNPM,
				Name: "@aws-sdk/client-dynamodb", Version: "^3.600.0", Manifest: m}},
			CodeUses: []model.CodeUse{{Component: "orders-app//#component", Ecosystem: model.EcoNPM,
				PatternKind: "import", Service: "aws.dynamo-old", Match: `"@aws-sdk/client-dynamodb"`, Evidence: e}},
		},
	}
}

func billingDoc() *Document {
	e := ev("billing-app", "billing/queue.py", 5, `sqs = boto3.client("sqs")`, model.KindCode)
	return &Document{
		Format: version.ResultFormat, Kind: KindGraph, Versions: vers("2026.11.1"),
		Inputs: []Input{{Repo: "billing-app", AsOf: "2026-10-07", Versions: vers("2026.11.1")}},
		Facts: model.Facts{
			Sources: []model.Source{{Repo: "billing-app", Path: "requirements.txt", Kind: model.KindPackageManifest, Status: model.StatusParsed}},
			CodeUses: []model.CodeUse{{Component: "billing-app//#component", Ecosystem: model.EcoPyPI,
				PatternKind: "usage", Service: "aws.sqs", Match: `.client("sqs"`, Evidence: e}},
		},
	}
}

func aliasResolver(id string) string {
	if id == "aws.dynamo-old" {
		return "aws.dynamodb"
	}
	return id
}

func permutations(n int) [][]int {
	if n == 1 {
		return [][]int{{0}}
	}
	var out [][]int
	for _, p := range permutations(n - 1) {
		for i := 0; i <= len(p); i++ {
			q := append(append(append([]int{}, p[:i]...), n-1), p[i:]...)
			out = append(out, q)
		}
	}
	return out
}

func TestMergePermutationIndependent(t *testing.T) {
	docs := []NamedDoc{{"infra.json", infraDoc()}, {"orders.json", ordersDoc()}, {"billing.json", billingDoc()}}
	var wantF, wantI string
	perms := permutations(len(docs))
	if len(perms) != 6 {
		t.Fatalf("permutations = %d", len(perms))
	}
	for _, p := range perms {
		in := make([]NamedDoc, 0, len(p))
		for _, i := range p {
			in = append(in, docs[i])
		}
		f, inputs, err := MergeFacts(in, aliasResolver)
		if err != nil {
			t.Fatal(err)
		}
		gf, gi := canonical(f), canonical(inputs)
		if wantF == "" {
			wantF, wantI = gf, gi
			continue
		}
		if gf != wantF || gi != wantI {
			t.Fatalf("permutation %v differs:\n%s\nvs\n%s", p, gf, wantF)
		}
	}
}

func TestMergeVersionsPreservedAndSorted(t *testing.T) {
	_, inputs, err := MergeFacts([]NamedDoc{{"b", billingDoc()}, {"o", ordersDoc()}, {"i", infraDoc()}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var repos, cats []string
	for _, in := range inputs {
		repos = append(repos, in.Repo)
		cats = append(cats, in.Versions.Catalog)
	}
	if !reflect.DeepEqual(repos, []string{"billing-app", "infra", "orders-app"}) {
		t.Fatalf("repos = %v", repos)
	}
	if !reflect.DeepEqual(cats, []string{"2026.11.1", "2026.10.1", "2026.10.1"}) {
		t.Fatalf("catalog versions = %v", cats)
	}
}

func TestMergeFlattensMergedInputs(t *testing.T) {
	f1, in1, err := MergeFacts([]NamedDoc{{"i", infraDoc()}, {"o", ordersDoc()}}, aliasResolver)
	if err != nil {
		t.Fatal(err)
	}
	merged := &Document{Format: version.ResultFormat, Kind: KindResult, Inputs: in1, Facts: f1}
	f2, in2, err := MergeFacts([]NamedDoc{{"m", merged}, {"b", billingDoc()}, {"o", ordersDoc()}}, aliasResolver)
	if err != nil {
		t.Fatal(err)
	}
	f3, in3, err := MergeFacts([]NamedDoc{{"i", infraDoc()}, {"o", ordersDoc()}, {"b", billingDoc()}}, aliasResolver)
	if err != nil {
		t.Fatal(err)
	}
	if canonical(f2) != canonical(f3) || canonical(in2) != canonical(in3) {
		t.Fatalf("re-merging a merged document differs from a flat merge")
	}
	if len(in2) != 3 {
		t.Fatalf("inputs = %d, want 3", len(in2))
	}
}

func TestMergeDeduplicates(t *testing.T) {
	a := infraDoc()
	b := infraDoc()
	// Same node ID with extra evidence: evidence is unioned into one node.
	extra := ev("infra", "db.tf", 2, `name = "orders"`, model.KindTerraform)
	b.Facts.RawNodes[0].Evidence = append(b.Facts.RawNodes[0].Evidence, extra)
	f, inputs, err := MergeFacts([]NamedDoc{{"a", a}, {"b", b}, {"o", ordersDoc()}, {"o2", ordersDoc()}}, aliasResolver)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Sources) != 3 {
		t.Errorf("sources = %d, want 3", len(f.Sources))
	}
	if len(f.RawNodes) != 1 || len(f.RawNodes[0].Evidence) != 2 {
		t.Errorf("raw nodes = %+v", f.RawNodes)
	}
	if len(f.RawEdges) != 1 || len(f.PackageUses) != 1 || len(f.CodeUses) != 1 || len(f.Gaps) != 1 {
		t.Errorf("edges %d pkgs %d code %d gaps %d", len(f.RawEdges), len(f.PackageUses), len(f.CodeUses), len(f.Gaps))
	}
	if len(inputs) != 2 {
		t.Errorf("inputs = %d, want 2", len(inputs))
	}
	// Inputs are not mutated.
	if len(a.Facts.RawNodes[0].Evidence) != 1 || a.Facts.Sources[0].Path != "queue.tf" {
		t.Errorf("input document was mutated")
	}
}

func TestMergeReResolvesServiceAliases(t *testing.T) {
	f, _, err := MergeFacts([]NamedDoc{{"o", ordersDoc()}}, aliasResolver)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.CodeUses[0].Service; got != "aws.dynamodb" {
		t.Fatalf("service = %q, want aws.dynamodb", got)
	}
	f, _, _ = MergeFacts([]NamedDoc{{"o", ordersDoc()}}, CatalogResolver(nil))
	if got := f.CodeUses[0].Service; got != "aws.dynamo-old" {
		t.Fatalf("nil index resolver changed service to %q", got)
	}
}

func TestMergeKinds(t *testing.T) {
	if _, _, err := MergeFacts([]NamedDoc{{"g.json", billingDoc()}}, nil); err != nil {
		t.Fatalf("graph kind rejected: %v", err)
	}
	d := ordersDoc()
	d.Kind = "report"
	_, _, err := MergeFacts([]NamedDoc{{"x.json", d}}, nil)
	if err == nil || !strings.Contains(err.Error(), "x.json") {
		t.Fatalf("err = %v, want rejection naming x.json", err)
	}
	if _, _, err := MergeFacts(nil, nil); err == nil {
		t.Fatal("empty merge accepted")
	}
}

func TestLoadAllRejectsIncompatibleFormat(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	gd := ordersDoc()
	gd.Analysis.Status = "ok"
	if err := Save(gd, good); err != nil {
		t.Fatal(err)
	}
	docs, err := LoadAll([]string{good})
	if err != nil {
		t.Fatalf("load good: %v", err)
	}
	if len(docs) != 1 || docs[0].Name != good || docs[0].Doc.Inputs[0].Repo != "orders-app" {
		t.Fatalf("docs = %+v", docs)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"format":"exitctl.result/v2","kind":"result"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadAll([]string{good, bad})
	var ie *IncompatibleError
	if !errors.As(err, &ie) || ie.Path != bad {
		t.Fatalf("err = %v, want IncompatibleError for %s", err, bad)
	}
}
