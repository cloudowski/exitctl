package catalog_test

import (
	"testing"

	catalogdata "github.com/cloudowski/exitctl/catalog"
	"github.com/cloudowski/exitctl/internal/catalog"
)

// TestBuiltinCatalog checks the embedded catalog is valid and meets the
// FR-020 breadth targets. Entries are counted whatever their status: the
// v0.1 catalog is LLM-drafted and is promoted to verified by maintainer review.
func TestBuiltinCatalog(t *testing.T) {
	cat, probs, err := catalog.LoadAndValidate(catalogdata.FS)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range probs {
		t.Errorf("%s", p.String())
	}
	want := map[string]int{"aws": 50, "gcp": 30, "azure": 30, "oci": 15}
	got := map[string]int{}
	for _, s := range cat.Services {
		got[s.Provider]++
		if len(s.Alternatives) == 0 {
			t.Errorf("%s has no alternative mapping", s.ID)
		}
		if len(s.Traits) == 0 {
			t.Errorf("%s has no traits", s.ID)
		}
	}
	for p, n := range want {
		if got[p] < n {
			t.Errorf("provider %s: %d services, want >= %d (FR-020)", p, got[p], n)
		}
	}
	// SDK patterns: every provider covers all five ecosystems somewhere.
	for _, p := range []string{"aws", "gcp", "azure", "oci"} {
		eco := map[string]bool{}
		for _, s := range cat.Services {
			if s.Provider != p {
				continue
			}
			for _, sp := range s.SDK {
				eco[sp.Ecosystem] = true
			}
		}
		for _, e := range []string{"npm", "pypi", "go", "maven", "nuget"} {
			if !eco[e] {
				t.Errorf("provider %s has no %s SDK patterns", p, e)
			}
		}
	}
	// Every draft LLM entry carries its generation metadata.
	for _, s := range cat.Services {
		if s.Provenance.Origin == "llm" && s.Provenance.LLM == nil {
			t.Errorf("%s: llm origin without llm metadata", s.ID)
		}
	}
	ix := catalog.NewIndex(cat, catalog.IndexOptions{IncludeDrafts: true})
	if c := ix.Classify(catalog.TypeRef{SourceFormat: "kubernetes", Type: "apps/Deployment"}); c.Class != "neutral" {
		t.Errorf("apps/Deployment should be neutral, got %s", c.Class)
	}
}
