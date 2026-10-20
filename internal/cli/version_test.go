package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/cloudowski/exitctl/internal/result"
)

// TestVersionMatchesResult: `version --format json` reports exactly the
// versions recorded in a scan result (US9).
func TestVersionMatchesResult(t *testing.T) {
	var so, se bytes.Buffer
	if code := Run([]string{"version", "--format", "json"}, &so, &se); code != 0 {
		t.Fatalf("version exited %d: %s", code, se.String())
	}
	var v versionJSON
	if err := json.Unmarshal(so.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	_, js := scanFixture(t, "shop-aws")
	doc, err := result.Decode("scan", js)
	if err != nil {
		t.Fatal(err)
	}
	got := versionJSON{Tool: doc.Versions.Tool, Catalog: doc.Versions.Catalog,
		CatalogSchema: doc.Versions.CatalogSchema, Rules: doc.Versions.Rules, ResultFormat: doc.Format}
	if got != v {
		t.Fatalf("version %+v differs from result versions %+v", v, got)
	}
}
