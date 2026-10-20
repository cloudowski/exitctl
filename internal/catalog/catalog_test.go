package catalog_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	catalogdata "github.com/cloudowski/exitctl/catalog"
	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/testutil"
)

// overlay returns the minimal test catalog with the files of extra (a
// broken-* fixture directory) added on top.
func overlay(t *testing.T, extra string) fs.FS {
	t.Helper()
	root := testutil.RepoRoot(t)
	m := fstest.MapFS{}
	add := func(dir string) {
		base := filepath.Join(root, "testdata", "catalogs", dir)
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(base, p)
			m[filepath.ToSlash(rel)] = &fstest.MapFile{Data: b}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	add("minimal")
	if extra != "" {
		add(extra)
	}
	return m
}

func load(t *testing.T, fsys fs.FS) (*catalog.Catalog, []catalog.Problem) {
	t.Helper()
	cat, probs, err := catalog.LoadAndValidate(fsys)
	if err != nil {
		return nil, []catalog.Problem{{Message: err.Error()}}
	}
	return cat, probs
}

func TestMinimalCatalogValid(t *testing.T) {
	cat, probs := load(t, overlay(t, ""))
	if len(probs) > 0 {
		t.Fatalf("minimal catalog has problems: %v", probs)
	}
	if cat.Manifest.CatalogVersion == "" {
		t.Fatal("manifest not loaded")
	}
}

func TestBrokenCatalogs(t *testing.T) {
	cases := map[string]string{
		"broken-alias-cycle":    "cycle",
		"broken-duplicate-id":   "duplicate",
		"broken-duplicate-type": "aws_",
		"broken-level-reason":   "reason",
		"broken-no-alternative": "alternative",
		"broken-recommended":    "recommended",
		"broken-reference":      "no-such-trait",
		"broken-regex":          "regex",
		"broken-schema":         "",
		"broken-schema-major":   "schema",
	}
	for dir, want := range cases {
		t.Run(dir, func(t *testing.T) {
			_, probs := load(t, overlay(t, dir))
			if len(probs) == 0 {
				t.Fatal("expected problems")
			}
			if want == "" {
				return
			}
			for _, p := range probs {
				if strings.Contains(strings.ToLower(p.Message+" "+p.EntryID), want) {
					return
				}
			}
			t.Fatalf("no problem mentions %q: %v", want, probs)
		})
	}
}

func TestIndexClassifyAndAlternatives(t *testing.T) {
	cat, probs := load(t, overlay(t, ""))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	ix := catalog.NewIndex(cat, catalog.IndexOptions{})
	c := ix.Classify(catalog.TypeRef{SourceFormat: "terraform", Type: "aws_dynamodb_table"})
	if c.Class != "service" || c.Service == nil || c.Service.ID != "aws.dynamodb" {
		t.Fatalf("dynamodb: %+v", c)
	}
	if c := ix.Classify(catalog.TypeRef{SourceFormat: "kubernetes", Type: "apps/Deployment"}); c.Class != "neutral" {
		t.Fatalf("deployment: %+v", c)
	}
	if c := ix.Classify(catalog.TypeRef{SourceFormat: "terraform", Type: "acme_widget"}); c.Class != "unknown" {
		t.Fatalf("unknown: %+v", c)
	}
	alts := ix.Alternatives("aws.dynamodb")
	if len(alts) < 2 || !alts[0].Recommended {
		t.Fatalf("recommended alternative must come first: %+v", alts)
	}
	for i := 1; i < len(alts); i++ {
		if alts[i].Recommended && !alts[i-1].Recommended {
			t.Fatal("ordering broken")
		}
	}
}

func TestDraftsOnlyWithOption(t *testing.T) {
	cat, probs := load(t, overlay(t, ""))
	if len(probs) > 0 {
		t.Fatal(probs)
	}
	ref := catalog.TypeRef{SourceFormat: "terraform", Type: "aws_cognito_user_pool"}
	if c := catalog.NewIndex(cat, catalog.IndexOptions{}).Classify(ref); c.Class != "unverified" {
		t.Fatalf("draft must be unverified by default: %+v", c.Class)
	}
	if c := catalog.NewIndex(cat, catalog.IndexOptions{IncludeDrafts: true}).Classify(ref); c.Class != "service" {
		t.Fatalf("draft must classify with IncludeDrafts: %+v", c.Class)
	}
}

func TestBuiltinCatalogLoads(t *testing.T) {
	_, probs, err := catalog.LoadAndValidate(catalogdata.FS)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range probs {
		t.Errorf("%s", p.String())
	}
}
