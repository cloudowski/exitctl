package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/result"
	"github.com/cloudowski/exitctl/internal/testutil"
)

var splitRepos = []string{"infra", "orders-app", "billing-app"}

// scanSplit scans the split fixtures into dir and returns the result paths.
func scanSplit(t *testing.T, dir string) map[string]string {
	t.Helper()
	root := testutil.RepoRoot(t)
	out := map[string]string{}
	for _, r := range splitRepos {
		p := filepath.Join(dir, r+".json")
		var so, se bytes.Buffer
		code := Run([]string{"scan", filepath.Join(root, "testdata", "fixtures", "split", r),
			"--include-drafts", "--as-of", "2026-10-06", "--repo-name", r, "--no-store", "--output", p}, &so, &se)
		if code != 0 {
			t.Fatalf("scan %s exited %d: %s", r, code, se.String())
		}
		out[r] = p
	}
	return out
}

func runMerge(t *testing.T, out string, inputs ...string) (int, string) {
	t.Helper()
	var so, se bytes.Buffer
	args := append([]string{"merge", "--include-drafts", "--no-color", "--emoji", "never", "-o", out}, inputs...)
	code := Run(args, &so, &se)
	return code, so.String() + se.String()
}

func permutations(s []string) [][]string {
	if len(s) <= 1 {
		return [][]string{append([]string{}, s...)}
	}
	var out [][]string
	for i := range s {
		rest := append(append([]string{}, s[:i]...), s[i+1:]...)
		for _, p := range permutations(rest) {
			out = append(out, append([]string{s[i]}, p...))
		}
	}
	return out
}

// TestMergePermutations: every input order gives a byte-identical result (SC-003, FR-076).
func TestMergePermutations(t *testing.T) {
	dir := t.TempDir()
	paths := scanSplit(t, dir)
	var first []byte
	for i, perm := range permutations(splitRepos) {
		var in []string
		for _, r := range perm {
			in = append(in, paths[r])
		}
		out := filepath.Join(dir, "merged.json")
		if code, msg := runMerge(t, out, in...); code != 0 {
			t.Fatalf("merge %v exited %d: %s", perm, code, msg)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = b
			continue
		}
		if !bytes.Equal(first, b) {
			t.Fatalf("merge output depends on input order %v", perm)
		}
	}

	doc, err := result.Decode("merged", first)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Inputs) != 3 {
		t.Fatalf("inputs = %d, want 3", len(doc.Inputs))
	}
	table := doc.NodeByID("infra//#aws_dynamodb_table.orders")
	if table == nil {
		t.Fatal("merged result has no infra DynamoDB table")
	}
	found := false
	for _, d := range table.Dependents {
		if strings.HasPrefix(d, "orders-app//") {
			found = true
		}
	}
	if !found {
		t.Errorf("table dependents %v do not include the orders-app component", table.Dependents)
	}
	resourceLevel := false
	for _, e := range doc.Graph.Edges {
		if e.To == table.ID && e.Kind == "usage" && e.Confidence == "resource-level" && strings.HasPrefix(e.From, "orders-app//") {
			resourceLevel = true
		}
	}
	if !resourceLevel {
		t.Error("no resource-level usage edge from orders-app to the table")
	}
}

// TestMergeKeepsInputVersions: an input produced with another catalog version
// keeps its versions in inputs.
func TestMergeKeepsInputVersions(t *testing.T) {
	dir := t.TempDir()
	paths := scanSplit(t, dir)
	old, err := result.Load(paths["infra"])
	if err != nil {
		t.Fatal(err)
	}
	old.Versions.Catalog = "2026.09.1"
	for i := range old.Inputs {
		old.Inputs[i].Versions.Catalog = "2026.09.1"
	}
	if err := result.Save(old, paths["infra"]); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "merged.json")
	if code, msg := runMerge(t, out, paths["infra"], paths["orders-app"]); code != 0 {
		t.Fatalf("merge exited %d: %s", code, msg)
	}
	doc, err := result.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, in := range doc.Inputs {
		got[in.Repo] = in.Versions.Catalog
	}
	if got["infra"] != "2026.09.1" || got["orders-app"] != doc.Versions.Catalog {
		t.Errorf("input catalog versions = %v, merged catalog %s", got, doc.Versions.Catalog)
	}
}

// TestMergeRejectsNewerFormat: an unsupported format major exits 3 naming the file.
func TestMergeRejectsNewerFormat(t *testing.T) {
	dir := t.TempDir()
	paths := scanSplit(t, dir)
	b, err := os.ReadFile(paths["infra"])
	if err != nil {
		t.Fatal(err)
	}
	v2 := filepath.Join(dir, "v2.json")
	if err := os.WriteFile(v2, bytes.Replace(b, []byte("exitctl.result/v1"), []byte("exitctl.result/v2"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	code, msg := runMerge(t, filepath.Join(dir, "m.json"), paths["orders-app"], v2)
	if code != ExitInvalidInput {
		t.Fatalf("exit %d, want %d", code, ExitInvalidInput)
	}
	if !strings.Contains(msg, "v2.json") {
		t.Errorf("message does not name the file: %s", msg)
	}
}
