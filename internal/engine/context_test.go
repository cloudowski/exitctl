package engine_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/config"
	"github.com/cloudowski/exitctl/internal/engine"
	"github.com/cloudowski/exitctl/internal/model"
	"github.com/cloudowski/exitctl/internal/result"
	"github.com/cloudowski/exitctl/internal/testutil"
)

// indexWithLookups is the minimal catalog plus a verified neutral entry for
// two lookup types, scored or not.
func indexWithLookups(t *testing.T, scored bool) *catalog.Index {
	t.Helper()
	src := filepath.Join(testutil.RepoRoot(t), "testdata", "catalogs", "minimal")
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	flag := "false"
	if scored {
		flag = "true"
	}
	entry := `schema: '1.0'
kind: neutral
id: aws-lookups
provider: aws
resource_types:
  terraform: [aws_caller_identity, aws_iam_policy_document]
scored: ` + flag + `
provenance: {origin: maintainer, author: test, reviewed_by: test, reviewed_on: '2026-10-07', status: verified}
`
	if err := os.WriteFile(filepath.Join(dst, "neutral", "aws-lookups.yaml"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, probs, err := catalog.LoadAndValidate(os.DirFS(dst))
	if err != nil || len(probs) > 0 {
		t.Fatalf("catalog: %v %v", err, probs)
	}
	return catalog.NewIndex(cat, catalog.IndexOptions{})
}

// withLookups adds two lookups to the hand-built shop; the policy document
// reads the DynamoDB table.
func withLookups() model.Facts {
	f := handFacts()
	id := func(addr string) string { return model.CanonicalID("shop", "infra", addr) }
	for i, typ := range []string{"aws_caller_identity", "aws_iam_policy_document"} {
		addr := "data." + typ + ".this"
		f.RawNodes = append(f.RawNodes, model.RawNode{ID: id(addr), NodeKind: model.NodeResource,
			SourceKind: model.KindTerraform, Type: typ, NativeAddress: addr, Attributes: map[string]any{},
			Multiplicity: model.MultOne, Evidence: ev("infra/data.tf", 1+i*5, `data "`+typ+`" "this"`)})
	}
	f.RawEdges = append(f.RawEdges, model.RawEdge{From: id("data.aws_iam_policy_document.this"),
		To: id("aws_dynamodb_table.orders"), Attribute: "statement.resources", Evidence: ev("infra/data.tf", 8, "resources")})
	return f
}

func analyze(t *testing.T, f model.Facts, ix *catalog.Index) *result.Document {
	t.Helper()
	return engine.Analyze(f, ix, config.Defaults(), []engine.Input{{Repo: "shop", AsOf: "2026-10-07"}}).Doc
}

// TestContextNodesNotScored: resources of a scored:false entry change
// neither the index, the range, coverage, nor other nodes' dependents.
func TestContextNodesNotScored(t *testing.T) {
	ix := indexWithLookups(t, false)
	base := analyze(t, handFacts(), ix)
	got := analyze(t, withLookups(), ix)

	if *got.Analysis.Index != *base.Analysis.Index || got.Analysis.Coverage.Weighted != base.Analysis.Coverage.Weighted ||
		got.Analysis.Coverage.Resources != base.Analysis.Coverage.Resources {
		t.Fatalf("lookups changed the score: index %d -> %d, coverage %v -> %v",
			*base.Analysis.Index, *got.Analysis.Index, base.Analysis.Coverage, got.Analysis.Coverage)
	}
	if (base.Analysis.Range == nil) != (got.Analysis.Range == nil) ||
		(base.Analysis.Range != nil && *base.Analysis.Range != *got.Analysis.Range) {
		t.Fatalf("range changed: %v -> %v", base.Analysis.Range, got.Analysis.Range)
	}
	table := model.CanonicalID("shop", "infra", "aws_dynamodb_table.orders")
	if b, g := base.NodeByID(table), got.NodeByID(table); b.DependentsCount != g.DependentsCount || b.Weight != g.Weight {
		t.Fatalf("table dependents %d -> %d, weight %v -> %v", b.DependentsCount, g.DependentsCount, b.Weight, g.Weight)
	}
	for _, addr := range []string{"data.aws_caller_identity.this", "data.aws_iam_policy_document.this"} {
		n := got.NodeByID(model.CanonicalID("shop", "infra", addr))
		if n == nil || n.Class != "context" {
			t.Fatalf("%s class = %v, want context", addr, n)
		}
		if result.DisplayAddress(n) != addr+" (not scored)" {
			t.Errorf("display address = %q", result.DisplayAddress(n))
		}
		if slices.Contains(got.Analysis.Coverage.UnverifiedMatches, n.ID) {
			t.Errorf("%s listed as unverified", addr)
		}
	}
	b, err := result.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Validate(b); err != nil {
		t.Fatalf("result with context nodes does not match the schema: %v", err)
	}
}

// TestScoredNeutralDilutes: the same lookups as ordinary (scored) neutral
// entries lower the index, which is what scored: false avoids.
func TestScoredNeutralDilutes(t *testing.T) {
	ix := indexWithLookups(t, true)
	base := analyze(t, handFacts(), ix)
	got := analyze(t, withLookups(), ix)
	if *got.Analysis.Index >= *base.Analysis.Index {
		t.Fatalf("scored neutral lookups should dilute the index: %d -> %d", *base.Analysis.Index, *got.Analysis.Index)
	}
}
