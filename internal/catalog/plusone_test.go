package catalog_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	catalogdata "github.com/cloudowski/exitctl/catalog"
	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/testutil"
)

// overlayOn copies every file of base and then the files of the given
// testdata/catalogs directories on top (same path = replaced). It builds a
// "built-in catalog plus extra entries" tree without copying the built-in
// files into testdata.
func overlayOn(t *testing.T, base fs.FS, dirs ...string) fs.FS {
	t.Helper()
	m := fstest.MapFS{}
	err := fs.WalkDir(base, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(base, p)
		if err != nil {
			return err
		}
		m[p] = &fstest.MapFile{Data: b}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	root := testutil.RepoRoot(t)
	for _, dir := range dirs {
		src := filepath.Join(root, "testdata", "catalogs", dir)
		err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			m[filepath.ToSlash(rel)] = &fstest.MapFile{Data: b}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return m
}

// TestPlusOne checks the plus-one catalog (US8, SC-007) both on its own and
// overlaid on the built-in catalog: a new service added only as data is
// recognized, and its draft LLM sibling stays unverified.
func TestPlusOne(t *testing.T) {
	cases := map[string]fs.FS{
		"standalone":       overlayOn(t, fstest.MapFS{}, "plus-one"),
		"built-in+overlay": overlayOn(t, catalogdata.FS, "plus-one"),
	}
	for _, name := range []string{"standalone", "built-in+overlay"} {
		t.Run(name, func(t *testing.T) {
			cat, probs := load(t, cases[name])
			for _, p := range probs {
				t.Errorf("%s", p.String())
			}
			if t.Failed() {
				return
			}
			ix := catalog.NewIndex(cat, catalog.IndexOptions{})
			w := ix.Classify(catalog.TypeRef{SourceFormat: "terraform", Type: "acme_widget"})
			if w.Class != "service" || w.Service == nil || w.Service.ID != "acme.widget" {
				t.Fatalf("acme_widget: %+v", w)
			}
			if alts := ix.Alternatives("acme.widget"); len(alts) != 1 || !alts[0].Recommended {
				t.Fatalf("acme.widget alternatives: %+v", alts)
			}
			g := ix.Classify(catalog.TypeRef{SourceFormat: "terraform", Type: "acme_gadget"})
			if g.Class != "unverified" || g.Service == nil || g.Service.ID != "acme.gadget" {
				t.Fatalf("acme_gadget must be unverified: %+v", g)
			}
			if lm := g.Service.Provenance.LLM; g.Service.Provenance.Origin != "llm" || lm == nil || lm.EvalPassed {
				t.Fatalf("acme.gadget provenance: %+v", g.Service.Provenance)
			}
			d := catalog.NewIndex(cat, catalog.IndexOptions{IncludeDrafts: true})
			if c := d.Classify(catalog.TypeRef{SourceFormat: "terraform", Type: "acme_gadget"}); c.Class != "service" {
				t.Fatalf("acme_gadget with drafts: %+v", c.Class)
			}
		})
	}
	// The overlay keeps every built-in service.
	builtin, _, err := catalog.LoadAndValidate(catalogdata.FS)
	if err != nil {
		t.Fatal(err)
	}
	over, _ := load(t, cases["built-in+overlay"])
	if over != nil && len(over.Services) != len(builtin.Services)+2 {
		t.Fatalf("overlay has %d services, want %d", len(over.Services), len(builtin.Services)+2)
	}
}

// TestBrokenCatalogFive checks testdata/catalogs/broken reports exactly the
// five problems it was built with (US8 acceptance scenario 3).
func TestBrokenCatalogFive(t *testing.T) {
	_, probs := load(t, overlayOn(t, fstest.MapFS{}, "broken"))
	want := []string{
		"services/aws/bad-usage.yaml",
		"services/aws/no-reason.yaml",
		"services/aws/self-recommended.yaml",
		"services/aws/sqs-copy.yaml",
		"services/aws/unknown-trait.yaml",
	}
	if len(probs) != len(want) {
		t.Fatalf("got %d problems, want %d: %v", len(probs), len(want), probs)
	}
	for i, p := range probs {
		if p.File != want[i] || p.Line == 0 || p.EntryID == "" {
			t.Errorf("problem %d: %s (want file %s with line and entry id)", i, p.String(), want[i])
		}
	}
}
