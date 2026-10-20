package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudowski/exitctl/internal/testutil"
)

// TestGraphRoundTrip: a graph JSON export merged on its own yields a
// byte-identical graph section (FR-060).
func TestGraphRoundTrip(t *testing.T) {
	root := testutil.RepoRoot(t)
	for _, fx := range []string{"shop-aws", "multi-cloud", "cycles"} {
		t.Run(fx, func(t *testing.T) {
			dir := t.TempDir()
			res := filepath.Join(dir, "result.json")
			var so, se bytes.Buffer
			if code := Run([]string{"scan", filepath.Join(root, "testdata", "fixtures", fx), "--include-drafts",
				"--as-of", "2026-10-06", "--repo-name", fx, "--no-store", "--output", res}, &so, &se); code != 0 {
				t.Fatalf("scan exited %d: %s", code, se.String())
			}
			so.Reset()
			if code := Run([]string{"graph", "--format", "json", "--input", res}, &so, &se); code != 0 {
				t.Fatalf("graph exited %d: %s", code, se.String())
			}
			exported := filepath.Join(dir, "graph.json")
			if err := os.WriteFile(exported, so.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			merged := filepath.Join(dir, "merged.json")
			if code := Run([]string{"merge", "--include-drafts", exported, "-o", merged}, &so, &se); code != 0 {
				t.Fatalf("merge exited %d: %s", code, se.String())
			}
			a, b := graphSection(t, exported), graphSection(t, merged)
			if !bytes.Equal(a, b) {
				t.Fatalf("graph section differs after round trip:\n--- export ---\n%s\n--- merged ---\n%s", a, b)
			}
		})
	}
}

func graphSection(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Graph json.RawMessage `json:"graph"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d.Graph
}
