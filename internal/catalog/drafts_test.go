package catalog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudowski/exitctl/internal/catalog"
	"github.com/cloudowski/exitctl/internal/testutil"
)

// TestDraftTraitsAndAlternativesUnused: only verified entries affect results
// (FR-016), so a verified service's draft trait or draft alternative is not
// returned by the index unless drafts are included.
func TestDraftTraitsAndAlternativesUnused(t *testing.T) {
	dir := filepath.Join(testutil.RepoRoot(t), "testdata", "catalogs", "plus-one")
	cat, probs, err := catalog.LoadAndValidate(os.DirFS(dir))
	if err != nil || len(probs) > 0 {
		t.Fatalf("plus-one: %v %v", err, probs)
	}
	ix := catalog.NewIndex(cat, catalog.IndexOptions{})
	if ix.Trait("acme-widget-protocol") == nil || len(ix.Alternatives("acme.widget")) != 1 {
		t.Fatal("verified trait and alternative should be usable")
	}
	for i := range cat.Traits {
		cat.Traits[i].Provenance.Status = catalog.StatusDraft
	}
	for i := range cat.Alternatives {
		cat.Alternatives[i].Provenance.Status = catalog.StatusDraft
	}
	ix = catalog.NewIndex(cat, catalog.IndexOptions{})
	if ix.Trait("acme-widget-protocol") != nil {
		t.Error("draft trait is returned by the index")
	}
	if alts := ix.Alternatives("acme.widget"); len(alts) != 0 {
		t.Errorf("draft alternative is returned: %v", alts)
	}
	ix = catalog.NewIndex(cat, catalog.IndexOptions{IncludeDrafts: true})
	if ix.Trait("acme-widget-protocol") == nil || len(ix.Alternatives("acme.widget")) != 1 {
		t.Error("drafts should be usable with IncludeDrafts")
	}
}
