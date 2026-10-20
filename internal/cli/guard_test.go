package cli

import (
	"bytes"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudowski/exitctl/internal/result"
	"github.com/cloudowski/exitctl/internal/testutil"
)

// TestNoNetworkOrExecImports: internal packages never import network
// packages or os/exec, except internal/cli/open.go which opens a local
// report file (constitution I, T102). Mirrors the depguard rule so the
// guarantee holds even where golangci-lint does not run.
func TestNoNetworkOrExecImports(t *testing.T) {
	root := testutil.RepoRoot(t)
	banned := map[string]bool{"net": true, "net/http": true, "net/url": true, "os/exec": true}
	allowed := map[string]bool{"internal/cli/open.go": true}
	fset := token.NewFileSet()
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if banned[p] && !allowed[rel] {
				t.Errorf("%s imports %s", rel, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// seededSecrets are the fake secrets planted in testdata/fixtures/secrets,
// one or more per source kind.
var seededSecrets = []string{
	"tf-Pa55word-seeded-1",
	"AKIAIOSFODNN7EXAMPLE",
	"cfn-Pa55word-seeded-2",
	"k8s-Pa55word-seeded-3",
	"k8s-token-seeded-4",
	"docker-key-seeded-5",
	"py-secret-seeded-6",
	"ci-token-seeded-7",
}

// TestSecretsRedactedEverywhere: seeded secrets in every source kind never
// reach the result JSON, the HTML report, or the Markdown report (R17).
func TestSecretsRedactedEverywhere(t *testing.T) {
	root := testutil.RepoRoot(t)
	dir := t.TempDir()
	res := filepath.Join(dir, "result.json")
	var so, se bytes.Buffer
	if code := Run([]string{"scan", filepath.Join(root, "testdata", "fixtures", "secrets"), "--include-drafts",
		"--as-of", "2026-10-06", "--repo-name", "secrets", "--no-store", "--output", res}, &so, &se); code != 0 {
		t.Fatalf("scan exited %d: %s", code, se.String())
	}
	doc, err := result.Load(res)
	if err != nil {
		t.Fatal(err)
	}
	// Every source kind must actually be read, or the test proves nothing.
	kinds := map[string]bool{}
	for _, s := range doc.Facts.Sources {
		if s.Status == "parsed" {
			kinds[string(s.Kind)] = true
		}
	}
	for _, k := range []string{"terraform", "cloudformation", "kubernetes", "dockerfile", "github-actions", "package-manifest"} {
		if !kinds[k] {
			t.Errorf("source kind %s was not parsed", k)
		}
	}
	if len(doc.Facts.CodeUses) == 0 {
		t.Error("no code uses found; the Python secret line was not read")
	}

	outputs := map[string]string{"result.json": res}
	for _, f := range []string{"html", "md"} {
		p := filepath.Join(dir, "report."+f)
		if code := Run([]string{"report", "--input", res, "--format", f, "-o", p}, &so, &se); code != 0 {
			t.Fatalf("report %s exited %d: %s", f, code, se.String())
		}
		outputs["report."+f] = p
	}
	for _, name := range []string{"result.json", "report.html", "report.md"} {
		b, err := os.ReadFile(outputs[name])
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range seededSecrets {
			if bytes.Contains(b, []byte(s)) {
				t.Errorf("%s contains seeded secret %q", name, s)
			}
		}
	}
}
